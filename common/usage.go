package common

// Usage reports token consumption and estimated cost for a single RPC.
// InputTokens and CachedInputTokens are non-overlapping; the total prompt
// size is their sum. Local backends report token counts with zero cost.
type Usage struct {
	InputTokens             int
	CachedInputTokens       int
	OutputTokens            int
	EstimatedCostMicros     int64
	CurrencyCode            string
	InputPricePerMTok       float64
	OutputPricePerMTok      float64
	CachedInputPricePerMTok float64
}

// EstimatedCost returns the estimated cost as a decimal amount with its
// ISO currency code, e.g. (0.0042, "USD"). Amount is 0 when the server
// could not price the call.
func (u Usage) EstimatedCost() (amount float64, currency string) {
	return float64(u.EstimatedCostMicros) / 1e6, u.CurrencyCode
}
