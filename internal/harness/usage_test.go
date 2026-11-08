package harness_test

import (
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
)

func call(id string, input, cacheRead, cacheWrite int64) harness.Event {
	return harness.Event{Kind: harness.UsageReport, CallID: id, Usage: harness.Usage{Input: input, CacheRead: cacheRead, CacheWrite: cacheWrite, Output: 7}}
}

func TestContextWindowIsTheInputTokensOfTheLastModelCall(t *testing.T) {
	out := harness.Resolve([]harness.Event{
		call("a", 10, 100, 5),
		call("b", 20, 300, 1),
		{Kind: harness.UsageReport, Cumulative: true, Usage: harness.Usage{Input: 30, CacheRead: 400, CacheWrite: 6, Output: 14}},
		{Kind: harness.Final, Terminal: true},
	}, 0)

	if out.ContextWindow != 321 {
		t.Errorf("ContextWindow = %d, want 321 (the last call's input, cache read and cache write)", out.ContextWindow)
	}
}

func TestAFailedCallWithoutUsageDoesNotEmptyTheContextWindow(t *testing.T) {
	out := harness.Resolve([]harness.Event{
		call("a", 50, 0, 0),
		{Kind: harness.UsageReport, CallID: "synthetic"},
		{Kind: harness.Error, Class: harness.Other, Text: "boom", Terminal: true},
	}, 1)

	if out.ContextWindow != 50 {
		t.Errorf("ContextWindow = %d, want 50", out.ContextWindow)
	}
}

func TestCostsAreSummedPerUnitAndNeverConverted(t *testing.T) {
	cases := []struct {
		name   string
		usages []harness.Usage
		want   string
	}{
		{"none reported", []harness.Usage{{}, {}}, "no cost reported"},
		{"one unit", []harness.Usage{{Cost: 1.5, CostUnit: harness.USD}, {Cost: 1.6, CostUnit: harness.USD}}, "$3.10"},
		{"below a cent stays visible", []harness.Usage{{Cost: 0.002, CostUnit: harness.USD}}, "$0.0020"},
		{"mixed units and silent Steps", []harness.Usage{
			{Cost: 3.1, CostUnit: harness.USD}, {Cost: 12, CostUnit: harness.Credits}, {}, {},
		}, "$3.10 + 12 credits (2 Steps reported no cost)"},
		{"one silent Step", []harness.Usage{{Cost: 1, CostUnit: harness.Credits}, {}}, "1 credit (1 Step reported no cost)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var total harness.Totals
			for _, u := range c.usages {
				total.Add(u)
			}
			if got := total.CostString(); got != c.want {
				t.Errorf("CostString() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTotalsSumTokens(t *testing.T) {
	var total harness.Totals
	total.Add(harness.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4})
	total.Add(harness.Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40})

	if total.Input != 11 || total.Output != 22 || total.CacheRead != 33 || total.CacheWrite != 44 {
		t.Errorf("totals = %+v", total)
	}
}
