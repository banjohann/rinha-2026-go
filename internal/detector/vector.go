package detector

import (
	"time"
)

const (
	Dims      = 14
	SentinelF = float32(-1)
)

func clamp01(x float32) float32 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// weekdayMonZero maps Go's Sunday=0..Saturday=6 to the spec's Monday=0..Sunday=6.
func weekdayMonZero(t time.Time) int {
	w := int(t.Weekday())
	return (w + 6) % 7
}

// Vectorize transforms a request into a 14-dim float vector following
// the formulas in REGRAS_DE_DETECCAO.md. Indices 5 and 6 are -1 when
// LastTx is nil.
func Vectorize(req *Request, mcc MCCRisk, n Constants) [Dims]float32 {
	var v [Dims]float32

	v[0] = clamp01(req.Transaction.Amount / n.MaxAmount)
	v[1] = clamp01(float32(req.Transaction.Installments) / n.MaxInstallments)

	if req.Customer.AvgAmount > 0 {
		v[2] = clamp01((req.Transaction.Amount / req.Customer.AvgAmount) / n.AmountVsAvgRatio)
	} else {
		v[2] = 1
	}

	t, err := time.Parse(time.RFC3339, req.Transaction.RequestedAt)
	if err == nil {
		t = t.UTC()
		v[3] = float32(t.Hour()) / 23.0
		v[4] = float32(weekdayMonZero(t)) / 6.0
	}

	if req.LastTx != nil {
		lt, err := time.Parse(time.RFC3339, req.LastTx.Timestamp)
		if err == nil {
			minutes := t.Sub(lt.UTC()).Minutes()
			v[5] = clamp01(float32(minutes) / n.MaxMinutes)
		}
		v[6] = clamp01(req.LastTx.KmFromCurrent / n.MaxKm)
	} else {
		v[5] = SentinelF
		v[6] = SentinelF
	}

	v[7] = clamp01(req.Terminal.KmFromHome / n.MaxKm)
	v[8] = clamp01(float32(req.Customer.TxCount24h) / n.MaxTxCount24h)

	if req.Terminal.IsOnline {
		v[9] = 1
	}
	if req.Terminal.CardPresent {
		v[10] = 1
	}
	if !contains(req.Customer.KnownMerchants, req.Merchant.ID) {
		v[11] = 1
	}
	v[12] = mcc.Get(req.Merchant.MCC)
	v[13] = clamp01(req.Merchant.AvgAmount / n.MaxMerchantAvgAmount)

	return v
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}
