package harness

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// inputTokens are all the input tokens of a Usage, cached or not.
func (u Usage) inputTokens() int64 { return u.Input + u.CacheRead + u.CacheWrite }

// Totals adds up the Usage of several Steps. Costs stay in the unit they
// were reported in: nothing is converted and nothing made up.
type Totals struct {
	Input, Output, CacheRead, CacheWrite int64
	// Costs are the summed costs, by unit.
	Costs map[CostUnit]float64
	// NoCost is how many Steps reported no cost.
	NoCost int
}

// Add counts one Step's Usage.
func (t *Totals) Add(u Usage) {
	t.Input += u.Input
	t.Output += u.Output
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
	if u.CostUnit == NoCost {
		t.NoCost++
		return
	}
	if t.Costs == nil {
		t.Costs = map[CostUnit]float64{}
	}
	t.Costs[u.CostUnit] += u.Cost
}

// CostBasis is what a reported cost is: a charge, or an estimate of what
// the same tokens would cost at API prices.
type CostBasis string

const (
	// Billed is a cost the credential is charged.
	Billed CostBasis = "billed"
	// SubscriptionEstimate is the API-price figure of tokens a subscription
	// covers: no one is charged it.
	SubscriptionEstimate CostBasis = "subscription_estimate"
)

// CostString shows the costs per unit and how many Steps reported none,
// e.g. "$3.10 + 12 credits (2 Steps reported no cost)". A
// SubscriptionEstimate reads as one, e.g. "≈$0.28 API-price estimate
// (covered by your subscription)".
func (t Totals) CostString(basis CostBasis) string {
	if len(t.Costs) == 0 {
		return "no cost reported"
	}
	var notes []string
	costs := t.costs()
	if basis == SubscriptionEstimate {
		costs = "≈" + costs + " API-price estimate"
		notes = append(notes, "covered by your subscription")
	}
	switch t.NoCost {
	case 0:
	case 1:
		notes = append(notes, "1 Step reported no cost")
	default:
		notes = append(notes, fmt.Sprintf("%d Steps reported no cost", t.NoCost))
	}
	if len(notes) == 0 {
		return costs
	}
	return costs + " (" + strings.Join(notes, ", ") + ")"
}

// costs are the summed costs, dollars before credits.
func (t Totals) costs() string {
	units := make([]CostUnit, 0, len(t.Costs))
	for u := range t.Costs {
		units = append(units, u)
	}
	slices.Sort(units)
	slices.Reverse(units) // dollars before credits
	parts := make([]string, 0, len(units))
	for _, u := range units {
		parts = append(parts, formatCost(t.Costs[u], u))
	}
	return strings.Join(parts, " + ")
}

// formatCost shows a cost in its unit. Dollars below a cent keep four
// decimals, so that a small Step doesn't show as free.
func formatCost(v float64, unit CostUnit) string {
	if unit == USD {
		if v < 0.01 {
			return fmt.Sprintf("$%.4f", v)
		}
		return fmt.Sprintf("$%.2f", v)
	}
	n := strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
	if unit == Credits && n != "1" {
		return n + " credits"
	}
	if unit == Credits {
		return n + " credit"
	}
	return n + " " + string(unit)
}

// contextWindow is the input tokens of the last model call: how full the
// agent's context was at the end of the Step. A call that reported nothing,
// such as the synthetic one of a failed request, doesn't count.
func contextWindow(events []Event) int64 {
	for _, e := range slices.Backward(events) {
		if e.Kind == UsageReport && !e.Cumulative && e.Usage.inputTokens() > 0 {
			return e.Usage.inputTokens()
		}
	}
	return 0
}
