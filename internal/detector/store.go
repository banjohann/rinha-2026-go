package detector

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const (
	LabelLegit uint8 = 0
	LabelFraud uint8 = 1
)

// Store holds the reference dataset in compact uint16 form.
//
// Vectors is a flat slice of length N*Dims; the i-th reference occupies
// [i*Dims : (i+1)*Dims]. Labels[i] is LabelLegit or LabelFraud.
type Store struct {
	Vectors []uint16
	Labels  []uint8
	N       int
}

// referenceRecord matches references.json.gz: { "vector": [...], "label": "fraud"|"legit" }.
type referenceRecord struct {
	Vector [Dims]float32 `json:"vector"`
	Label  string        `json:"label"`
}

// LoadStore reads a (possibly gzipped) JSON array of reference records
// and quantizes them into a Store. The file path is auto-detected as
// gzipped when the extension is .gz.
//
// The decoder streams the input so peak memory is bounded by the
// resulting Store plus a small per-record buffer.
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
