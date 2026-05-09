package detector

type Transaction struct {
	Amount       float32 `json:"amount"`
	Installments int     `json:"installments"`
	RequestedAt  string  `json:"requested_at"`
}

type Customer struct {
	AvgAmount      float32  `json:"avg_amount"`
	TxCount24h     int      `json:"tx_count_24h"`
	KnownMerchants []string `json:"known_merchants"`
}

type Merchant struct {
	ID        string  `json:"id"`
	MCC       string  `json:"mcc"`
	AvgAmount float32 `json:"avg_amount"`
}

type Terminal struct {
	IsOnline    bool    `json:"is_online"`
	CardPresent bool    `json:"card_present"`
	KmFromHome  float32 `json:"km_from_home"`
}

type LastTx struct {
	Timestamp     string  `json:"timestamp"`
	KmFromCurrent float32 `json:"km_from_current"`
}

type Request struct {
	ID          string      `json:"id"`
	Transaction Transaction `json:"transaction"`
	Customer    Customer    `json:"customer"`
	Merchant    Merchant    `json:"merchant"`
	Terminal    Terminal    `json:"terminal"`
	LastTx      *LastTx     `json:"last_transaction"`
}

type Response struct {
	Approved   bool    `json:"approved"`
	FraudScore float32 `json:"fraud_score"`
}
