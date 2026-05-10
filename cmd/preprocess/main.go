// Command preprocess reads references.json.gz, quantizes each vector to
// uint16, runs k-means clustering (K=1024) over the reference set, and
// writes a binary index ready to be mmap-loaded at runtime.
//
// File format (all little-endian):
//
//	uint32  magic   = 0x52494e48 "RINH"
//	uint16  version = 2
//	uint16  dims    = 14
//	uint32  N                                          // number of records
//	uint32  K                                          // number of clusters
//	N × Dims uint16                                    // vectors, REORDERED by cluster
//	N × uint8                                          // labels (0=legit, 1=fraud), same order
//	K × Dims uint16                                    // cluster centroids (quantized)
//	(K+1) × uint32                                     // cluster offsets: cluster c spans [Offsets[c], Offsets[c+1])
//
// At runtime, queries find their top-P (default 3) nearest centroids and
// brute-force only those clusters, cutting per-query cost from O(N) to
// roughly O(K + (P × N/K)) — a few hundred microseconds instead of tens
// of milliseconds.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/johannb/rinha-2026-go/internal/detector"
)

const (
	KClusters     = 1024
	MaxIter       = 10
	BinaryMagic   = uint32(0x52494e48) // "RINH"
	BinaryVersion = uint16(2)
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: preprocess <input.json.gz> <output.bin>")
		os.Exit(2)
	}
	in, out := os.Args[1], os.Args[2]

	log.Printf("step 1/4: reading %s", in)
	t0 := time.Now()
	vectors, labels, err := readReferences(in)
	if err != nil {
		log.Fatalf("read: %v", err)
	}
	n := len(labels)
	log.Printf("  decoded %d records in %s", n, time.Since(t0))

	log.Printf("step 2/4: k-means K=%d, maxIter=%d", KClusters, MaxIter)
	t0 = time.Now()
	centroidsF, assignment := kmeans(vectors, n, KClusters, MaxIter)
	log.Printf("  k-means done in %s", time.Since(t0))

	log.Printf("step 3/4: reordering by cluster + quantizing centroids")
	t0 = time.Now()
	reorderedVectors, reorderedLabels, offsets := reorder(vectors, labels, assignment, KClusters)
	centroids := quantizeCentroids(centroidsF)
	log.Printf("  reorder + quantize done in %s", time.Since(t0))

	log.Printf("step 4/4: writing %s", out)
	t0 = time.Now()
	if err := writeBinary(out, reorderedVectors, reorderedLabels, centroids, offsets, n, KClusters); err != nil {
		log.Fatalf("write: %v", err)
	}
	log.Printf("  wrote %d records, %d clusters in %s", n, KClusters, time.Since(t0))
}

// readReferences decodes the gzipped JSON array of records and returns the
// flat quantized vectors + labels.
func readReferences(path string) (vectors []uint16, labels []uint8, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, nil, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	const sizeHint = 3_000_000
	vectors = make([]uint16, 0, sizeHint*detector.Dims)
	labels = make([]uint8, 0, sizeHint)

	dec := json.NewDecoder(gz)
	t, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '[' {
		return nil, nil, fmt.Errorf("expected JSON array, got %v", t)
	}

	var rec struct {
		Vector [detector.Dims]float32 `json:"vector"`
		Label  string                 `json:"label"`
	}
	for dec.More() {
		if err := dec.Decode(&rec); err != nil {
			return nil, nil, fmt.Errorf("record %d: %w", len(labels), err)
		}
		for i := 0; i < detector.Dims; i++ {
			vectors = append(vectors, detector.QuantizeDim(rec.Vector[i]))
		}
		if rec.Label == "fraud" {
			labels = append(labels, 1)
		} else {
			labels = append(labels, 0)
		}
	}
	return vectors, labels, nil
}

// vecAsFloat dequantizes a single record into a fresh []float32. Sentinel
// (65535) becomes -1.0; otherwise scaled by QuantScale.
func vecAsFloat(vectors []uint16, i int) []float32 {
	out := make([]float32, detector.Dims)
	for j := 0; j < detector.Dims; j++ {
		v := vectors[i*detector.Dims+j]
		if v == detector.Sentinel {
			out[j] = -1
		} else {
			out[j] = float32(v) / float32(detector.QuantScale)
		}
	}
	return out
}

// vecToFloat dequantizes record i directly into the provided slice.
func vecToFloat(vectors []uint16, i int, out []float32) {
	for j := 0; j < detector.Dims; j++ {
		v := vectors[i*detector.Dims+j]
		if v == detector.Sentinel {
			out[j] = -1
		} else {
			out[j] = float32(v) / float32(detector.QuantScale)
		}
	}
}

// distFloat computes squared Euclidean distance between a quantized record
// and a float centroid. The dequantize is inlined per dim to avoid the
// allocation in vecAsFloat.
func distFloat(vectors []uint16, i int, centroid []float32) float32 {
	var d float32
	for j := 0; j < detector.Dims; j++ {
		v := vectors[i*detector.Dims+j]
		var fv float32
		if v == detector.Sentinel {
			fv = -1
		} else {
			fv = float32(v) / float32(detector.QuantScale)
		}
		diff := fv - centroid[j]
		d += diff * diff
	}
	return d
}

