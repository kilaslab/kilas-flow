package property_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/property"
)

// customers is the schema every test below maps onto.
var customers = []property.MapperField{
	{ID: "id", DisplayName: "id", Type: "number", CanBeUsedToMatch: true, DefaultMatch: true, ReadOnly: true},
	{ID: "email", DisplayName: "email", Type: "string", Required: true, CanBeUsedToMatch: true},
	{ID: "tier", DisplayName: "tier", Type: "string"},
	{ID: "createdAt", DisplayName: "createdAt", Type: "dateTime", ReadOnly: true},
}

func TestAutomaticMappingOmitsAColumnAnItemDoesNotCarry(t *testing.T) {
	t.Parallel()

	// The trap. Building the write from the schema would turn a column the item
	// does not carry into an explicit null, and on an update that silently
	// blanks a column the user never touched — no error, no diagnostic, and the
	// damage visible only in the customer's data.
	mapping := property.Mapping{Mode: property.MappingAuto}

	full, dropped := property.MappedColumns(customers, mapping, map[string]any{
		"email": "ada@example.test", "tier": "gold",
	})
	if !reflect.DeepEqual(full, map[string]any{"email": "ada@example.test", "tier": "gold"}) {
		t.Errorf("columns = %#v, want both fields", full)
	}
	if dropped != nil {
		t.Errorf("dropped = %#v, want nothing dropped", dropped)
	}

	partial, _ := property.MappedColumns(customers, mapping, map[string]any{"email": "grace@example.test"})
	if _, present := partial["tier"]; present {
		t.Errorf("columns = %#v, want the absent column omitted rather than sent as null", partial)
	}
	if len(partial) != 1 {
		t.Errorf("columns = %#v, want only what the item carried", partial)
	}

	// A key with no column is reported rather than silently absent: unmapped is
	// something the author needs to see.
	_, unmapped := property.MappedColumns(customers, mapping, map[string]any{
		"email": "ada@example.test", "nickname": "Ada", "region": "SEA",
	})
	if !reflect.DeepEqual(unmapped, []string{"nickname", "region"}) {
		t.Errorf("dropped = %#v, want both unmapped keys named", unmapped)
	}

	// A column the database fills in is never written, whichever mode.
	written, _ := property.MappedColumns(customers, mapping, map[string]any{
		"id": float64(1), "createdAt": "2026-09-05", "email": "ada@example.test",
	})
	if _, present := written["id"]; present {
		t.Errorf("columns = %#v, want the read-only column left alone", written)
	}
	if _, present := written["createdAt"]; present {
		t.Errorf("columns = %#v, want the read-only column left alone", written)
	}
}

func TestManualMappingWritesOnlyDeclaredWritableColumns(t *testing.T) {
	t.Parallel()

	values, dropped := property.MappedColumns(customers, property.Mapping{
		Mode: property.MappingManual,
		Values: map[string]any{
			"email": "ada@example.test", "tier": "gold",
			"id": float64(9), "unknown": "x",
		},
	}, map[string]any{"ignored": true})

	if !reflect.DeepEqual(values, map[string]any{"email": "ada@example.test", "tier": "gold"}) {
		t.Errorf("columns = %#v, want the writable declared ones only", values)
	}
	if dropped != nil {
		t.Errorf("dropped = %#v, want nothing reported for a manual mapping", dropped)
	}
}

