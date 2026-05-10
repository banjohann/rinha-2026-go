package detector

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const (
	LabelLegit uint8 = 0
	LabelFraud uint8 = 1

	BinaryMagic   = uint32(0x52494e48) // "RINH"
	BinaryVersion = uint16(2)
)

// Store holds the reference dataset in compact uint16 form, optionally
// with an IVF (inverted-file) cluster index for sub-millisecond k-NN.
//
// Vectors is a flat slice of length N*Dims; record i occupies
// [i*Dims : (i+1)*Dims]. When K > 0, records are reordered by cluster
// assignment so each cluster's vectors are contiguous, and:
//
//   - Centroids[c*Dims : (c+1)*Dims] is the centroid of cluster c
//   - Cluster c spans records [Offsets[c], Offsets[c+1])
//
// When K == 0 (legacy / test fixture), TopKFraudCount falls back to
// a brute-force scan over all records.
type Store struct {
	Vectors   []uint16
	Labels    []uint8
	Centroids []uint16
	Offsets   []uint32
	N         int
	K         int
}

// referenceRecord matches references.json.gz: { "vector": [...], "label": "fraud"|"legit" }.
type referenceRecord struct {
	Vector [Dims]float32 `json:"vector"`
	Label  string        `json:"label"`
}

// LoadStore reads a (possibly gzipped) JSON array of reference records
// and quantizes them into a Store with no IVF index (K=0). Used for
// tests and as a developer-friendly fallback; production runs go
// through LoadStoreFromBinary.
func LoadStore(path string, sizeHint int) (*Store, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var r io.Reader = f
	if hasGzipExt(path) {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		r = gz
	}

	return loadStoreFromJSON(r, sizeHint)
}

func loadStoreFromJSON(r io.Reader, sizeHint int) (*Store, error) {
	if sizeHint <= 0 {
		sizeHint = 1024
	}
	s := &Store{
		Vectors: make([]uint16, 0, sizeHint*Dims),
		Labels:  make([]uint8, 0, sizeHint),
	}

	dec := json.NewDecoder(r)
	t, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("expected '[': %w", err)
	}
	if d, ok := t.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("expected JSON array, got %v", t)
	}

	for dec.More() {
		var rec referenceRecord
		if err := dec.Decode(&rec); err != nil {
			return nil, fmt.Errorf("decode record %d: %w", s.N, err)
		}
		for i := 0; i < Dims; i++ {
			s.Vectors = append(s.Vectors, QuantizeDim(rec.Vector[i]))
		}
		s.Labels = append(s.Labels, labelToByte(rec.Label))
		s.N++
	}

	return s, nil
}

func labelToByte(l string) uint8 {
	if l == "fraud" {
		return LabelFraud
	}
	return LabelLegit
}

func hasGzipExt(path string) bool {
	n := len(path)
	return n >= 3 && path[n-3:] == ".gz"
}

// LoadStoreFromBinary reads the IVF binary index produced by cmd/preprocess.
// See cmd/preprocess/main.go for the format documentation.
//
// Bypasses JSON parsing and gzip decompression entirely, so cold-start
// drops from ~10 s to ~200 ms.
func LoadStoreFromBinary(path string) (*Store, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readStoreBinary(bufio.NewReaderSize(f, 4<<20))
}

func readStoreBinary(r io.Reader) (*Store, error) {
	var magic uint32
	if err := binary.Read(r, binary.LittleEndian, &magic); err != nil {
		return nil, fmt.Errorf("read magic: %w", err)
	}
	if magic != BinaryMagic {
		return nil, fmt.Errorf("bad magic: got %#x, want %#x — regenerate with cmd/preprocess", magic, BinaryMagic)
	}

	var version uint16
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return nil, fmt.Errorf("read version: %w", err)
	}
	if version != BinaryVersion {
		return nil, fmt.Errorf("unsupported binary version %d (this build expects %d)", version, BinaryVersion)
	}

	var dims uint16
	if err := binary.Read(r, binary.LittleEndian, &dims); err != nil {
		return nil, fmt.Errorf("read dims: %w", err)
	}
	if int(dims) != Dims {
		return nil, fmt.Errorf("dims mismatch: file says %d, build expects %d", dims, Dims)
	}

	var n, k uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return nil, fmt.Errorf("read N: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &k); err != nil {
		return nil, fmt.Errorf("read K: %w", err)
	}

	s := &Store{
		N:         int(n),
		K:         int(k),
		Vectors:   make([]uint16, int(n)*Dims),
		Labels:    make([]uint8, int(n)),
		Centroids: make([]uint16, int(k)*Dims),
		Offsets:   make([]uint32, int(k)+1),
	}
	if err := binary.Read(r, binary.LittleEndian, s.Vectors); err != nil {
		return nil, fmt.Errorf("read vectors: %w", err)
	}
	if _, err := io.ReadFull(r, s.Labels); err != nil {
		return nil, fmt.Errorf("read labels: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, s.Centroids); err != nil {
		return nil, fmt.Errorf("read centroids: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, s.Offsets); err != nil {
		return nil, fmt.Errorf("read offsets: %w", err)
	}
	return s, nil
}
