// Package test provides the data structures for testrun execution and analysis.
package test

import (
	"context"
	"encoding/json"
	"iter"
	"time"
)

// Runner can be assigned to an [api.TestingFramework] in order to enable the builtin
// /testrun/* endpoints.
type History interface {
	// Summary should return the latest status for each test.
	Summary(ctx context.Context) ([]Summary, error)

	// Inspect the history for a specific test run.
	Inspect(ctx context.Context, title string) (iter.Seq[Execution], error)

	// Capture an execution, so that it can be retrieved later via History.
	Capture(ctx context.Context, result Execution) error
}

// Execution record.
type Execution struct {
	Title string        `json:"title"`
	Story string        `json:"story,omitzero"`
	Speed time.Duration `json:"speed"`
	Trace []Event       `json:"trace"`
	Error string        `json:"error,omitzero"`
	Panic bool          `json:"panic,omitzero"`
}

// Event record.
type Event struct {
	Note string          `json:"note,omitzero"`
	Docs string          `json:"docs,omitzero"`
	Call string          `json:"call,omitzero"`
	Time time.Time       `json:"time,omitzero"`
	Args json.RawMessage `json:"args,omitzero"`
	Vals json.RawMessage `json:"vals,omitzero"`
}

type Summary struct {
	Name string `json:"name"`
	From string `json:"from"`
	Pass bool   `json:"pass"`
	Fail bool   `json:"fail"`
	Todo bool   `json:"todo"`
}
