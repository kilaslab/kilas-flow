package tenantsettings

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type limitsSection struct {
	RunPerMinute *int   `json:"runPerMinute,omitempty"`
	Label        string `json:"label,omitempty"`
	Big          int64  `json:"big,omitempty"`
}

func validateLimits(value limitsSection) error {
	if value.RunPerMinute != nil && *value.RunPerMinute < 1 {
		return errors.New("runPerMinute must be at least 1")
	}
	return nil
}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry()
	if err := Register(registry, "limits", validateLimits); err != nil {
		t.Fatalf("Register(limits) error = %v", err)
	}
	return registry
}

func patchOf(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var patch map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &patch); err != nil {
		t.Fatalf("patch %s: %v", raw, err)
	}
	return patch
}

func applyOK(t *testing.T, registry *Registry, stored, patch string) string {
	t.Helper()
	var current []byte
	if stored != "" {
		current = []byte(stored)
	}
	out, err := registry.Apply(current, patchOf(t, patch))
	if err != nil {
		t.Fatalf("Apply(%s, %s) error = %v", stored, patch, err)
	}
	return string(out)
}

func patchIssues(t *testing.T, err error) []Issue {
	t.Helper()
	var patchErr *PatchError
	if !errors.As(err, &patchErr) {
		t.Fatalf("error = %v, want a *PatchError", err)
	}
	return patchErr.Issues
}

func TestRegisterRefusesABadNameADuplicateAndANonStruct(t *testing.T) {
	registry := testRegistry(t)
	if err := Register(registry, "limits", validateLimits); err == nil {
		t.Error("a duplicate section registered")
	}
	for _, name := range []string{"", "Limits", "rate-limit", "1st", strings.Repeat("a", 65)} {
		if err := Register[limitsSection](registry, name, nil); err == nil {
			t.Errorf("section name %q registered", name)
		}
	}
	if err := Register[map[string]int](registry, "mapped", nil); err == nil {
		t.Error("a non-struct section registered")
	}
	type embedded struct{ limitsSection }
	if err := Register[embedded](registry, "embedded", nil); err == nil {
		t.Error("a section embedding a struct registered")
	}
	if got := registry.Sections(); len(got) != 1 || got[0] != "limits" {
		t.Errorf("Sections() = %v, want [limits]", got)
	}
}

func TestApplyMergesASectionFieldByField(t *testing.T) {
	registry := testRegistry(t)
	first := applyOK(t, registry, "", `{"limits":{"runPerMinute":5}}`)
	if first != `{"limits":{"runPerMinute":5}}` {
		t.Fatalf("first = %s", first)
	}
	second := applyOK(t, registry, first, `{"limits":{"label":"gold"}}`)
	if second != `{"limits":{"runPerMinute":5,"label":"gold"}}` {
		t.Fatalf("second = %s, want both fields kept", second)
	}
	third := applyOK(t, registry, second, `{"limits":{"label":null}}`)
	if third != `{"limits":{"runPerMinute":5}}` {
		t.Fatalf("third = %s, want the nulled field removed", third)
	}
	if again := applyOK(t, registry, third, `{"limits":{"runPerMinute":5}}`); again != third {
		t.Errorf("a repeated patch changed the document: %s", again)
	}
}

func TestApplyNullRemovesASectionAndAnEmptySectionIsDropped(t *testing.T) {
	registry := testRegistry(t)
	stored := applyOK(t, registry, "", `{"limits":{"runPerMinute":5}}`)
	if out := applyOK(t, registry, stored, `{"limits":null}`); out != "" {
		t.Errorf("after null section = %q, want an empty (NULL) document", out)
	}
	if out := applyOK(t, registry, stored, `{"limits":{"runPerMinute":null}}`); out != "" {
		t.Errorf("after nulling the last field = %q, want an empty (NULL) document", out)
	}
	if out := applyOK(t, registry, "", `{}`); out != "" {
		t.Errorf("an empty patch on nothing = %q", out)
	}
}

