package conditions_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/conditions"
	"github.com/kilaslab/kilas-flow/internal/expression"
)

func op(valueType conditions.ValueType, operation string) conditions.Operator {
	return conditions.Operator{Type: valueType, Operation: operation}
}

// The operator table, one row per case, because the difference between "5"
// being 5 and "5" being nothing is a branch a workflow takes — and inferring
// these from the implementation would test that the implementation agrees with
// itself.
func TestOperatorSemantics(t *testing.T) {
	t.Parallel()

	sensitive := conditions.Options{CaseSensitive: true}
	for name, testCase := range map[string]struct {
		condition conditions.Condition
		options   conditions.Options
		want      bool
	}{
		// string
		"string equals":                           {conditions.Condition{LeftValue: "abc", Operator: op(conditions.TypeString, "equals"), RightValue: "abc"}, sensitive, true},
		"string equals is cased":                  {conditions.Condition{LeftValue: "ABC", Operator: op(conditions.TypeString, "equals"), RightValue: "abc"}, sensitive, false},
		"string equals ignores case when told to": {conditions.Condition{LeftValue: "ABC", Operator: op(conditions.TypeString, "equals"), RightValue: "abc"}, conditions.Options{}, true},
		"string contains":                         {conditions.Condition{LeftValue: "hello world", Operator: op(conditions.TypeString, "contains"), RightValue: "lo w"}, sensitive, true},
		"string notContains":                      {conditions.Condition{LeftValue: "hello", Operator: op(conditions.TypeString, "notContains"), RightValue: "z"}, sensitive, true},
		"string startsWith":                       {conditions.Condition{LeftValue: "hello", Operator: op(conditions.TypeString, "startsWith"), RightValue: "he"}, sensitive, true},
		"string notStartsWith":                    {conditions.Condition{LeftValue: "hello", Operator: op(conditions.TypeString, "notStartsWith"), RightValue: "lo"}, sensitive, true},
		"string endsWith":                         {conditions.Condition{LeftValue: "hello", Operator: op(conditions.TypeString, "endsWith"), RightValue: "lo"}, sensitive, true},
		"string notEndsWith":                      {conditions.Condition{LeftValue: "hello", Operator: op(conditions.TypeString, "notEndsWith"), RightValue: "he"}, sensitive, true},
		"string empty":                            {conditions.Condition{LeftValue: "", Operator: op(conditions.TypeString, "empty")}, sensitive, true},
		"string notEmpty":                         {conditions.Condition{LeftValue: "x", Operator: op(conditions.TypeString, "notEmpty")}, sensitive, true},
		"absent string is empty":                  {conditions.Condition{LeftValue: nil, Operator: op(conditions.TypeString, "empty")}, sensitive, true},

		// number
		"number equals":                  {conditions.Condition{LeftValue: float64(5), Operator: op(conditions.TypeNumber, "equals"), RightValue: float64(5)}, sensitive, true},
		"number gt":                      {conditions.Condition{LeftValue: float64(6), Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(5)}, sensitive, true},
		"number lt":                      {conditions.Condition{LeftValue: float64(4), Operator: op(conditions.TypeNumber, "lt"), RightValue: float64(5)}, sensitive, true},
		"number gte equal":               {conditions.Condition{LeftValue: float64(5), Operator: op(conditions.TypeNumber, "gte"), RightValue: float64(5)}, sensitive, true},
		"number lte equal":               {conditions.Condition{LeftValue: float64(5), Operator: op(conditions.TypeNumber, "lte"), RightValue: float64(5)}, sensitive, true},
		"number empty is about presence": {conditions.Condition{LeftValue: nil, Operator: op(conditions.TypeNumber, "empty")}, sensitive, true},

		// boolean
		"boolean true":      {conditions.Condition{LeftValue: true, Operator: op(conditions.TypeBoolean, "true")}, sensitive, true},
		"boolean false":     {conditions.Condition{LeftValue: false, Operator: op(conditions.TypeBoolean, "false")}, sensitive, true},
		"boolean equals":    {conditions.Condition{LeftValue: true, Operator: op(conditions.TypeBoolean, "equals"), RightValue: true}, sensitive, true},
		"boolean notEquals": {conditions.Condition{LeftValue: true, Operator: op(conditions.TypeBoolean, "notEquals"), RightValue: false}, sensitive, true},

		// dateTime
		"date after":          {conditions.Condition{LeftValue: "2026-02-01T00:00:00Z", Operator: op(conditions.TypeDateTime, "after"), RightValue: "2026-01-01T00:00:00Z"}, sensitive, true},
		"date before":         {conditions.Condition{LeftValue: "2026-01-01", Operator: op(conditions.TypeDateTime, "before"), RightValue: "2026-02-01"}, sensitive, true},
		"date equals":         {conditions.Condition{LeftValue: "2026-01-01T00:00:00Z", Operator: op(conditions.TypeDateTime, "equals"), RightValue: "2026-01-01T00:00:00Z"}, sensitive, true},
		"date afterOrEquals":  {conditions.Condition{LeftValue: "2026-01-01T00:00:00Z", Operator: op(conditions.TypeDateTime, "afterOrEquals"), RightValue: "2026-01-01T00:00:00Z"}, sensitive, true},
		"date beforeOrEquals": {conditions.Condition{LeftValue: "2026-01-01T00:00:00Z", Operator: op(conditions.TypeDateTime, "beforeOrEquals"), RightValue: "2026-01-01T00:00:00Z"}, sensitive, true},

		// array
		"array contains":            {conditions.Condition{LeftValue: []any{"a", "b"}, Operator: op(conditions.TypeArray, "contains"), RightValue: "b"}, sensitive, true},
		"array contains cased":      {conditions.Condition{LeftValue: []any{"A"}, Operator: op(conditions.TypeArray, "contains"), RightValue: "a"}, sensitive, false},
		"array contains loose case": {conditions.Condition{LeftValue: []any{"A"}, Operator: op(conditions.TypeArray, "contains"), RightValue: "a"}, conditions.Options{}, true},
		"array notContains":         {conditions.Condition{LeftValue: []any{"a"}, Operator: op(conditions.TypeArray, "notContains"), RightValue: "z"}, sensitive, true},
		"array lengthEquals":        {conditions.Condition{LeftValue: []any{1, 2}, Operator: op(conditions.TypeArray, "lengthEquals"), RightValue: float64(2)}, sensitive, true},
		"array lengthGt":            {conditions.Condition{LeftValue: []any{1, 2}, Operator: op(conditions.TypeArray, "lengthGt"), RightValue: float64(1)}, sensitive, true},
		"array lengthLte":           {conditions.Condition{LeftValue: []any{1}, Operator: op(conditions.TypeArray, "lengthLte"), RightValue: float64(1)}, sensitive, true},
		"array empty":               {conditions.Condition{LeftValue: []any{}, Operator: op(conditions.TypeArray, "empty")}, sensitive, true},
		"a non-array is empty":      {conditions.Condition{LeftValue: "not a list", Operator: op(conditions.TypeArray, "empty")}, sensitive, true},

		// object
		"object empty":    {conditions.Condition{LeftValue: map[string]any{}, Operator: op(conditions.TypeObject, "empty")}, sensitive, true},
		"object notEmpty": {conditions.Condition{LeftValue: map[string]any{"a": 1}, Operator: op(conditions.TypeObject, "notEmpty")}, sensitive, true},

		// presence, asked before any conversion
		"exists":                  {conditions.Condition{LeftValue: "", Operator: op(conditions.TypeString, "exists")}, sensitive, true},
		"notExists":               {conditions.Condition{LeftValue: nil, Operator: op(conditions.TypeString, "notExists")}, sensitive, true},
		"exists on a zero number": {conditions.Condition{LeftValue: float64(0), Operator: op(conditions.TypeNumber, "exists")}, sensitive, true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := conditions.Match(testCase.condition, testCase.options)
			if err != nil {
				t.Fatalf("Match() error = %v", err)
			}
			if got != testCase.want {
				t.Errorf("Match() = %t, want %t", got, testCase.want)
			}
		})
	}
}

