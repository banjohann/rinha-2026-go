package detector

import (
	"runtime"
	"sync"
)

// K is the number of nearest neighbors used in the fraud vote.
const K = 5

// PProbes is the number of clusters to scan per query in the IVF path.
// 3 gives recall@5 ≈ 98 % at the cost of scanning 3× as many records as
// a single-probe lookup. Tunable.
const PProbes = 3

// minParallelN is the dataset-size threshold below which the brute-force
// fallback runs sequentially. Spawning goroutines for a few thousand
// refs costs more in scheduler overhead than it saves.
const minParallelN = 1024

// sentinelGapSq is the squared distance contribution when one side of a
// dimension is the sentinel and the other is not. Equal to the maximum
// possible squared difference between two non-sentinel quantized values.
const sentinelGapSq uint64 = uint64(QuantScale) * uint64(QuantScale)

// dimContrib returns (a-b)² with sentinel handling.
//
// Sentinel-vs-sentinel: 0 (perfect match: both are "no previous tx").
// Sentinel-vs-value:    sentinelGapSq (clusters the two cases apart).
// Value-vs-value:       (a-b)².
func dimContrib(a, b uint16) uint64 {
	if a == Sentinel || b == Sentinel {
		if a == b {
			return 0
		}
		return sentinelGapSq
	}
	var diff uint32
	if a > b {
		diff = uint32(a) - uint32(b)
	} else {
		diff = uint32(b) - uint32(a)
	}
	return uint64(diff) * uint64(diff)
}

// vecDist computes the squared distance between two flat 14-dim segments.
func vecDist(a, b []uint16) uint64 {
	var d uint64
	for j := 0; j < Dims; j++ {
		d += dimContrib(a[j], b[j])
	}
	return d
}

// fixedHeap is a max-heap of size K used to track the K smallest
// distances seen so far. The largest distance sits at index 0 so we
// can compare incoming distances against the current worst keeper.
type fixedHeap struct {
	dist  [K]uint64
	label [K]uint8
	size  int
}

func (h *fixedHeap) max() uint64 { return h.dist[0] }

func (h *fixedHeap) push(d uint64, label uint8) {
	if h.size < K {
		i := h.size
		h.dist[i] = d
		h.label[i] = label
		h.size++
		for i > 0 {
			parent := (i - 1) / 2
			if h.dist[parent] >= h.dist[i] {
				break
			}
			h.dist[parent], h.dist[i] = h.dist[i], h.dist[parent]
			h.label[parent], h.label[i] = h.label[i], h.label[parent]
			i = parent
		}
		return
	}
	if d >= h.dist[0] {
		return
	}
	h.dist[0] = d
	h.label[0] = label
	i := 0
	for {
		l := 2*i + 1
		r := 2*i + 2
		largest := i
		if l < K && h.dist[l] > h.dist[largest] {
			largest = l
		}
		if r < K && h.dist[r] > h.dist[largest] {
			largest = r
		}
		if largest == i {
			return
		}
		h.dist[i], h.dist[largest] = h.dist[largest], h.dist[i]
		h.label[i], h.label[largest] = h.label[largest], h.label[i]
		i = largest
	}
}

func (h *fixedHeap) fraudCount() int {
	c := 0
	for i := 0; i < h.size; i++ {
		if h.label[i] == LabelFraud {
			c++
		}
	}
	return c
}

// scanRange computes the K nearest neighbours within s.Vectors[start:end]
// and accumulates them into h. Used both as the brute-force fallback's
// inner loop and as the per-cluster scan in the IVF path.
func (s *Store) scanRange(q [Dims]uint16, start, end int, h *fixedHeap) {
	v := s.Vectors
	for i := start; i < end; i++ {
		base := i * Dims
		var d uint64
		full := h.size == K
		var worst uint64
		if full {
			worst = h.max()
		}
		exceeded := false
		for j := 0; j < Dims; j++ {
			d += dimContrib(q[j], v[base+j])
			if full && d >= worst {
				exceeded = true
				break
			}
		}
		if exceeded {
			continue
		}
		h.push(d, s.Labels[i])
	}
}

// topPCentroids returns the indices of the P clusters whose centroids are
// closest to q, in ascending distance order. P must be small (≤ 16).
func (s *Store) topPCentroids(q [Dims]uint16, p int) []int {
	type entry struct {
		idx  int
		dist uint64
	}
	top := make([]entry, 0, p)

	for c := 0; c < s.K; c++ {
		base := c * Dims
		// Compute distance to centroid c with early termination if we already
		// have p candidates and this one is worse than the current worst.
		full := len(top) == p
		var worst uint64
		if full {
			worst = top[len(top)-1].dist
		}
		var d uint64
		exceeded := false
		for j := 0; j < Dims; j++ {
			d += dimContrib(q[j], s.Centroids[base+j])
			if full && d >= worst {
				exceeded = true
				break
			}
		}
		if exceeded {
			continue
		}
		// Insertion sort into top — list is small (p ≤ ~16).
		ins := len(top)
		for ins > 0 && top[ins-1].dist > d {
			ins--
		}
		if len(top) < p {
			top = append(top, entry{})
		}
		copy(top[ins+1:], top[ins:len(top)-1])
		top[ins] = entry{idx: c, dist: d}
	}

	out := make([]int, len(top))
	for i, e := range top {
		out[i] = e.idx
	}
	return out
}

// TopKFraudCount scans the K nearest references and returns how many of
// them are labeled as fraud.
//
// If the Store has an IVF index (s.K > 0), the search visits only the
// PProbes nearest clusters. Otherwise it falls back to a brute-force
// scan over all records, parallelised for large N.
func (s *Store) TopKFraudCount(q [Dims]uint16) int {
	if s.N == 0 {
		return 0
	}
	if s.K > 0 {
		return s.topKIVF(q)
	}
	return s.topKBrute(q)
}

func (s *Store) topKIVF(q [Dims]uint16) int {
	clusters := s.topPCentroids(q, PProbes)
	var h fixedHeap
	for _, c := range clusters {
		start := int(s.Offsets[c])
		end := int(s.Offsets[c+1])
		s.scanRange(q, start, end, &h)
	}
	return h.fraudCount()
}

func (s *Store) topKBrute(q [Dims]uint16) int {
	workers := runtime.GOMAXPROCS(0)
	if workers > 4 {
		workers = 4
	}
	if workers <= 1 || s.N < minParallelN {
		var h fixedHeap
		s.scanRange(q, 0, s.N, &h)
		return h.fraudCount()
	}

	chunk := (s.N + workers - 1) / workers
	locals := make([]fixedHeap, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		start := w * chunk
		end := start + chunk
		if end > s.N {
			end = s.N
		}
		if start >= end {
			break
		}
		wg.Add(1)
		go func(w, start, end int) {
			defer wg.Done()
			s.scanRange(q, start, end, &locals[w])
		}(w, start, end)
	}
	wg.Wait()

	var merged fixedHeap
	for _, h := range locals {
		for j := 0; j < h.size; j++ {
			merged.push(h.dist[j], h.label[j])
		}
	}
	return merged.fraudCount()
}
