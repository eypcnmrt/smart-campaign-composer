package llm

// Prices are the only place where this code knows what a token costs. They are used to log the spend of a call and to
// stop `make eval` at its budget; nothing else depends on them.
//
// Source: the Claude API pricing page (https://platform.claude.com/docs/en/about-claude/pricing), US dollars per million
// tokens, first-party API, standard speed, checked on 2026-11-15. Prompt caching is not used by this application, so there
// is no cache price here. Update this table, and its date, when a price or a default model changes.
var prices = map[string]Price{
	"claude-opus-5-5":   {InputPerMTok: 4, OutputPerMTok: 20},
	"claude-sonnet-5-5": {InputPerMTok: 2, OutputPerMTok: 10},
}

// Price is the cost of one model in US dollars per million tokens.
type Price struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// Of returns the cost of the given token counts.
func (p Price) Of(inputTokens, outputTokens int64) float64 {
	return (float64(inputTokens)*p.InputPerMTok + float64(outputTokens)*p.OutputPerMTok) / 1e6
}

// PriceOf returns the price of a model and whether the table knows it.
func PriceOf(model string) (Price, bool) {
	p, ok := prices[model]
	return p, ok
}

// priciest is the most expensive price in the table. A model the table does not know is charged at it: a budget guard
// that guessed low would let a run spend more than it was told to.
func priciest() Price {
	var top Price
	for _, p := range prices {
		if p.OutputPerMTok > top.OutputPerMTok || (p.OutputPerMTok == top.OutputPerMTok && p.InputPerMTok > top.InputPerMTok) {
			top = p
		}
	}
	return top
}

// PriceOrPriciest returns the price of a model, or the dearest price in the table for a model it does not know.
func PriceOrPriciest(model string) Price {
	if p, ok := PriceOf(model); ok {
		return p
	}
	return priciest()
}

// CostOf returns the cost in US dollars of one answer. The mock models, which report no tokens, cost nothing. When a
// fallback model wrote part of the answer, the usage is not split between the two, so the dearer of the two prices is used.
func CostOf(resp Response) float64 {
	if resp.InputTokens == 0 && resp.OutputTokens == 0 {
		return 0
	}
	p := PriceOrPriciest(resp.Model)
	if resp.FallbackUsed && resp.ServedBy != resp.Model {
		if q := PriceOrPriciest(resp.ServedBy); q.OutputPerMTok > p.OutputPerMTok {
			p = q
		}
	}
	return p.Of(resp.InputTokens, resp.OutputTokens)
}
