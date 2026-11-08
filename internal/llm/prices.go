package llm

// Price is USD per million tokens.
type Price struct {
	Input, Output, CacheRead, CacheWrite float64
}

// Cost computes the USD cost of one call. input_tokens excludes cached tokens on the Messages API, so the
// four counts are disjoint.
func (p Price) Cost(u Usage) float64 {
	return (float64(u.InputTokens)*p.Input + float64(u.OutputTokens)*p.Output +
		float64(u.CacheReadTokens)*p.CacheRead + float64(u.CacheWriteTokens)*p.CacheWrite) / 1e6
}

// PriceSource documents where the table comes from (KICKOFF §6.7: keep a price table with source and date).
// Source: claude-api skill model table (cached 2026-09-25), checked 2026-11-08. Cache writes are the
// 5-minute TTL rate (1.25× input). Re-check before quoting costs externally.
const PriceSource = "claude-api skill model table (cached 2026-09-25), checked 2026-11-08; cache write = 5-min TTL (1.25x input)"

var prices = map[string]Price{
	"claude-sonnet-5-5": {Input: 2, Output: 10, CacheRead: 0.20, CacheWrite: 2.50},
	"claude-opus-5-5":   {Input: 4, Output: 20, CacheRead: 0.20, CacheWrite: 5},
	"claude-haiku-4-5":  {Input: 1, Output: 5, CacheRead: 0.10, CacheWrite: 1.25},
}

// PriceFor looks up a model's price. Unknown models (mock, aliases) have no price and cost 0.
func PriceFor(model string) (Price, bool) {
	p, ok := prices[model]
	return p, ok
}
