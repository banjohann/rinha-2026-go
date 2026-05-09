// Command preprocess reads references.json.gz, quantizes each vector to
// uint16 using detector.QuantizeDim, and writes a compact binary index.
//
// Output format (little-endian throughout):
//   uint32           N            number of records
//   N × Dims × uint16             vectors, flat (record i at offset 4 + i*Dims*2)
//   N × uint8                     labels (0 = legit, 1 = fraud)
//
// Run at container build time so the runtime image just mmaps a ready-to-use
// index instead of paying ~10 s of gunzip + JSON decode on every cold start.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/johannb/rinha-2026-go/internal/detector"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: preprocess <input.json.gz> <output.bin>")
		os.Exit(2)
	}
	t0 := time.Now()
	n, err := preprocess(os.Args[1], os.Args[2])
	if err != nil {
		log.Fatalf("preprocess: %v", err)
	}
	log.Printf("wrote %d records to %s in %s", n, os.Args[2], time.Since(t0))
}

func preprocess(inPath, outPath string) (int, error) {
	in, err := os.Open(inPath)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	gz, err := gzip.NewReader(in)
	if err != nil {
		return 0, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	const sizeHint = 3_000_000
	vectors := make([]uint16, 0, sizeHint*detector.Dims)
	labels := make([]uint8, 0, sizeHint)

	dec := json.NewDecoder(gz)
	t, err := dec.Token()
	if err != nil {
		return 0, fmt.Errorf("token: %w", err)
	}
	if d, ok := t.(json.Delim); !ok || d != '[' {
		return 0, fmt.Errorf("expected JSON array, got %v", t)
	}

	var rec struct {
		Vector [detector.Dims]float32 `json:"vector"`
		Label  string                 `json:"label"`
	}
	for dec.More() {
		if err := dec.Decode(&rec); err != nil {
			return 0, fmt.Errorf("record %d: %w", len(labels), err)
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

	out, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	bw := bufio.NewWriter(out)
	if err := binary.Write(bw, binary.LittleEndian, uint32(len(labels))); err != nil {
		return 0, err
	}
	if err := binary.Write(bw, binary.LittleEndian, vectors); err != nil {
		return 0, err
	}
	if _, err := bw.Write(labels); err != nil {
		return 0, err
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	return len(labels), nil
}
