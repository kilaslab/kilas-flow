package nodes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// An escaped apostrophe is ordinary SQL and must reach a real server.
func TestALiveServerAcceptsAnEscapedApostrophe(t *testing.T) {
	for name, live := range liveV2 {
		t.Run(name, func(t *testing.T) {
			if live.credential != "mysql" {
				t.Skip("the backslash question is a MySQL sql_mode setting")
			}
			resolver := liveCredential(t, live.env, live.credential)
			executor := newV2Executor(t, live.credential)
			ir := v2Node(t, live.nodeType, live.credential, map[string]any{
				"operation": "executeQuery", "query": `SELECT 'O\'Brien' AS n`,
			})
			output, err := executor.Execute(context.Background(), ir,
				workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{Credentials: resolver})
			if err != nil {
				t.Fatalf("an escaped apostrophe was refused: %v", err)
			}
			if got := output[0][0].JSON["n"]; got != "O'Brien" {
				t.Errorf("n = %#v, want O'Brien", got)
			}
			// And two statements are still refused on the same connection.
			bad := v2Node(t, live.nodeType, live.credential, map[string]any{
				"operation": "executeQuery", "query": `SELECT 1; DROP TABLE zz_nonexistent`,
			})
			_, err = executor.Execute(context.Background(), bad,
				workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{Credentials: resolver})
			if err == nil || !strings.Contains(err.Error(), "refused") {
				t.Errorf("two statements were not refused: %v", err)
			}
		})
	}
}