func TestAMappingIsCheckedAgainstTheSchemaThatIsLiveNow(t *testing.T) {
	t.Parallel()

	update := property.ResourceMapperDeclaration{MatchingColumnsRequired: true}
	insert := property.ResourceMapperDeclaration{}

	for name, testCase := range map[string]struct {
		declaration property.ResourceMapperDeclaration
		mapping     property.Mapping
		wantIn      string
	}{
		"an update with nothing to match on": {
			declaration: update,
			mapping:     property.Mapping{Mode: property.MappingManual, Values: map[string]any{"email": "a"}},
			wantIn:      "at least one column to match on",
		},
		"a matching column that is not in the table": {
			declaration: update,
			mapping:     property.Mapping{Mode: property.MappingManual, MatchingColumns: []string{"nickname"}},
			wantIn:      "not in this table",
		},
		"a matching column that cannot identify a row": {
			declaration: update,
			mapping:     property.Mapping{Mode: property.MappingManual, MatchingColumns: []string{"tier"}},
			wantIn:      "cannot be used to match",
		},
		"a required column left unset": {
			declaration: insert,
			mapping:     property.Mapping{Mode: property.MappingManual, Values: map[string]any{"tier": "gold"}},
			wantIn:      "email",
		},
		"a required column set to nothing": {
			declaration: insert,
			mapping:     property.Mapping{Mode: property.MappingManual, Values: map[string]any{"email": ""}},
			wantIn:      "email",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := property.ValidateMapping(testCase.declaration, customers, testCase.mapping, "update")
			if err == nil {
				t.Fatalf("ValidateMapping() accepted %#v", testCase.mapping)
			}
			if !strings.Contains(err.Error(), testCase.wantIn) {
				t.Errorf("error = %v, want it to mention %q", err, testCase.wantIn)
			}
		})
	}

	// More than one matching column is allowed: a composite key is a key.
	if err := property.ValidateMapping(update, customers, property.Mapping{
		Mode: property.MappingManual, MatchingColumns: []string{"id", "email"},
		Values: map[string]any{"tier": "gold"},
	}, "update"); err != nil {
		t.Errorf("ValidateMapping() with two matching columns = %v, want accepted", err)
	}

	// A required column used as the match identifies the row rather than
	// supplying it, so it satisfies its own required-ness.
	if err := property.ValidateMapping(update, customers, property.Mapping{
		Mode: property.MappingManual, MatchingColumns: []string{"email"},
		Values: map[string]any{"tier": "gold"},
	}, "update"); err != nil {
		t.Errorf("ValidateMapping() with the required column as the match = %v, want accepted", err)
	}

	// Automatic mapping cannot know per item at edit time, so required-ness is
	// checked as the write is built rather than here.
	if err := property.ValidateMapping(insert, customers, property.Mapping{Mode: property.MappingAuto}, "insert"); err != nil {
		t.Errorf("ValidateMapping() on an automatic mapping = %v, want accepted", err)
	}
}

func TestAMappingRoundTripsThroughItsStoredShape(t *testing.T) {
	t.Parallel()

	mapping := property.Mapping{
		Mode:            property.MappingManual,
		Values:          map[string]any{"email": "ada@example.test", "tier": "gold"},
		MatchingColumns: []string{"id"},
		Schema:          customers,
	}
	stored := property.WriteMapping(mapping)
	// n8n's own key names, so an imported mapper reads back and an export is
	// not lossy.
	for _, key := range []string{"mappingMode", "value", "matchingColumns", "schema"} {
		if _, present := stored[key]; !present {
			t.Errorf("the stored shape is missing %q: %#v", key, stored)
		}
	}
	read, ok := property.ReadMapping(stored)
	if !ok {
		t.Fatalf("ReadMapping(%#v) refused its own output", stored)
	}
	if read.Mode != mapping.Mode || !reflect.DeepEqual(read.Values, mapping.Values) {
		t.Errorf("round trip = %#v, want %#v", read, mapping)
	}
	if !reflect.DeepEqual(read.MatchingColumns, mapping.MatchingColumns) {
		t.Errorf("matching columns = %#v, want %#v", read.MatchingColumns, mapping.MatchingColumns)
	}
	if len(read.Schema) != len(customers) || read.Schema[0].ID != "id" || !read.Schema[0].ReadOnly {
		t.Errorf("schema copy = %#v, want it carried", read.Schema)
	}

	// Not a mapping at all reads as one, so a validator that trusted the cast
	// would run against an empty mapping instead of refusing.
	for _, value := range []any{nil, "defineBelow", map[string]any{"value": map[string]any{}}} {
		if _, ok := property.ReadMapping(value); ok {
			t.Errorf("ReadMapping(%#v) accepted something that is not a mapping", value)
		}
	}
}
