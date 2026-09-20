package sdk_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

func echo(items []sdk.Item) ([]sdk.Item, error) { return items, nil }

func decodeEnvelope(t *testing.T, output []byte) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(output, &envelope); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, output)
	}
	return envelope
}

func TestHandlePassesItemsThrough(t *testing.T) {
	output, exit := sdk.Handle([]byte(`[{"json":{"name":"ada"}}]`), echo)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0\n%s", exit, output)
	}
	envelope := decodeEnvelope(t, output)
	items, ok := envelope["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("envelope = %v, want one item", envelope)
	}
	name := items[0].(map[string]any)["json"].(map[string]any)["name"]
	if name != "ada" {
		t.Errorf("name = %v, want ada", name)
	}
}

func TestHandleNormalisesNilToEmptyArray(t *testing.T) {
	output, exit := sdk.Handle([]byte(`[]`), func([]sdk.Item) ([]sdk.Item, error) {
		return nil, nil
	})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0\n%s", exit, output)
	}
	if !strings.Contains(string(output), `"items":[]`) {
		t.Errorf("output = %s, want an empty items array rather than null", output)
	}
}

func TestHandleReportsRunFailureAsErrorEnvelope(t *testing.T) {
	output, exit := sdk.Handle([]byte(`[]`), func([]sdk.Item) ([]sdk.Item, error) {
		return nil, errors.New("upstream refused the key")
	})
	if exit == 0 {
		t.Fatalf("exit = 0, want non-zero\n%s", output)
	}
	envelope := decodeEnvelope(t, output)
	if envelope["error"] != "upstream refused the key" {
		t.Errorf("envelope = %v, want the run message", envelope)
	}
}

func TestHandleReportsBadInputAsErrorEnvelope(t *testing.T) {
	output, exit := sdk.Handle([]byte(`not json`), echo)
	if exit == 0 {
		t.Fatalf("exit = 0, want non-zero\n%s", output)
	}
	envelope := decodeEnvelope(t, output)
	message, _ := envelope["error"].(string)
	if !strings.HasPrefix(message, "input could not be decoded: ") {
		t.Errorf("envelope = %v, want an input decoding diagnostic", envelope)
	}
}

func TestVersionIsPinned(t *testing.T) {
	if sdk.Version == "" {
		t.Error("Version must be set: the host keys cached artifacts on it")
	}
}
