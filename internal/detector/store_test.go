package detector

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func writeGzipJSON(t *testing.T, dir, name string, payload any) string {
	t.Helper()
	var raw bytes.Buffer
	if err := json.NewEncoder(&raw).Encode(payload); err != nil {
		t.Fatalf("encode: %v", err)
	}
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	if _, err := gz.Write(raw.Bytes()); err != nil {
		t.Fatalf("gz write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return p
}

func TestLoadStoreFromGzip(t *testing.T) {
	dir := t.TempDir()
	records := []map[string]any{
		{
			"vector": []float32{0.0041, 0.1667, 0.05, 0.7826, 0.3333, -1, -1, 0.0292, 0.15, 0, 1, 0, 0.15, 0.006},
			"label":  "legit",
		},
		{
			"vector": []float32{0.95, 0.83, 1.0, 0.21, 0.83, -1, -1, 0.95, 1.0, 0, 1, 1, 0.75, 0.005},
			"label":  "fraud",
		},
	}
	path := writeGzipJSON(t, dir, "refs.json.gz", records)

	s, err := LoadStore(path, 2)
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	if s.N != 2 {
		t.Fatalf("N = %d, want 2", s.N)
	}
	if s.Labels[0] != LabelLegit || s.Labels[1] != LabelFraud {
		t.Fatalf("labels: %v", s.Labels)
	}
	// Verify sentinel positions for the legit record.
	if s.Vectors[5] != Sentinel || s.Vectors[6] != Sentinel {
		t.Fatalf("expected sentinels at dims 5,6 of record 0; got %d, %d", s.Vectors[5], s.Vectors[6])
	}
}

func TestLoadStoreFromBinaryRoundTrip(t *testing.T) {
	// Hand-write a tiny index.bin and verify LoadStoreFromBinary reads it back.
	dir := t.TempDir()
	path := filepath.Join(dir, "index.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 2 records: dims=14 each. record 0 legit (label 0), record 1 fraud (label 1).
	const n = 2
	if err := writeBinIndex(f, n,
		[]uint16{
			0, 1, 2, 3, 4, Sentinel, Sentinel, 7, 8, 9, 10, 11, 12, 13,
			100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113,
		},
		[]uint8{LabelLegit, LabelFraud},
	); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	s, err := LoadStoreFromBinary(path)
	if err != nil {
		t.Fatalf("LoadStoreFromBinary: %v", err)
	}
	if s.N != n {
		t.Fatalf("N = %d, want %d", s.N, n)
	}
	if s.Vectors[5] != Sentinel || s.Vectors[6] != Sentinel {
		t.Fatalf("expected sentinels at dims 5,6 of record 0; got %d, %d", s.Vectors[5], s.Vectors[6])
	}
	if s.Vectors[Dims] != 100 {
		t.Fatalf("record 1 dim 0: want 100, got %d", s.Vectors[Dims])
	}
	if s.Labels[0] != LabelLegit || s.Labels[1] != LabelFraud {
		t.Fatalf("labels: %v", s.Labels)
	}
}

func writeBinIndex(w io.Writer, n int, vectors []uint16, labels []uint8) error {
	if err := binary.Write(w, binary.LittleEndian, uint32(n)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, vectors); err != nil {
		return err
	}
	_, err := w.Write(labels)
	return err
}

func TestLoadStorePlainJSON(t *testing.T) {
	dir := t.TempDir()
	records := []map[string]any{
		{"vector": []float32{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "label": "legit"},
	}
	p := filepath.Join(dir, "refs.json")
	b, _ := json.Marshal(records)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := LoadStore(p, 1)
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	if s.N != 1 {
		t.Fatalf("N = %d, want 1", s.N)
	}
}
