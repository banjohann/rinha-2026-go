package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/johannb/rinha-2026-go/internal/detector"
)

// reqPool reuses Request structs to avoid per-request heap allocation.
// A reset clears the slice in Customer.KnownMerchants to drop the
// previous request's reference (so the underlying array can be reused
// when JSON decode appends to it).
var reqPool = sync.Pool{
	New: func() any { return new(detector.Request) },
}

func resetRequest(r *detector.Request) {
	r.ID = ""
	r.Transaction = detector.Transaction{}
	r.Customer.AvgAmount = 0
	r.Customer.TxCount24h = 0
	r.Customer.KnownMerchants = r.Customer.KnownMerchants[:0]
	r.Merchant = detector.Merchant{}
	r.Terminal = detector.Terminal{}
	r.LastTx = nil
}

func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	if s.store.Load() == nil {
		http.Error(w, "loading", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleFraudScore(w http.ResponseWriter, r *http.Request) {
	var t0 time.Time
	if metricsEnabled {
		t0 = time.Now()
		requestsTotal.Add(1)
		incInflight()
		defer decInflight()
	}

	st := s.store.Load()
	if st == nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}

	req := reqPool.Get().(*detector.Request)
	resetRequest(req)
	defer reqPool.Put(req)

	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var t1 time.Time
	if metricsEnabled {
		t1 = time.Now()
		hDecode.observe(t1.Sub(t0))
	}

	v := detector.Vectorize(req, s.mcc, s.norm)
	q := detector.QuantizeVector(v)

	var t2 time.Time
	if metricsEnabled {
		t2 = time.Now()
		hVectorize.observe(t2.Sub(t1))
	}

	var fc int
	if metricsEnabled {
		var ts detector.Timings
		fc = st.TopKFraudCountTimed(q, &ts)
		hCentroids.observe(ts.Centroids)
		hIVFScan.observe(ts.Scan)
	} else {
		fc = st.TopKFraudCount(q)
	}

	var t3 time.Time
	if metricsEnabled {
		t3 = time.Now()
	}

	score := float32(fc) / float32(detector.K)
	resp := detector.Response{
		Approved:   score < 0.6,
		FraudScore: score,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&resp)

	if metricsEnabled {
		t4 := time.Now()
		hEncode.observe(t4.Sub(t3))
		hTotal.observe(t4.Sub(t0))
	}
}