// Loose conversion is why a webhook workflow works at all: an inbound body is
// text, and a condition comparing one of its fields to a number has to mean
// what the author meant.
func TestLooseValidationConvertsAndStrictRefuses(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		condition conditions.Condition
		want      bool
	}{
		"text that is a number":  {conditions.Condition{LeftValue: "5", Operator: op(conditions.TypeNumber, "equals"), RightValue: float64(5)}, true},
		"a number as text":       {conditions.Condition{LeftValue: float64(5), Operator: op(conditions.TypeString, "equals"), RightValue: "5"}, true},
		"text that is a boolean": {conditions.Condition{LeftValue: "true", Operator: op(conditions.TypeBoolean, "true")}, true},
		"a number as a boolean":  {conditions.Condition{LeftValue: float64(1), Operator: op(conditions.TypeBoolean, "true")}, true},
		"epoch milliseconds as a date": {conditions.Condition{
			LeftValue: float64(1767225600000), Operator: op(conditions.TypeDateTime, "after"), RightValue: "2000-01-01",
		}, true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := conditions.Match(testCase.condition, conditions.Options{CaseSensitive: true})
			if err != nil {
				t.Fatalf("loose Match() error = %v", err)
			}
			if got != testCase.want {
				t.Errorf("loose Match() = %t, want %t", got, testCase.want)
			}
			// Strict refuses the same comparison, and says which side was
			// wrong — the only useful thing to say about it.
			strict := conditions.Options{CaseSensitive: true, TypeValidation: conditions.ValidationStrict}
			if _, err := conditions.Match(testCase.condition, strict); err == nil {
				t.Error("strict Match() accepted a value of the wrong type")
			} else if !strings.Contains(err.Error(), "left") && !strings.Contains(err.Error(), "right") {
				t.Errorf("strict error = %q, want it to name the side", err)
			}
		})
	}
}

