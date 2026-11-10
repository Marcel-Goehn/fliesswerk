package claudecode

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
)

// line is the part of a `stream-json` line Parse reads.
type line struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	// assistant
	Message struct {
		ID      string    `json:"id"`
		Model   string    `json:"model"`
		Content []content `json:"content"`
		Usage   *usage    `json:"usage"`
	} `json:"message"`
	// Error is set on the synthetic assistant message Claude Code writes
	// for an API failure, e.g. "authentication_failed".
	Error string `json:"error"`

	// result
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	Errors           []string        `json:"errors"`
	TotalCostUSD     *float64        `json:"total_cost_usd"`
	Usage            *usage          `json:"usage"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

type content struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type usage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

func (u usage) normalise() harness.Usage {
	return harness.Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
}

// Parse turns one `stream-json` line into common events. The `result` line
// is the terminal event: it carries the Step's cumulative usage and cost,
// then either the final report or the error.
func (ClaudeCode) Parse(raw []byte) []harness.Event {
	var l line
	if json.Unmarshal(raw, &l) != nil {
		return nil
	}
	switch l.Type {
	case "assistant":
		return parseAssistant(l)
	case "result":
		return parseResult(l)
	}
	return nil
}

func parseAssistant(l line) []harness.Event {
	if l.Error != "" {
		return []harness.Event{{Kind: harness.Error, Class: assistantErrorClass(l.Error), Text: joinText(l.Message.Content)}}
	}
	var events []harness.Event
	for _, c := range l.Message.Content {
		switch c.Type {
		case "text":
			events = append(events, harness.Event{Kind: harness.Text, Text: c.Text})
		case "tool_use":
			events = append(events, harness.Event{Kind: harness.ToolCall, Tool: c.Name, Summary: summarise(c.Input)})
		}
	}
	if l.Message.Usage != nil {
		events = append(events, harness.Event{Kind: harness.UsageReport, CallID: l.Message.ID, Usage: l.Message.Usage.normalise()})
	}
	return events
}

func parseResult(l line) []harness.Event {
	var events []harness.Event
	if l.Usage != nil {
		u := l.Usage.normalise()
		if l.TotalCostUSD != nil {
			u.Cost, u.CostUnit = *l.TotalCostUSD, harness.USD
		}
		events = append(events, harness.Event{Kind: harness.UsageReport, Cumulative: true, Usage: u})
	}
	switch {
	case l.Subtype == "error_max_structured_output_retries":
		// The agent never produced a valid report.
		events = append(events, harness.Event{Kind: harness.Final, Terminal: true})
	case l.IsError || strings.HasPrefix(l.Subtype, "error"):
		msg := l.Result
		if msg == "" {
			msg = strings.Join(l.Errors, "; ")
		}
		if msg == "" {
			msg = l.Subtype
		}
		events = append(events, harness.Event{Kind: harness.Error, Class: resultErrorClass(msg), Text: msg, Terminal: true})
	default:
		final := harness.Event{Kind: harness.Final, Terminal: true}
		if len(l.StructuredOutput) > 0 {
			if r, err := harness.ParseReport(l.StructuredOutput); err == nil {
				final.Report = &r
			}
		}
		events = append(events, final)
	}
	return events
}

// assistantErrorClass classifies the error Claude Code sets on a synthetic
// assistant message. A billing error stops the Run like an auth one: a
// person has to fix the account before any Step can succeed.
func assistantErrorClass(e string) harness.ErrorClass {
	switch e {
	case "authentication_failed", "billing_error":
		return harness.Auth
	case "rate_limit", "server_error":
		return harness.Transient
	}
	return harness.Other
}

// Markers in a failed result's text, checked in order: auth first.
var (
	authMarkers = []string{
		"authentication_error", "API Error: 401", "API Error: 403", "permission_error",
		"Invalid API key", "invalid x-api-key", "OAuth token", "Please run /login",
		"Credit balance is too low",
	}
	transientMarkers = []string{
		"API Error: 429", "API Error: 5", "rate_limit", "overloaded", "hit your limit",
		"usage limit", "Connection error", "ECONNRESET", "ECONNREFUSED", "ETIMEDOUT",
		"fetch failed", "socket hang up", "Request timed out",
	}
)

// resultErrorClass classifies a failed result by its text, for failures
// that come without a synthetic assistant message.
func resultErrorClass(msg string) harness.ErrorClass {
	for _, m := range authMarkers {
		if strings.Contains(msg, m) {
			return harness.Auth
		}
	}
	for _, m := range transientMarkers {
		if strings.Contains(msg, m) {
			return harness.Transient
		}
	}
	return harness.Other
}

func joinText(cs []content) string {
	var parts []string
	for _, c := range cs {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// summaryKeys are the tool input keys a summary is taken from, in order:
// the first one present with a string value wins.
var summaryKeys = []string{"file_path", "notebook_path", "command", "pattern", "url", "query", "path", "description", "skill"}

const maxSummary = 100

// summarise turns a tool's input into one line of at most maxSummary
// runes.
func summarise(input json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	for _, k := range summaryKeys {
		s, ok := fields[k].(string)
		if !ok || s == "" {
			continue
		}
		s = strings.Join(strings.Fields(s), " ")
		if utf8.RuneCountInString(s) > maxSummary {
			s = string([]rune(s)[:maxSummary-1]) + "…"
		}
		return s
	}
	return ""
}
