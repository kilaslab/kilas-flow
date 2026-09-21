package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/datastore"
)

// The Increment Row Action (FEAT-1axhdn).
//
// Increment is not one of n8n's Data Table operations, and it exists here
// because the alternative is a lost update: the popular use of a data table
// is a counter or a flag, and reading it into the item, changing it and
// writing it back races with every other worker doing the same. The write
// itself is one UPDATE ... RETURNING in the row store, atomic per row on
// both drivers; this file is only the node's half of it — reading the
// amount a user typed, and refusing the parameter shapes the store cannot
// honour before any SQL exists.
//
// The interface is asserted rather than declared on DatastoreStore: the
// increment is a capability of the real engine, and adding it to that
// interface would break every stand-in that keeps the node testable.

// DatastoreIncrementer is the atomic increment the node needs beyond the
// DatastoreStore slice. The real store is the *datastore.Engine itself, so
// the assertion in runRow holds for every binding the server assembles.
type DatastoreIncrementer interface {
	Increment(ctx context.Context, tenantID, dsID string, filter *datastore.Filter, column string, delta float64) (*datastore.UpdateResult, error)
}

// datastoreIncrementAmount reads the amount an operator configured. The
// parameter is a number property, but a document that reached this executor
// without passing validation — an import, a repository write, a workflow
// written by hand — can carry any JSON type, so the value is coerced
// tolerantly and refused only when it is genuinely not a number. An absent
// or blank amount is one, which is the n8n-shaped default the property
// declares.
func datastoreIncrementAmount(parameters map[string]any) (float64, error) {
	raw, present := parameters["amount"]
	if !present || raw == nil {
		return 1, nil
	}
	var amount float64
	switch typed := raw.(type) {
	case float64:
		amount = typed
	case float32:
		amount = float64(typed)
	case int:
		amount = float64(typed)
	case int32:
		amount = float64(typed)
	case int64:
		amount = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("increment amount %q is not a number", typed.String())
		}
		amount = parsed
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 1, nil
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, fmt.Errorf("increment amount %q is not a number", typed)
		}
		amount = parsed
	default:
		return 0, fmt.Errorf("increment amount has type %T, want a number", raw)
	}
	// NaN and the infinities cannot travel through JSON and cannot be
	// stored either: they would poison the column rather than be refused.
	if math.IsNaN(amount) || math.IsInf(amount, 0) {
		return 0, fmt.Errorf("increment amount %v is not a finite number", amount)
	}
	return amount, nil
}
