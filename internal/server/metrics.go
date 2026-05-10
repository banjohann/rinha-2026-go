package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// Feature flags read once at process start.
//
//	METRICS=1  enables per-stage histograms + /debug/metrics
//	PPROF=1    enables /debug/pprof/*
//
// Both default to off so production prévia runs pay zero cost.
var (
	metricsEnabled = os.Getenv("METRICS") == "1"
	pprofEnabled   = os.Getenv("PPROF") == "1"
)

const histBuckets = 16

// Bucket upper bounds (nanoseconds). Last bucket is +Inf.
var bucketBoundsNs = [histBuckets]int64{
	10_000, 50_000, 100_000, 250_000, 500_000,
	1_000_000, 2_000_000, 5_000_000, 10_000_000, 25_000_000,
	50_000_000, 100_000_000, 250_000_000, 500_000_000,
	1_000_000_000,
	1 << 62,
}

type histogram struct {
	name    string
	buckets [histBuckets]atomic.Uint64
	sum     atomic.Int64 // ns
}

func (h *histogram) observe(d time.Duration) {
	ns := int64(d)
	for i, b := range bucketBoundsNs {
		if ns <= b {
			h.buckets[i].Add(1)
			break
		}
	}
	h.sum.Add(ns)
}

var (
	hTotal     = &histogram{name: "total"}
	hDecode    = &histogram{name: "decode"}
	hVectorize = &histogram{name: "vectorize"}
	hCentroids = &histogram{name: "centroids"}
	hIVFScan   = &histogram{name: "ivf_scan"}
	hEncode    = &histogram{name: "encode"}

	requestsTotal atomic.Uint64
	inflightCur   atomic.Int64
	inflightMax   atomic.Int64
)

func incInflight() {
	n := inflightCur.Add(1)
	for {
		m := inflightMax.Load()
		if n <= m || inflightMax.CompareAndSwap(m, n) {
			return
		}
	}
}

func decInflight() { inflightCur.Add(-1) }

func handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# metrics_enabled=%v pprof_enabled=%v\n", metricsEnabled, pprofEnabled)
	fmt.Fprintf(w, "requests_total %d\n", requestsTotal.Load())
	fmt.Fprintf(w, "inflight_current %d\n", inflightCur.Load())
	fmt.Fprintf(w, "inflight_max %d\n", inflightMax.Load())
	fmt.Fprintln(w)
	for _, h := range []*histogram{hTotal, hDecode, hVectorize, hCentroids, hIVFScan, hEncode} {
		writeHistogram(w, h)
	}
}

func writeHistogram(w io.Writer, h *histogram) {
	var counts [histBuckets]uint64
	var total uint64
	for i := 0; i < histBuckets; i++ {
		counts[i] = h.buckets[i].Load()
		total += counts[i]
	}
	sumNs := h.sum.Load()
	var avgUs float64
	if total > 0 {
		avgUs = float64(sumNs) / float64(total) / 1000
	}
	p50 := percentileLabel(counts, total, 0.50)
	p99 := percentileLabel(counts, total, 0.99)
	fmt.Fprintf(w, "# %s count=%d avg_us=%.2f p50=%s p99=%s\n", h.name, total, avgUs, p50, p99)
	var cum uint64
	for i, b := range bucketBoundsNs {
		cum += counts[i]
		fmt.Fprintf(w, "%s_bucket{le=\"%s\"} %d\n", h.name, bucketLabel(b), cum)
	}
	fmt.Fprintln(w)
}

func percentileLabel(counts [histBuckets]uint64, total uint64, p float64) string {
	if total == 0 {
		return "n/a"
	}
	target := uint64(float64(total) * p)
	if target == 0 {
		target = 1
	}
	var cum uint64
	for i, b := range bucketBoundsNs {
		cum += counts[i]
		if cum >= target {
			return bucketLabel(b)
		}
	}
	return "+Inf"
}

func bucketLabel(ns int64) string {
	switch {
	case ns >= 1<<60:
		return "+Inf"
	case ns >= 1_000_000_000:
		return fmt.Sprintf("%ds", ns/1_000_000_000)
	case ns >= 1_000_000:
		return fmt.Sprintf("%dms", ns/1_000_000)
	case ns >= 1_000:
		return fmt.Sprintf("%dus", ns/1_000)
	default:
		return fmt.Sprintf("%dns", ns)
	}
}