// Several conditions, joined. IF used to accept exactly one, which made every
// "A and B" workflow unimportable.
func TestCombinatorsJoinSeveralConditions(t *testing.T) {
	t.Parallel()

	vip := conditions.Condition{LeftValue: "vip", Operator: op(conditions.TypeString, "equals"), RightValue: "vip"}
	large := conditions.Condition{LeftValue: float64(10), Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(5)}
	small := conditions.Condition{LeftValue: float64(1), Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(5)}

	for name, testCase := range map[string]struct {
		filter conditions.Filter
		want   bool
	}{
		"and, both true": {conditions.Filter{Conditions: []conditions.Condition{vip, large}, Combinator: conditions.CombineAnd}, true},
		"and, one false": {conditions.Filter{Conditions: []conditions.Condition{vip, small}, Combinator: conditions.CombineAnd}, false},
		"or, one true":   {conditions.Filter{Conditions: []conditions.Condition{small, vip}, Combinator: conditions.CombineOr}, true},
		"or, none true":  {conditions.Filter{Conditions: []conditions.Condition{small, small}, Combinator: conditions.CombineOr}, false},
		// No combinator is `and`: a filter that silently ORed would pass rows
		// nobody asked for.
		"no combinator is and": {conditions.Filter{Conditions: []conditions.Condition{vip, small}}, false},
		// No conditions passes: a half-built Filter lets everything through
		// rather than making the workflow unrunnable.
		"no conditions": {conditions.Filter{}, true},
	} {
		t.Run(name, func(t *testing.T) {
			testCase.filter.Options.CaseSensitive = true
			got, err := conditions.Evaluate(testCase.filter)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if got != testCase.want {
				t.Errorf("Evaluate() = %t, want %t", got, testCase.want)
			}
		})
	}
}

func TestAnUnsupportedOperationIsNamedRatherThanFalse(t *testing.T) {
	t.Parallel()

	// Returning false for an operation nobody implemented would route every
	// item down the same branch and look like the workflow's own logic.
	_, err := conditions.Match(conditions.Condition{
		LeftValue: "x", Operator: op(conditions.TypeString, "soundsLike"), RightValue: "y",
	}, conditions.Options{})
	if err == nil || !strings.Contains(err.Error(), "soundsLike") {
		t.Fatalf("Match() error = %v, want the operation named", err)
	}
	if _, err := conditions.Match(conditions.Condition{
		Operator: conditions.Operator{Type: "colour", Operation: "equals"},
	}, conditions.Options{}); err == nil {
		t.Fatal("an unknown condition type was accepted")
	}
}

