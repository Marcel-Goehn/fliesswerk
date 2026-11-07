package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ReportSchema is the JSON Schema of the report every Step ends with. The
// core owns it and hands it to the adapter, which passes it natively where
// the Harness supports that.
const ReportSchema = `{"type":"object","properties":{"outcome":{"type":"string","enum":["done","cannot_do"]},"reason":{"type":"string"},"summary":{"type":"string"}},"required":["outcome","summary"],"additionalProperties":false}`

// Outcome values of a Report.
const (
	Done     = "done"
	CannotDo = "cannot_do"
)

// Report is what the agent reports at the end of a Step.
type Report struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
	Summary string `json:"summary"`
}

// ParseReport decodes a report and checks it against ReportSchema.
func ParseReport(data []byte) (Report, error) {
	var r Report
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Report{}, fmt.Errorf("report: %w", err)
	}
	if r.Outcome != Done && r.Outcome != CannotDo {
		return Report{}, fmt.Errorf("report: outcome %q is neither %q nor %q", r.Outcome, Done, CannotDo)
	}
	if r.Summary == "" {
		return Report{}, errors.New("report: no summary")
	}
	return r, nil
}
