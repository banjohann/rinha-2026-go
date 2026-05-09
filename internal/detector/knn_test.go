package detector

import (
	"math/rand/v2"
	"sort"
	"testing"
)

// naiveTopK computes the K nearest by full sort, used as oracle for the
// fixed-heap implementation. Distance metric must match dimContrib.
func naiveTopK(s *Store, q [Dims]uint16) int {
	type rec struct {
		d uint64
		l uint8
	}
	all := make([]rec, s.N)
	for i := 0; i < s.N; i++ {
		base := i * Dims
		var d uint64
		for j := 0; j < Dims; j++ {
			d += dimContrib(q[j], s.Vectors[base+j])
		}
		all[i] = rec{d: d, l: s.Labels[i]}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].d < all[j].d })
	c := 0
	for i := 0; i < K && i < len(all); i++ {
		if all[i].l == LabelFraud {
			c++
		}
	}
	return c
}

func makeRandomStore(n int, fraudRatio float64, r *rand.Rand) *Store {
	s := &Store{
		Vectors: make([]uint16, n*Dims),
		Labels:  make([]uint8, n),
		N:       n,
	}
	for i := 0; i < n; i++ {
		for j := 0; j < Dims; j++ {
			// Range [0, 65535] inclusive — exercises sentinel path too.
			s.Vectors[i*Dims+j] = uint16(r.Uint32N(65536))
		}
		if r.Float64() < fraudRatio {
			s.Labels[i] = LabelFraud
		}
	}
	return s
}

func TestTopKFraudCountAgainstNaive(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	s := makeRandomStore(2000, 0.35, r)

	for trial := 0; trial < 50; trial++ {
		var q [Dims]uint16
		for j := 0; j < Dims; j++ {
			q[j] = uint16(r.Uint32N(65536))
		}
		got := s.TopKFraudCount(q)
		want := naiveTopK(s, q)
		if got != want {
			t.Fatalf("trial %d: TopKFraudCount=%d, naive=%d", trial, got, want)
		}
	}
}

func TestTopKAllFraud(t *testing.T) {
	s := &Store{N: K, Labels: make([]uint8, K), Vectors: make([]uint16, K*Dims)}
	for i := 0; i < K; i++ {
		s.Labels[i] = LabelFraud
		for j := 0; j < Dims; j++ {
			s.Vectors[i*Dims+j] = uint16(i)
		}
	}
	var q [Dims]uint16
	if got := s.TopKFraudCount(q); got != K {
		t.Fatalf("want %d fraud neighbors, got %d", K, got)
	}
}

func TestTopKAllLegit(t *testing.T) {
	s := &Store{N: K, Labels: make([]uint8, K), Vectors: make([]uint16, K*Dims)}
	var q [Dims]uint16
	if got := s.TopKFraudCount(q); got != 0 {
		t.Fatalf("want 0 fraud neighbors, got %d", got)
	}
}

func TestSentinelHandling(t *testing.T) {
	// 5 zero-vector refs (legit) + 5 refs with sentinels at dims 5,6 (fraud).
	// A query carrying sentinels at dims 5,6 should pick the sentinel-matching
	// fraud refs as its K nearest.
	bigS := &Store{N: 10, Labels: make([]uint8, 10), Vectors: make([]uint16, 10*Dims)}
	for i := 0; i < 5; i++ {
		bigS.Labels[i] = LabelLegit
	}
	for i := 5; i < 10; i++ {
		bigS.Labels[i] = LabelFraud
		bigS.Vectors[i*Dims+5] = Sentinel
		bigS.Vectors[i*Dims+6] = Sentinel
	}
	var q [Dims]uint16
	q[5] = Sentinel
	q[6] = Sentinel
	if got := bigS.TopKFraudCount(q); got != K {
		t.Fatalf("sentinel query should pick all sentinel refs, got %d fraud neighbors (want %d)", got, K)
	}
}