// Regex is implemented rather than refused because Go's regexp is RE2: it runs
// in time linear in the input and cannot backtrack catastrophically, so a
// pattern taken from a workflow document is not the denial of service the same
// pattern would be in a backtracking engine.
func TestRegexOperators(t *testing.T) {
	t.Parallel()

	sensitive := conditions.Options{CaseSensitive: true}
	for name, testCase := range map[string]struct {
		left, pattern string
		operation     string
		options       conditions.Options
		want          bool
	}{
		"matches":                 {"ada@example.com", ".*@example", "regex", sensitive, true},
		"does not match":          {"ada@other.com", ".*@example", "regex", sensitive, false},
		"notRegex inverts":        {"ada@other.com", ".*@example", "notRegex", sensitive, true},
		"a slash literal":         {"ada@example.com", "/.*@EXAMPLE/i", "regex", sensitive, true},
		"case insensitive filter": {"ADA@EXAMPLE.com", ".*@example", "regex", conditions.Options{}, true},
		// A character class must survive a case-insensitive filter: lower-casing
		// the pattern along with the value would turn [A-Z] into [a-z].
		"a character class is not lower-cased": {"ABC", "[A-Z]+", "regex", conditions.Options{}, true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := conditions.Match(conditions.Condition{
				LeftValue:  testCase.left,
				Operator:   op(conditions.TypeString, testCase.operation),
				RightValue: testCase.pattern,
			}, testCase.options)
			if err != nil {
				t.Fatalf("Match() error = %v", err)
			}
			if got != testCase.want {
				t.Errorf("Match() = %t, want %t", got, testCase.want)
			}
		})
	}

	// An unusable pattern is named rather than silently never matching.
	if _, err := conditions.Match(conditions.Condition{
		LeftValue: "x", Operator: op(conditions.TypeString, "regex"), RightValue: "[",
	}, sensitive); err == nil {
		t.Error("an invalid pattern was accepted")
	}
}

