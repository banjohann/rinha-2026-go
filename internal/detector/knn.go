package detector

import (
	"runtime"
	"sync"
)

// K is the number of nearest neighbors used in the fraud vote.
const K = 5

// minParallelN is the dataset-size threshold below which we just do a
// single sequential scan. Spawning goroutines for a few thousand refs
// costs more in scheduler overhead than it saves.
const minParallelN = 1024

// sentinelGapSq is the squared distance contribution when one side of a
// dimension is the sentinel and the other is not. Equal to the maximum
// possible squared difference between two non-sentinel quantized values.
//
// QuantScale² = 65534² = 4_294_705_156 — fits in uint32; the per-query
// accumulator uses uint64 to safely sum 14 such contributions.
const sentinelGapSq uint64 = uint64(QuantScale) * uint64(QuantScale)

// knnWorkers picks the number of goroutines used per query. Capped to
// keep scheduler overhead low on the small CPU quota each container has.
//
// We don't import golang.org/x/automaxprocs (stdlib-only constraint), so
// GOMAXPROCS reflects the host CPU count, not the container quota.
// 4 is a pragmatic upper bound: the test machine is a 4-thread Mac Mini
// and our container's CPU share splits across at most that many cores.
func knnWorkers() int {
	w := runtime.GOMAXPROCS(0)
	if w > 4 {
		w = 4
	}
	if w < 1 {
		w = 1
	}
	return w
}

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
	// Use unsigned absolute diff so the squaring stays in uint64 range.
	// With uint16 values, max diff is 65534 and 65534² = 4_294_705_156,
	// which exceeds int32 — multiplying as int32 would silently overflow.
	var diff uint32
	if a > b {
		diff = uint32(a) - uint32(b)
	} else {
		diff = uint32(b) - uint32(a)
	}
	return uint64(diff) * uint64(diff)
}

// fixedHeap is a max-heap of size K used to track the K smallest
// distances seen so far. The largest distance sits at index 0 so we
// can compare incoming distances against the current worst keeper.
type fixedHeap struct {
	dist  [K]uint64
	label [K]uint8
	size  int
}

// max returns the current worst (largest) distance in the heap.
// Only valid when the heap is full.
func (h *fixedHeap) max() uint64 { return h.dist[0] }

// push inserts (d, label). When the heap is full, the largest element
// is replaced if d is smaller, then sift-down restores the max-heap.
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

// fraudCount returns the number of LabelFraud entries currently in the heap.
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
// and returns the local heap. Used both as the sequential implementation
// and as the per-worker function during parallel scans.
func (s *Store) scanRange(q [Dims]uint16, start, end int) fixedHeap {
	var h fixedHeap
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
	return h
}

// TopKFraudCount scans all references and returns how many of the K
// nearest are labeled as fraud. The caller derives fraud_score from this.
//
// For datasets above minParallelN, the work is split across knnWorkers()
// goroutines, each maintaining its own heap; the K survivors from each
// worker are merged into a final heap.
func (s *Store) TopKFraudCount(q [Dims]uint16) int {
	if s.N == 0 {
		return 0
	}
	workers := knnWorkers()
	if workers <= 1 || s.N < minParallelN {
		h := s.scanRange(q, 0, s.N)
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
			locals[w] = s.scanRange(q, start, end)
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
