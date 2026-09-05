package workflow

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// versionScale is how many decimal places a node type version keeps. n8n uses
// one or two ("4.2", "3.4"), and six leaves room without risking overflow: the
// largest version in play is WAHA's 202502, which scales to 2.02502e11 and sits
// comfortably inside an int64.
const versionScale = 1_000_000

// TypeVersion is the version of a node *type* — the thing that decides which
// parameter shape a node is configured against.
//
// It is not an integer, because n8n does not use integers: core nodes are on
// 4.2, 3.4 and 1.1, and WAHA uses YYYYMM values like 202502. It is not a float
// either, because a float is an unsafe map key — 4.2 and 4.2000000000000002 are
// different keys for the same version, and a registry lookup that fails one
// time in a million is miserable to find. So it is a fixed-point decimal held
// as a scaled integer: comparable, ordered, and a valid map key.
//
// The field is unexported deliberately. A bare named integer type would let
// `Version: 1` keep compiling while silently meaning 0.000001, and this type
// was introduced by widening every version site in the codebase at once — the
// compiler catching each one is the point.
type TypeVersion struct {
	scaled int64
}

// V is a whole-numbered version, which is what most node types have.
func V(major int) TypeVersion {
	return TypeVersion{scaled: int64(major) * versionScale}
}

// ParseTypeVersion reads a decimal version such as "1", "4.2" or "202502".
//
// The fractional part is a decimal fraction, not a second integer: "4.2" and
// "4.20" are the same version, and 4.2 is greater than 4.15. Reading it as two
// integers would order those two backwards.
func ParseTypeVersion(text string) (TypeVersion, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return TypeVersion{}, fmt.Errorf("a node type version cannot be empty")
	}
	if strings.HasPrefix(trimmed, "-") {
		return TypeVersion{}, fmt.Errorf("node type version %q cannot be negative", text)
	}
	whole, fraction, hasFraction := strings.Cut(trimmed, ".")
	major, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return TypeVersion{}, fmt.Errorf("node type version %q is not a decimal number", text)
	}
	scaled := major * versionScale
	if hasFraction {
		if fraction == "" {
			return TypeVersion{}, fmt.Errorf("node type version %q has no digits after the point", text)
		}
		digits := len(strconv.Itoa(versionScale)) - 1
		if len(fraction) > digits {
			return TypeVersion{}, fmt.Errorf("node type version %q keeps more than %d decimal places", text, digits)
		}
		padded := fraction + strings.Repeat("0", digits-len(fraction))
		minor, err := strconv.ParseInt(padded, 10, 64)
		if err != nil {
			return TypeVersion{}, fmt.Errorf("node type version %q is not a decimal number", text)
		}
		scaled += minor
	}
	return TypeVersion{scaled: scaled}, nil
}

// MustTypeVersion is ParseTypeVersion for a compile-time constant.
func MustTypeVersion(text string) TypeVersion {
	version, err := ParseTypeVersion(text)
	if err != nil {
		panic(err)
	}
	return version
}

// IsZero reports an unset version. A document that omits typeVersion gets one,
// which is how the registry knows to resolve a default rather than to look up
// version zero.
func (version TypeVersion) IsZero() bool { return version.scaled == 0 }

// Compare orders two versions: negative if lower, zero if equal, positive if
// higher.
func (version TypeVersion) Compare(other TypeVersion) int {
	switch {
	case version.scaled < other.scaled:
		return -1
	case version.scaled > other.scaled:
		return 1
	default:
		return 0
	}
}

// String renders the canonical decimal form, with no trailing zeros, which is
// what both the JSON wire format and every diagnostic use.
func (version TypeVersion) String() string {
	whole := version.scaled / versionScale
	fraction := version.scaled % versionScale
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	digits := len(strconv.Itoa(versionScale)) - 1
	text := strings.TrimRight(fmt.Sprintf("%0*d", digits, fraction), "0")
	return strconv.FormatInt(whole, 10) + "." + text
}

// MarshalJSON writes a JSON number, not a string.
//
// The wire format is unchanged by this type existing: a document still says
// "typeVersion": 4.2, which is what n8n writes and what every already-persisted
// KilasFlow document says. Emitting a string would have been an API break
// dressed up as a refactor.
func (version TypeVersion) MarshalJSON() ([]byte, error) {
	return []byte(version.String()), nil
}

// UnmarshalJSON reads a JSON number through its literal text rather than
// through a float, so 4.2 cannot arrive as 4.199999999999999.
func (version *TypeVersion) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" {
		*version = TypeVersion{}
		return nil
	}
	// Tolerate a quoted number: some hosts stringify numeric fields, and
	// refusing would reject a document that is otherwise perfectly valid.
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		var quoted string
		if err := json.Unmarshal(data, &quoted); err != nil {
			return fmt.Errorf("node type version is not readable: %w", err)
		}
		text = quoted
	}
	parsed, err := ParseTypeVersion(text)
	if err != nil {
		return err
	}
	*version = parsed
	return nil
}
