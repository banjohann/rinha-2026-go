package detector

import (
	"encoding/json"
	"os"
)

type Constants struct {
	MaxAmount             float32 `json:"max_amount"`
	MaxInstallments       float32 `json:"max_installments"`
	AmountVsAvgRatio      float32 `json:"amount_vs_avg_ratio"`
	MaxMinutes            float32 `json:"max_minutes"`
	MaxKm                 float32 `json:"max_km"`
	MaxTxCount24h         float32 `json:"max_tx_count_24h"`
	MaxMerchantAvgAmount  float32 `json:"max_merchant_avg_amount"`
}

func LoadConstants(path string) (Constants, error) {
	var c Constants
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}
