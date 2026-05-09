package server

import (
	"encoding/json"
	"net/http"
	"sync"

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

	v := detector.Vectorize(req, s.mcc, s.norm)
	q := detector.QuantizeVector(v)
	fc := st.TopKFraudCount(q)
	score := float32(fc) / float32(detector.K)

	resp := detector.Response{
		Approved:   score < 0.6,
		FraudScore: score,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&resp)
}