func TestApplyRefusesUnknownSectionsAndFieldsWithTheNamedCode(t *testing.T) {
	registry := testRegistry(t)
	stored := []byte(`{"limits":{"runPerMinute":5}}`)
	out, err := registry.Apply(stored, patchOf(t, `{"limitz":{"a":1},"limits":{"runPerMinite":3,"typo":null}}`))
	if out != nil {
		t.Errorf("a refused patch returned a document: %s", out)
	}
	issues := patchIssues(t, err)
	want := map[string]bool{"limitz": true, "limits.runPerMinite": true, "limits.typo": true}
	if len(issues) != len(want) {
		t.Fatalf("issues = %+v, want %d", issues, len(want))
	}
	for _, issue := range issues {
		if !want[issue.Path] || issue.Code != CodeUnknownKey {
			t.Errorf("issue = %+v, want an unknown_key on one of %v", issue, want)
		}
	}
	// An unknown section set to null is refused too when nothing is stored
	// under it: there is nothing to retire.
	if _, err := registry.Apply(stored, patchOf(t, `{"limitz":null}`)); err == nil {
		t.Error("null for a key that was never stored was accepted")
	}
}

func TestApplyRefusesAValueTheSectionRejects(t *testing.T) {
	registry := testRegistry(t)
	cases := map[string]string{
		`{"limits":{"runPerMinute":0}}`:      "limits",
		`{"limits":{"runPerMinute":"five"}}`: "limits.runPerMinute",
		`{"limits":[1]}`:                     "limits",
		`{"limits":7}`:                       "limits",
	}
	for patch, path := range cases {
		_, err := registry.Apply(nil, patchOf(t, patch))
		issues := patchIssues(t, err)
		if len(issues) != 1 || issues[0].Code != CodeInvalid || issues[0].Path != path {
			t.Errorf("Apply(%s) issues = %+v, want one invalid at %s", patch, issues, path)
		}
	}
}

func TestApplyLetsNullRetireASectionNoLongerRegistered(t *testing.T) {
	registry := testRegistry(t)
	stored := `{"limits":{"runPerMinute":5},"retired":{"x":1}}`
	if out := applyOK(t, registry, stored, `{"retired":null}`); out != `{"limits":{"runPerMinute":5}}` {
		t.Errorf("after retiring = %s", out)
	}
}

func TestApplyKeepsLargeIntegersExact(t *testing.T) {
	registry := testRegistry(t)
	out := applyOK(t, registry, "", `{"limits":{"big":9007199254740993}}`)
	if out != `{"limits":{"big":9007199254740993}}` {
		t.Errorf("out = %s, want the integer unchanged", out)
	}
	again := applyOK(t, registry, out, `{"limits":{"label":"x"}}`)
	if !strings.Contains(again, `9007199254740993`) {
		t.Errorf("re-merged = %s, want the integer unchanged", again)
	}
}

func TestDecodeSkipsASectionThatNoLongerValidates(t *testing.T) {
	registry := testRegistry(t)
	settings, err := registry.Decode([]byte(`{"limits":{"runPerMinute":0},"gone":{"a":1}}`), 4)
	if err == nil {
		t.Error("Decode() reported nothing for an invalid section")
	}
	if settings.Version() != 4 {
		t.Errorf("Version() = %d, want 4", settings.Version())
	}
	if _, ok := Lookup[limitsSection](settings, "limits"); ok {
		t.Error("an invalid section was served")
	}

	good, err := registry.Decode([]byte(`{"limits":{"runPerMinute":3}}`), 5)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	limits, ok := Lookup[limitsSection](good, "limits")
	if !ok || limits.RunPerMinute == nil || *limits.RunPerMinute != 3 {
		t.Errorf("Lookup() = %+v, %v", limits, ok)
	}
}
