package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
)

func TestStaticDataLoadsOnceAndOnlyWhenFirstRead(t *testing.T) {
	loads := 0
	data := engine.NewStaticData(func(context.Context) (json.RawMessage, error) {
		loads++
		return json.RawMessage(`{"global":{"count":1},"node:Code":{"seen":true}}`), nil
	})
	if _, changed := data.Changed(); changed || loads != 0 {
		t.Fatalf("an unread handle loaded %d times or reports a change", loads)
	}
	global, err := data.Get(context.Background(), "global")
	if err != nil || string(global) != `{"count":1}` {
		t.Fatalf("Get(global) = %s, %v", global, err)
	}
	missing, err := data.Get(context.Background(), "node:Other")
	if err != nil || string(missing) != `{}` || loads != 1 {
		t.Fatalf("Get(node:Other) = %s, %v after %d loads, want {} from one load", missing, err, loads)
	}
}

func TestStaticDataChangesOnlyWhenAnEntryDoes(t *testing.T) {
	data := engine.NewStaticData(func(context.Context) (json.RawMessage, error) {
		return json.RawMessage(`{"global":{"count":1}}`), nil
	})
	ctx := context.Background()
	if err := data.Set(ctx, map[string]json.RawMessage{"global": json.RawMessage(`{"count":1}`), "node:Code": json.RawMessage(`{}`)}, 1024); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if _, changed := data.Changed(); changed {
		t.Fatal("writing back what was read counts as a change")
	}
	if err := data.Set(ctx, map[string]json.RawMessage{"global": json.RawMessage(`{"count":2}`)}, 1024); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	document, changed := data.Changed()
	if !changed || string(document) != `{"global":{"count":2}}` {
		t.Fatalf("Changed() = %s, %v", document, changed)
	}
}

func TestStaticDataRefusesToGrowPastItsLimit(t *testing.T) {
	data := engine.NewStaticData(nil)
	ctx := context.Background()
	err := data.Set(ctx, map[string]json.RawMessage{"global": json.RawMessage(`{"blob":"` + strings.Repeat("x", 64) + `"}`)}, 32)
	if !errors.Is(err, engine.ErrStaticDataTooLarge) {
		t.Fatalf("Set() error = %v, want it refused", err)
	}
	if _, changed := data.Changed(); changed {
		t.Fatal("a refused change was kept")
	}
}

func TestStaticDataThatCannotLoadSaysSo(t *testing.T) {
	data := engine.NewStaticData(func(context.Context) (json.RawMessage, error) { return nil, errors.New("database is down") })
	if _, err := data.Get(context.Background(), "global"); err == nil {
		t.Fatal("Get() succeeded without its data")
	}
}
