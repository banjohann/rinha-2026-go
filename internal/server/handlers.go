package server

import (
	"encoding/json"
	"net/http"

	"github.com/johannb/rinha-2026-go/internal/detector"
)

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

	var req detector.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	v := detector.Vectorize(&req, s.mcc, s.norm)
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
