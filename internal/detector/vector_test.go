package detector

import (
	"math"
	"testing"
)

func testNorm() Constants {
	return Constants{
		MaxAmount:            10000,
		MaxInstallments:      12,
		AmountVsAvgRatio:     10,
		MaxMinutes:           1440,
		MaxKm:                1000,
		MaxTxCount24h:        20,
		MaxMerchantAvgAmount: 10000,
	}
}

func testMCC() MCCRisk {
	return MCCRisk{
		"5411": 0.15,
		"7802": 0.75,
	}
}

func approxEq(a, b float32, tol float32) bool {
	return float32(math.Abs(float64(a-b))) <= tol
}

// TestVectorizeLegitNullLastTx mirrors the worked example in
// REGRAS_DE_DETECCAO.md (legit transaction with last_transaction: null).
//
// Expected vector (4-decimal rounded, per the spec):
//   [0.0041, 0.1667, 0.05, 0.7826, 0.3333, -1, -1, 0.0292, 0.15, 0, 1, 0, 0.15, 0.006]
func TestVectorizeLegitNullLastTx(t *testing.T) {
	req := &Request{
		ID: "tx-1329056812",
		Transaction: Transaction{
			Amount:       41.12,
			Installments: 2,
			RequestedAt:  "2026-03-11T18:45:53Z", // Wednesday UTC
		},
		Customer: Customer{
			AvgAmount:      82.24,
			TxCount24h:     3,
			KnownMerchants: []string{"MERC-003", "MERC-016"},
		},
		Merchant: Merchant{ID: "MERC-016", MCC: "5411", AvgAmount: 60.25},
		Terminal: Terminal{IsOnline: false, CardPresent: true, KmFromHome: 29.2331036248},
		LastTx:   nil,
	}

	v := Vectorize(req, testMCC(), testNorm())

	expected := [Dims]float32{
		0.004112, 0.166666, 0.05, 0.7826, 0.3333,
		-1, -1,
		0.0292, 0.15, 0, 1, 0, 0.15, 0.006025,
	}
	for i := 0; i < Dims; i++ {
		tol := float32(0.001)
		if i == 5 || i == 6 {
			if v[i] != -1 {
				t.Fatalf("dim %d: want sentinel -1, got %v", i, v[i])
			}
			continue
		}
		if !approxEq(v[i], expected[i], tol) {
			t.Fatalf("dim %d: want %v, got %v", i, expected[i], v[i])
		}
	}
}

// TestVectorizeFraudHighRisk mirrors the second worked example
// (fraudulent transaction).
//
// Expected: [0.9506, 0.8333, 1.0, 0.2174, 0.8333, -1, -1, 0.9523, 1.0, 0, 1, 1, 0.75, 0.0055]
func TestVectorizeFraudHighRisk(t *testing.T) {
	req := &Request{
		ID: "tx-3330991687",
		Transaction: Transaction{
			Amount:       9505.97,
			Installments: 10,
			RequestedAt:  "2026-03-14T05:15:12Z", // Saturday UTC
		},
		Customer: Customer{
			AvgAmount:      81.28,
			TxCount24h:     20,
			KnownMerchants: []string{"MERC-008", "MERC-007", "MERC-005"},
		},
		Merchant: Merchant{ID: "MERC-068", MCC: "7802", AvgAmount: 54.86},
		Terminal: Terminal{IsOnline: false, CardPresent: true, KmFromHome: 952.27},
		LastTx:   nil,
	}

	v := Vectorize(req, testMCC(), testNorm())

	expected := [Dims]float32{
		0.9506, 0.8333, 1.0, 0.2174, 0.8333,
		-1, -1,
		0.9523, 1.0, 0, 1, 1, 0.75, 0.005486,
	}
	for i := 0; i < Dims; i++ {
		if i == 5 || i == 6 {
			if v[i] != -1 {
				t.Fatalf("dim %d: want sentinel -1, got %v", i, v[i])
			}
			continue
		}
		if !approxEq(v[i], expected[i], 0.001) {
			t.Fatalf("dim %d: want %v, got %v", i, expected[i], v[i])
		}
	}
}

func TestVectorizeWithLastTx(t *testing.T) {
	req := &Request{
		Transaction: Transaction{
			Amount:       100,
			Installments: 1,
			RequestedAt:  "2026-03-11T20:00:00Z",
		},
		Customer: Customer{AvgAmount: 100, TxCount24h: 1, KnownMerchants: []string{"M1"}},
		Merchant: Merchant{ID: "M1", MCC: "9999", AvgAmount: 100},
		Terminal: Terminal{KmFromHome: 10},
		LastTx: &LastTx{
			Timestamp:     "2026-03-11T19:00:00Z", // 60 minutes earlier
			KmFromCurrent: 50,
		},
	}
	v := Vectorize(req, testMCC(), testNorm())

	// 60 / 1440 ≈ 0.0417
	if !approxEq(v[5], 60.0/1440.0, 0.001) {
		t.Fatalf("dim 5 (minutes_since_last_tx): want ~0.0417, got %v", v[5])
	}
	// 50 / 1000 = 0.05
	if !approxEq(v[6], 0.05, 0.001) {
		t.Fatalf("dim 6 (km_from_last_tx): want 0.05, got %v", v[6])
	}
	// unknown MCC -> default 0.5
	if !approxEq(v[12], DefaultMCCRisk, 0.0001) {
		t.Fatalf("dim 12: want default %v, got %v", DefaultMCCRisk, v[12])
	}
	// known merchant -> dim 11 = 0
	if v[11] != 0 {
		t.Fatalf("dim 11 (unknown_merchant): want 0 for known, got %v", v[11])
	}
}

func TestVectorizeUnknownMerchant(t *testing.T) {
	req := &Request{
		Transaction: Transaction{Amount: 1, Installments: 1, RequestedAt: "2026-01-01T00:00:00Z"},
		Customer:    Customer{AvgAmount: 1, KnownMerchants: []string{"M1", "M2"}},
		Merchant:    Merchant{ID: "M99", MCC: "5411"},
		Terminal:    Terminal{},
	}
	v := Vectorize(req, testMCC(), testNorm())
	if v[11] != 1 {
		t.Fatalf("dim 11 (unknown_merchant): want 1, got %v", v[11])
	}
}
