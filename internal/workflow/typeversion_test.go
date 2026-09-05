package workflow_test

import (
	"encoding/json"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

func TestTypeVersionRoundTripsEveryFormInPlay(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"1", "2", "1.1", "3.4", "4.2", "202409", "202502"} {
		version, err := workflow.ParseTypeVersion(text)
		if err != nil {
			t.Fatalf("ParseTypeVersion(%q) error = %v", text, err)
		}
		if got := version.String(); got != text {
			t.Errorf("ParseTypeVersion(%q).String() = %q, want %q", text, got, text)
		}
		encoded, err := json.Marshal(version)
		if err != nil {
			t.Fatalf("Marshal(%q) error = %v", text, err)
		}
		// The wire format is a JSON number, unchanged by this type existing.
		if string(encoded) != text {
			t.Errorf("Marshal(%q) = %s, want the bare number %s", text, encoded, text)
		}
		var decoded workflow.TypeVersion
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", encoded, err)
		}
		if decoded.Compare(version) != 0 {
			t.Errorf("round trip of %q gave %q", text, decoded)
		}
	}
}

// TestTypeVersionTreatsTheFractionAsADecimal is the property a two-integer
// representation gets wrong: 4.2 is greater than 4.15, and "4.20" is the same
// version as "4.2".
func TestTypeVersionTreatsTheFractionAsADecimal(t *testing.T) {
	t.Parallel()

	if workflow.MustTypeVersion("4.2").Compare(workflow.MustTypeVersion("4.15")) <= 0 {
		t.Error("4.2 must order above 4.15")
	}
	if workflow.MustTypeVersion("4.20").Compare(workflow.MustTypeVersion("4.2")) != 0 {
		t.Error("4.20 and 4.2 are the same version")
	}
	if got := workflow.MustTypeVersion("4.20").String(); got != "4.2" {
		t.Errorf("MustTypeVersion(\"4.20\").String() = %q, want \"4.2\"", got)
	}
	if workflow.MustTypeVersion("3").Compare(workflow.MustTypeVersion("3.1")) >= 0 {
		t.Error("3 must order below 3.1")
	}
	if workflow.MustTypeVersion("202502").Compare(workflow.MustTypeVersion("202409")) <= 0 {
		t.Error("202502 must order above 202409")
	}
}

// TestTypeVersionIsASafeMapKey is why this is not a float. Two versions parsed
// from the same decimal must be the same key, every time.
func TestTypeVersionIsASafeMapKey(t *testing.T) {
	t.Parallel()

	seen := map[workflow.TypeVersion]int{}
	for _, text := range []string{"4.2", "4.20", "4.2"} {
		seen[workflow.MustTypeVersion(text)]++
	}
	if len(seen) != 1 {
		t.Fatalf("map keys = %d, want 1 — the same decimal must be the same key", len(seen))
	}
	if seen[workflow.MustTypeVersion("4.2")] != 3 {
		t.Errorf("count = %d, want 3", seen[workflow.MustTypeVersion("4.2")])
	}
	if workflow.V(1) != workflow.MustTypeVersion("1") {
		t.Error("V(1) and MustTypeVersion(\"1\") must be the same key")
	}
}

func TestTypeVersionRejectsWhatIsNotAVersion(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"", "  ", "abc", "-1", "1.", "1.2.3", "1.2345678", "1e3"} {
		if _, err := workflow.ParseTypeVersion(text); err == nil {
			t.Errorf("ParseTypeVersion(%q) accepted an invalid version", text)
		}
	}
}

// TestTypeVersionZeroMeansUnset separates "no version was given" from "version
// zero", which is what lets the registry resolve a default.
func TestTypeVersionZeroMeansUnset(t *testing.T) {
	t.Parallel()

	var unset workflow.TypeVersion
	if !unset.IsZero() {
		t.Error("the zero value must report IsZero")
	}
	if workflow.V(1).IsZero() {
		t.Error("V(1) is not unset")
	}
	var decoded workflow.TypeVersion
	if err := json.Unmarshal([]byte("null"), &decoded); err != nil {
		t.Fatalf("Unmarshal(null) error = %v", err)
	}
	if !decoded.IsZero() {
		t.Error("null must decode to the unset version")
	}
}

// TestTypeVersionReadsAQuotedNumber tolerates a host that stringifies numeric
// fields, because refusing would reject a document that is otherwise valid.
func TestTypeVersionReadsAQuotedNumber(t *testing.T) {
	t.Parallel()

	var decoded workflow.TypeVersion
	if err := json.Unmarshal([]byte(`"4.2"`), &decoded); err != nil {
		t.Fatalf("Unmarshal(\"4.2\") error = %v", err)
	}
	if decoded.Compare(workflow.MustTypeVersion("4.2")) != 0 {
		t.Errorf("decoded = %q, want 4.2", decoded)
	}
}
