package jsrun_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Lineage stays in the server: its size is the upstream node IDs', which the
// workflow's author chooses, and nothing caps them. What does cross counts
// against the input cap.
func TestAJobCarriesNoLineageAndCountsAllItCarries(t *testing.T) {
	origin := &workflow.PairedItem{SourceNodeID: strings.Repeat("n", 10_000)}
	items := make([]workflow.Item, 2_000)
	for index := range items {
		items[index] = workflow.Item{JSON: map[string]any{}, Paired: origin}
	}
	job, _, err := newRunner().Prepare(jsrun.Task{Source: "return items", Items: items})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	header, _ := json.Marshal(job)
	if len(header) > 1<<10 || strings.Contains(string(header), "nnnn") {
		t.Fatalf("the job's header is %d bytes, want the lineage kept out of it", len(header))
	}

	roots := jsrun.Roots{Env: map[string]string{"BIG": strings.Repeat("x", 2<<20)}}
	_, _, err = jsrun.NewRunner(jsrun.Options{Limits: jsrun.Limits{MaxInputBytes: 1 << 20}}).Prepare(jsrun.Task{Source: "return items", Roots: roots})
	if !errors.Is(err, jsrun.ErrInputLimit) {
		t.Fatalf("Prepare() error = %v, want the roots counted against the input cap", err)
	}
}

// Only the server's own context cancels a run, so no failure a worker
// describes decodes to a cancellation.
func TestAWorkerCannotReportACancellation(t *testing.T) {
	for _, kind := range []string{"canceled", "deadline"} {
		err := (&jsrun.WireError{Kind: kind, Text: "context canceled"}).Decode()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("kind %q decodes to %v, a cancellation", kind, err)
		}
	}
}

// A thrown message of any size is cut to what a failure carries.
func TestAHugeThrownMessageIsCut(t *testing.T) {
	_, err := newRunner().Run(context.Background(), jsrun.Task{Source: "throw new Error('x'.repeat(1 << 20))"})
	var script *jsrun.ScriptError
	if !errors.As(err, &script) || len(script.Message) > 5<<10 || !strings.HasSuffix(script.Message, "…") {
		t.Fatalf("Run() error of %d bytes, want it cut", len(err.Error()))
	}
}