// kmeans clusters the N records into K groups using Lloyd's algorithm
// with random initialization. Centroids are tracked in float32 for the
// averaging step; they are quantized back to uint16 only at the end.
func kmeans(vectors []uint16, n, k, maxIter int) (centroids [][]float32, assignment []uint16) {
	r := rand.New(rand.NewPCG(42, 0xC0FFEE))

	centroids = make([][]float32, k)
	chosen := make(map[int]bool, k)
	for i := 0; i < k; i++ {
		var idx int
		for {
			idx = r.IntN(n)
			if !chosen[idx] {
				chosen[idx] = true
				break
			}
		}
		centroids[i] = vecAsFloat(vectors, idx)
	}

	assignment = make([]uint16, n)
	for i := range assignment {
		assignment[i] = math.MaxUint16
	}

	for iter := 0; iter < maxIter; iter++ {
		t0 := time.Now()
		changed := assignParallel(vectors, n, centroids, assignment)
		updateCentroids(vectors, n, k, assignment, centroids)
		log.Printf("  iter %d: %d reassignments in %s", iter+1, changed, time.Since(t0))
		if changed == 0 {
			break
		}
	}
	return centroids, assignment
}

// assignParallel splits the assignment step across NumCPU workers.
// Returns the number of records whose assigned cluster changed.
func assignParallel(vectors []uint16, n int, centroids [][]float32, assignment []uint16) int {
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	chunk := (n + workers - 1) / workers

	var wg sync.WaitGroup
	changedPerWorker := make([]int, workers)
	for w := 0; w < workers; w++ {
		start := w * chunk
		end := start + chunk
		if end > n {
			end = n
		}
		if start >= end {
			break
		}
		wg.Add(1)
		go func(w, start, end int) {
			defer wg.Done()
			local := 0
			for i := start; i < end; i++ {
				bestK := uint16(0)
				bestD := float32(math.MaxFloat32)
				for c := 0; c < len(centroids); c++ {
					d := distFloat(vectors, i, centroids[c])
					if d < bestD {
						bestD = d
						bestK = uint16(c)
					}
				}
				if assignment[i] != bestK {
					assignment[i] = bestK
					local++
				}
			}
			changedPerWorker[w] = local
		}(w, start, end)
	}
	wg.Wait()

	total := 0
	for _, c := range changedPerWorker {
		total += c
	}
	return total
}

// updateCentroids replaces each centroid with the mean of its assigned
// records (in float space, with sentinels treated as -1.0).
func updateCentroids(vectors []uint16, n, k int, assignment []uint16, centroids [][]float32) {
	sums := make([][]float32, k)
	counts := make([]int, k)
	for i := range sums {
		sums[i] = make([]float32, detector.Dims)
	}

	tmp := make([]float32, detector.Dims)
	for i := 0; i < n; i++ {
		c := assignment[i]
		counts[c]++
		vecToFloat(vectors, i, tmp)
		for j := 0; j < detector.Dims; j++ {
			sums[c][j] += tmp[j]
		}
	}

	for c := 0; c < k; c++ {
		if counts[c] == 0 {
			continue
		}
		inv := 1.0 / float32(counts[c])
		for j := 0; j < detector.Dims; j++ {
			centroids[c][j] = sums[c][j] * inv
		}
	}
}

// quantizeCentroids converts float centroids back to uint16 using the
// same QuantizeDim mapping as the records: -1 → Sentinel, [0,1] → [0, QuantScale].
func quantizeCentroids(centroidsF [][]float32) []uint16 {
	out := make([]uint16, len(centroidsF)*detector.Dims)
	for c, vec := range centroidsF {
		for j, f := range vec {
			out[c*detector.Dims+j] = detector.QuantizeDim(f)
		}
	}
	return out
}

// reorder produces new vectors and labels slices sorted by cluster
// assignment, plus a (K+1)-entry offsets table where cluster c spans
// [offsets[c], offsets[c+1]). This makes per-cluster scans contiguous.
func reorder(vectors []uint16, labels []uint8, assignment []uint16, k int) (rv []uint16, rl []uint8, offsets []uint32) {
	n := len(labels)

	counts := make([]uint32, k)
	for _, c := range assignment {
		counts[c]++
	}

	offsets = make([]uint32, k+1)
	var cur uint32
	for c := 0; c < k; c++ {
		offsets[c] = cur
		cur += counts[c]
	}
	offsets[k] = cur

	rv = make([]uint16, n*detector.Dims)
	rl = make([]uint8, n)

	cursors := make([]uint32, k)
	copy(cursors, offsets[:k])
	for i := 0; i < n; i++ {
		c := assignment[i]
		pos := int(cursors[c])
		copy(rv[pos*detector.Dims:(pos+1)*detector.Dims], vectors[i*detector.Dims:(i+1)*detector.Dims])
		rl[pos] = labels[i]
		cursors[c]++
	}

	return rv, rl, offsets
}

// writeBinary serializes the index in the documented format.
func writeBinary(path string, vectors []uint16, labels []uint8, centroids []uint16, offsets []uint32, n, k int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	bw := bufio.NewWriterSize(f, 4<<20)

	if err := binary.Write(bw, binary.LittleEndian, BinaryMagic); err != nil {
		return err
	}
	if err := binary.Write(bw, binary.LittleEndian, BinaryVersion); err != nil {
		return err
	}
	if err := binary.Write(bw, binary.LittleEndian, uint16(detector.Dims)); err != nil {
		return err
	}
	if err := binary.Write(bw, binary.LittleEndian, uint32(n)); err != nil {
		return err
	}
	if err := binary.Write(bw, binary.LittleEndian, uint32(k)); err != nil {
		return err
	}
	if err := binary.Write(bw, binary.LittleEndian, vectors); err != nil {
		return err
	}
	if _, err := bw.Write(labels); err != nil {
		return err
	}
	if err := binary.Write(bw, binary.LittleEndian, centroids); err != nil {
		return err
	}
	if err := binary.Write(bw, binary.LittleEndian, offsets); err != nil {
		return err
	}
	return bw.Flush()
}
