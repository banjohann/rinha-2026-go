package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johannb/rinha-2026-go/internal/detector"
)

func newTestServer() *Server {
	norm := detector.Constants{
		MaxAmount: 10000, MaxInstallments: 12, AmountVsAvgRatio: 10,
		MaxMinutes: 1440, MaxKm: 1000, MaxTxCount24h: 20,
		MaxMerchantAvgAmount: 10000,
	}
	return New(detector.MCCRisk{"5411": 0.15}, norm)
}

func TestReadyBeforeStore(t *testing.T) {
	s := newTestServer()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 before store loaded, got %d", rr.Code)
	}
}

func TestReadyAfterStore(t *testing.T) {
	s := newTestServer()
	s.SetStore(&detector.Store{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 after store set, got %d", rr.Code)
	}
}

func TestFraudScoreAllFraudNeighbors(t *testing.T) {
	s := newTestServer()
	// Build a tiny store where all K nearest are fraud.
	store := &detector.Store{
		N:       detector.K,
		Labels:  make([]uint8, detector.K),
		Vectors: make([]uint16, detector.K*detector.Dims),
	}
	for i := 0; i < detector.K; i++ {
		store.Labels[i] = detector.LabelFraud
	}
	s.SetStore(store)

	body := `{
		"id": "tx-1",
		"transaction": { "amount": 1, "installments": 1, "requested_at": "2026-03-11T18:00:00Z" },
		"customer":    { "avg_amount": 1, "tx_count_24h": 0, "known_merchants": [] },
		"merchant":    { "id": "M1", "mcc": "5411", "avg_amount": 1 },
		"terminal":    { "is_online": false, "card_present": true, "km_from_home": 0 },
		"last_transaction": null
	}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fraud-score", strings.NewReader(body))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp detector.Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.FraudScore != 1.0 {
		t.Fatalf("fraud_score = %v, want 1.0", resp.FraudScore)
	}
	if resp.Approved {
		t.Fatalf("approved = true, want false")
	}
}

func TestFraudScoreAllLegitNeighbors(t *testing.T) {
	s := newTestServer()
	store := &detector.Store{
		N:       detector.K,
		Labels:  make([]uint8, detector.K),
		Vectors: make([]uint16, detector.K*detector.Dims),
	}
	s.SetStore(store)

	body := `{
		"id": "tx-2",
		"transaction": { "amount": 1, "installments": 1, "requested_at": "2026-03-11T18:00:00Z" },
		"customer":    { "avg_amount": 1, "tx_count_24h": 0, "known_merchants": ["M1"] },
		"merchant":    { "id": "M1", "mcc": "5411", "avg_amount": 1 },
		"terminal":    { "is_online": false, "card_present": true, "km_from_home": 0 },
		"last_transaction": null
	}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fraud-score", strings.NewReader(body))
	s.Handler().ServeHTTP(rr, req)
	var resp detector.Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.FraudScore != 0 {
		t.Fatalf("fraud_score = %v, want 0", resp.FraudScore)
	}
	if !resp.Approved {
		t.Fatalf("approved = false, want true")
	}
}

func TestFraudScoreBadJSON(t *testing.T) {
	s := newTestServer()
	s.SetStore(&detector.Store{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fraud-score", strings.NewReader("{not json"))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 on bad JSON, got %d", rr.Code)
	}
}