// The loose conversion table, row by row against live n8n 2.x (IF 2.2 with
// "Convert types where required" on).
//
// This is the difference between a workflow that runs on a webhook body and one
// that aborts on the first unusual value: a body is all text, and `"yes"`, `"1"`
// and `""` are ordinary things for it to carry. n8n reads them — its boolean
// conversion recognises `"true"`/`"false"` and anything its `Number()` reads as
// exactly 0 or 1, and falls back to JavaScript truthiness for the rest, which is
// why `"yes"` — and `"no"` — are both true there; its number conversion is
// `Number()`, where an empty string is zero; and a value that is not there
// routes the item rather than failing the run.
func TestLooseConversionFollowsN8nsOwnTable(t *testing.T) {
	t.Parallel()

	loose := conditions.Options{CaseSensitive: true}
	// The expression engine's sentinel, which is what a left value reads as when
	// the field is not in the item at all.
	missing := expression.Undefined

	for name, testCase := range map[string]struct {
		condition conditions.Condition
		want      bool
	}{
		// Boolean "is true", the values n8n's own repro used.
		`"true" is true`:  {conditions.Condition{LeftValue: "true", Operator: op(conditions.TypeBoolean, "true")}, true},
		`"yes" is true`:   {conditions.Condition{LeftValue: "yes", Operator: op(conditions.TypeBoolean, "true")}, true},
		`"1" is true`:     {conditions.Condition{LeftValue: "1", Operator: op(conditions.TypeBoolean, "true")}, true},
		`"no" is true`:    {conditions.Condition{LeftValue: "no", Operator: op(conditions.TypeBoolean, "true")}, true},
		`"false" is true`: {conditions.Condition{LeftValue: "false", Operator: op(conditions.TypeBoolean, "true")}, false},
		`"0" is true`:     {conditions.Condition{LeftValue: "0", Operator: op(conditions.TypeBoolean, "true")}, false},
		`"" is true`:      {conditions.Condition{LeftValue: "", Operator: op(conditions.TypeBoolean, "true")}, false},
		// A field that is null and one that is not there are both false, not an
		// error: this is the row that used to stop the execution.
		`null is true`:                     {conditions.Condition{LeftValue: nil, Operator: op(conditions.TypeBoolean, "true")}, false},
		`a missing field is true`:          {conditions.Condition{LeftValue: missing, Operator: op(conditions.TypeBoolean, "true")}, false},
		`a missing field is not false`:     {conditions.Condition{LeftValue: missing, Operator: op(conditions.TypeBoolean, "false")}, true},
		`"1" equals "yes"`:                 {conditions.Condition{LeftValue: "yes", Operator: op(conditions.TypeBoolean, "equals"), RightValue: "1"}, true},
		`a missing field equals null`:      {conditions.Condition{LeftValue: missing, Operator: op(conditions.TypeBoolean, "equals"), RightValue: nil}, false},
		`a missing field equals a missing`: {conditions.Condition{LeftValue: missing, Operator: op(conditions.TypeBoolean, "equals"), RightValue: missing}, true},

		// Number, greater than 4 — n8n's other repro.
		`"10" is greater than 4`:            {conditions.Condition{LeftValue: "10", Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(4)}, true},
		`" 3 " is greater than 4`:           {conditions.Condition{LeftValue: " 3 ", Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(4)}, false},
		`"5.5" is greater than 4`:           {conditions.Condition{LeftValue: "5.5", Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(4)}, true},
		`"" is greater than 4`:              {conditions.Condition{LeftValue: "", Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(4)}, false},
		`a missing field is greater than 4`: {conditions.Condition{LeftValue: missing, Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(4)}, false},
		`true is greater than 4`:            {conditions.Condition{LeftValue: true, Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(4)}, false},
		// JavaScript's Number('') is 0, so an empty field compares as zero.
		`"" equals 0`: {conditions.Condition{LeftValue: "", Operator: op(conditions.TypeNumber, "equals"), RightValue: float64(0)}, true},
		// Null is coerced to zero in a relational comparison and is not equal to
		// it; undefined satisfies neither.
		`null is at most 0`:            {conditions.Condition{LeftValue: nil, Operator: op(conditions.TypeNumber, "lte"), RightValue: float64(0)}, true},
		`a missing field is at most 0`: {conditions.Condition{LeftValue: missing, Operator: op(conditions.TypeNumber, "lte"), RightValue: float64(0)}, false},
		`null equals 0`:                {conditions.Condition{LeftValue: nil, Operator: op(conditions.TypeNumber, "equals"), RightValue: float64(0)}, false},

		// An empty value is absent for `empty`/`exists`, whatever its type.
		`mean empty is empty`:    {conditions.Condition{LeftValue: missing, Operator: op(conditions.TypeNumber, "empty")}, true},
		`mean empty is notEmpty`: {conditions.Condition{LeftValue: missing, Operator: op(conditions.TypeNumber, "notEmpty")}, false},
		`null exists`:            {conditions.Condition{LeftValue: nil, Operator: op(conditions.TypeBoolean, "exists")}, false},

		// Text that carries JSON is that JSON, which is what a sheet column or a
		// webhook body holds.
		`"[1,2]" contains 1`:     {conditions.Condition{LeftValue: "[1,2]", Operator: op(conditions.TypeArray, "contains"), RightValue: float64(1)}, true},
		`"[1,2]" has length 2`:   {conditions.Condition{LeftValue: "[1,2]", Operator: op(conditions.TypeArray, "lengthEquals"), RightValue: float64(2)}, true},
		`"['a']" contains "a"`:   {conditions.Condition{LeftValue: "['a']", Operator: op(conditions.TypeArray, "contains"), RightValue: "a"}, true},
		`"[]" is empty`:          {conditions.Condition{LeftValue: "[]", Operator: op(conditions.TypeArray, "empty")}, true},
		`"not a list" is empty`:  {conditions.Condition{LeftValue: "not a list", Operator: op(conditions.TypeArray, "empty")}, true},
		`'{"a":1}' is not empty`: {conditions.Condition{LeftValue: `{"a":1}`, Operator: op(conditions.TypeObject, "notEmpty")}, true},
		`'{}' is empty`:          {conditions.Condition{LeftValue: `{}`, Operator: op(conditions.TypeObject, "empty")}, true},

		// Text and dates: an absent value reads as empty text, and a date that
		// is not there is not compared.
		`a missing field is empty text`: {conditions.Condition{LeftValue: missing, Operator: op(conditions.TypeString, "equals"), RightValue: ""}, true},
		`a missing date is after`:       {conditions.Condition{LeftValue: missing, Operator: op(conditions.TypeDateTime, "after"), RightValue: "2026-01-01"}, false},
		`a null date is before`:         {conditions.Condition{LeftValue: nil, Operator: op(conditions.TypeDateTime, "before"), RightValue: "2026-01-01"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := conditions.Match(testCase.condition, loose)
			if err != nil {
				t.Fatalf("Match() error = %v", err)
			}
			if got != testCase.want {
				t.Errorf("Match() = %t, want %t", got, testCase.want)
			}
		})
	}

	// Strict is about a value of the wrong type, and an absent value has no type
	// to be wrong: n8n's strict path accepts it too, and then refuses to
	// compare it. Refusing the comparison instead would fail a run over an
	// optional field.
	strict := conditions.Options{CaseSensitive: true, TypeValidation: conditions.ValidationStrict}
	for name, condition := range map[string]conditions.Condition{
		"a missing number is greater than 4": {LeftValue: missing, Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(4)},
		"a missing date is after":            {LeftValue: missing, Operator: op(conditions.TypeDateTime, "after"), RightValue: "2026-01-01"},
		"a missing boolean is true":          {LeftValue: missing, Operator: op(conditions.TypeBoolean, "true")},
	} {
		t.Run("strict: "+name, func(t *testing.T) {
			got, err := conditions.Match(condition, strict)
			if err != nil {
				t.Fatalf("Match() error = %v", err)
			}
			if got {
				t.Error("Match() = true, want an absent value to match nothing")
			}
		})
	}
}

// A value that genuinely cannot be converted is still named rather than routed:
// n8n fails the comparison there too, and a branch taken on an unconvertible
// value would look like the workflow's own logic.
//
// A boolean is deliberately not among them: n8n's boolean validation falls back
// to JavaScript truthiness, so a string it does not recognise is true rather
// than an error.
func TestLooseConversionStillNamesAnUnconvertibleValue(t *testing.T) {
	t.Parallel()

	for name, condition := range map[string]conditions.Condition{
		"a number": {LeftValue: "not a number", Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(4)},
		"a date":   {LeftValue: "not a date", Operator: op(conditions.TypeDateTime, "after"), RightValue: "2026-01-01"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := conditions.Match(condition, conditions.Options{CaseSensitive: true})
			if err == nil {
				t.Fatal("an unconvertible value was accepted")
			}
			if !strings.Contains(err.Error(), "left") {
				t.Errorf("error = %q, want it to name the side", err)
			}
		})
	}

	// A boolean it does not recognise is truthy, not an error.
	matched, err := conditions.Match(conditions.Condition{
		LeftValue: "maybe", Operator: op(conditions.TypeBoolean, "true"),
	}, conditions.Options{CaseSensitive: true})
	if err != nil || !matched {
		t.Errorf(`Match("maybe") = %t, %v; want true with no error`, matched, err)
	}

	// Strict still refuses a value of the wrong type, and an empty number is a
	// value of the wrong type there: n8n refuses it as well.
	if _, err := conditions.Match(conditions.Condition{
		LeftValue: "", Operator: op(conditions.TypeNumber, "gt"), RightValue: float64(4),
	}, conditions.Options{CaseSensitive: true, TypeValidation: conditions.ValidationStrict}); err == nil {
		t.Error("strict accepted an empty string as a number")
	}
}

// The count comparisons have two spellings and both have to run.
//
// n8n's own filter writes gt, gte, lt and lte; its v1 If — and this project's
// condition editor, which kept the v1 names — write larger, largerEqual,
// smaller and smallerEqual for the same four comparisons. A spelling the
// evaluator did not know failed the run outright with "not supported for a
// number value", so a number condition that came from a v1 import or from the
// editor stopped the workflow instead of taking a branch.
func TestBothSpellingsOfTheCountComparisonsRun(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		n8n, legacy string
		left, right float64
		want        bool
	}{
		"larger":            {"gt", "larger", 6, 5, true},
		"not larger":        {"gt", "larger", 5, 5, false},
		"larger equal":      {"gte", "largerEqual", 5, 5, true},
		"not larger equal":  {"gte", "largerEqual", 4, 5, false},
		"smaller":           {"lt", "smaller", 4, 5, true},
		"not smaller":       {"lt", "smaller", 5, 5, false},
		"smaller equal":     {"lte", "smallerEqual", 5, 5, true},
		"not smaller equal": {"lte", "smallerEqual", 6, 5, false},
	} {
		t.Run(name, func(t *testing.T) {
			for _, operation := range []string{testCase.n8n, testCase.legacy} {
				got, err := conditions.Match(conditions.Condition{
					LeftValue: testCase.left, RightValue: testCase.right,
					Operator: op(conditions.TypeNumber, operation),
				}, conditions.Options{CaseSensitive: true})
				if err != nil {
					t.Fatalf("operation %q does not run: %v", operation, err)
				}
				if got != testCase.want {
					t.Errorf("%q: %v %s %v = %t, want %t",
						operation, testCase.left, operation, testCase.right, got, testCase.want)
				}
			}
		})
	}
}
