package expression_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/expression"
)

// These are the parity probes the full review ran side by side against n8n.
// Every one of them was either an error here or, worse, a different value.

func parityContext() expression.Context {
	return expression.Context{
		JSON: map[string]any{
			"count":  float64(42),
			"active": true,
			"name":   "Ada Lovelace",
			"email":  "Ada.Lovelace@Example.com",
			"tags":   []any{"alpha", "beta", "gamma"},
			"items":  []any{map[string]any{"price": float64(3)}, map[string]any{"price": float64(4)}},
			"profile": map[string]any{
				"city": "London",
			},
			"título": "acentuado",
			"a]b":    "bracket",
		},
		Env:       map[string]string{"REGION": "eu-west-1"},
		ItemIndex: 0,
	}
}

func evaluateOne(t *testing.T, template string, ctx expression.Context) any {
	t.Helper()
	value, err := expression.Evaluate(template, ctx)
	if err != nil {
		t.Fatalf("Evaluate(%s) error = %v", template, err)
	}
	return value
}

// TestOperatorsMatchJavaScript is the grammar the evaluator did not have at
// all: every one of these was a parse error, and 205 of 1465 expression
// parameters in the imported corpus use one of them.
func TestOperatorsMatchJavaScript(t *testing.T) {
	t.Parallel()

	for template, want := range map[string]any{
		"{{ $json.count + 1 }}":                            float64(43),
		"{{ $json.count * 2 - 4 }}":                        float64(80),
		"{{ $json.count / 2 }}":                            float64(21),
		"{{ $json.count % 5 }}":                            float64(2),
		"{{ 'n=' + $json.count }}":                         "n=42",
		"{{ $json.count > 40 }}":                           true,
		"{{ $json.count <= 42 }}":                          true,
		"{{ $json.count === 42 }}":                         true,
		"{{ $json.count === '42' }}":                       false,
		"{{ $json.count == '42' }}":                        true,
		"{{ $json.active && 'yes' }}":                      "yes",
		"{{ $json.missing || 'default' }}":                 "default",
		"{{ $json.missing ?? 'fallback' }}":                "fallback",
		"{{ 0 ?? 'fallback' }}":                            float64(0),
		"{{ $json.active ? 'on' : 'off' }}":                "on",
		"{{ !$json.active }}":                              false,
		"{{ -$json.count }}":                               float64(-42),
		"{{ `Hello ${$json.name}!` }}":                     "Hello Ada Lovelace!",
		"{{ $json.profile?.city }}":                        "London",
		"{{ $json.missing?.deeper }}":                      nil,
		"{{ [1, 2, 3].length }}":                           float64(3),
		"{{ ({a: 1, b: 2}).b }}":                           float64(2),
		"{{ $json.tags.map(t => t.toUpperCase()) }}":       []any{"ALPHA", "BETA", "GAMMA"},
		"{{ $json.tags.filter(t => t.includes('a')) }}":    []any{"alpha", "beta", "gamma"},
		"{{ $json.items.map(i => i.price).sum() }}":        float64(7),
		"{{ Math.max(1, 5, 3) }}":                          float64(5),
		"{{ Math.max(...$json.items.map(i => i.price)) }}": float64(4),
	} {
		got := evaluateOne(t, template, parityContext())
		if !sameValue(got, want) {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}
}

func sameValue(got, want any) bool {
	left, err := json.Marshal(got)
	if err != nil {
		return false
	}
	right, err := json.Marshal(want)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}

// TestFunctionsKeepTheirJavaScriptMeaning covers the silent differences: the
// same name, a different answer, and no error to notice.
func TestFunctionsKeepTheirJavaScriptMeaning(t *testing.T) {
	t.Parallel()

	for template, want := range map[string]any{
		// replace replaces the first occurrence; replaceAll does the rest.
		"{{ $json.email.replace('a', 'A') }}":    "AdA.Lovelace@Example.com",
		"{{ $json.email.replace('.', '_') }}":    "Ada_Lovelace@Example.com",
		"{{ $json.email.replaceAll('.', '_') }}": "Ada_Lovelace@Example_com",
		// includes is type-strict on a list, like Array.prototype.includes.
		"{{ [3, 1, 2, 2].includes(2) }}":    true,
		"{{ [3, 1, 2, 2].includes('2') }}":  false,
		"{{ $json.tags.includes('beta') }}": true,
		// join defaults to a comma, and .length is a property.
		"{{ $json.tags.join() }}":      "alpha,beta,gamma",
		"{{ $json.tags.join(' / ') }}": "alpha / beta / gamma",
		"{{ $json.tags.length }}":      float64(3),
		"{{ $json.name.length }}":      float64(12),
		// A backslash escape in a single-quoted literal is an escape.
		`{{ $json.tags.join('\n- ') }}`:                     "alpha\n- beta\n- gamma",
		"{{ $json.name.slice(0, 3) }}":                      "Ada",
		"{{ $json.name.indexOf('Love') }}":                  float64(4),
		"{{ $json.email.split('@')[1] }}":                   "Example.com",
		"{{ $json.count.toFixed(2) }}":                      "42.00",
		"{{ [1, 2, 3].sum() }}":                             float64(6),
		"{{ [1, 2, 2, 3].removeDuplicates() }}":             []any{float64(1), float64(2), float64(3)},
		"{{ $json.profile.isEmpty() }}":                     false,
		"{{ ''.isEmpty() }}":                                true,
		"{{ 'https://api.example.com/x'.extractDomain() }}": "api.example.com",
		"{{ 'hello world'.toUpperCase() }}":                 "HELLO WORLD",
	} {
		got := evaluateOne(t, template, parityContext())
		if !sameValue(got, want) {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}

	// .length is a property, not a call: n8n refuses `.length()`.
	if _, err := expression.Evaluate("{{ $json.tags.length() }}", parityContext()); err == nil {
		t.Error("length() was accepted, want a refusal — length is a property")
	}
}

// TestValuesInTextRenderTheWayN8nRendersThem: a list joins with commas and an
// object is JSON, never Go's `[alpha beta]` or `map[city:London]`.
func TestValuesInTextRenderTheWayN8nRendersThem(t *testing.T) {
	t.Parallel()

	for template, want := range map[string]any{
		"Tags: {{ $json.tags }}":    "Tags: alpha,beta,gamma",
		"City: {{ $json.profile }}": `City: {"city":"London"}`,
		// A lone object is the object itself; only inside surrounding text is
		// it JSON, which is how n8n behaves.

		"{{ $json.missing }}":        nil,
		"{{ [1, null, 'x'] }}":       []any{float64(1), nil, "x"},
		"name: {{ $json.missing }}!": "name: !",
		"{{ $json.active }}":         true,
	} {
		got := evaluateOne(t, template, parityContext())
		if !sameValue(got, want) {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}
}

// TestJSONEncodingIsAvailable is the capability the grammar did not have at
// all: without it an object or free text cannot be placed in a JSON body.
func TestJSONEncodingIsAvailable(t *testing.T) {
	t.Parallel()

	for template, want := range map[string]any{
		"{{ JSON.stringify($json.profile) }}": `{"city":"London"}`,
		"{{ $json.profile.toJsonString() }}":  `{"city":"London"}`,
		"{{ Object.keys($json.profile) }}":    []any{"city"},
		"{{ Object.values($json.profile) }}":  []any{"London"},
		"{{ JSON.parse('{\"a\":1}').a }}":     float64(1),
		"{{ JSON.stringify([1, 2]) }}":        "[1,2]",
		"{{ Object.keys($json.tags) }}":       []any{"0", "1", "2"},
	} {
		got := evaluateOne(t, template, parityContext())
		if !sameValue(got, want) {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}

	// An indented stringify is what builds a readable prompt.
	indented := evaluateOne(t, "{{ JSON.stringify($json.profile, null, 2) }}", parityContext())
	if indented != "{\n  \"city\": \"London\"\n}" {
		t.Errorf("indented stringify = %#v", indented)
	}
}

// TestDatesAreLuxonDatesAndNeverLeakTheEvaluatorsStruct covers both halves of
// the date finding: Luxon tokens rather than Go layouts, and a value a node can
// actually consume.
func TestDatesAreLuxonDatesAndNeverLeakTheEvaluatorsStruct(t *testing.T) {
	t.Parallel()

	ctx := parityContext()
	ctx.Now = time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)

	for template, want := range map[string]any{
		`{{ $now.toFormat('yyyy') }}`:                                "2026",
		`{{ $now.format('yyyy') }}`:                                  "2026",
		`{{ $now.format('yyyy-MM-dd HH:mm:ss') }}`:                   "2026-09-05 14:30:00",
		`{{ $now.format('d. MMM. y') }}`:                             "5. Sep. 2026",
		`{{ $now.format("dd/LL/yyyy") }}`:                            "05/09/2026",
		`{{ $now.format('EEEE') }}`:                                  "Saturday",
		`{{ $now.format('yyyy-MM-dd') }}`:                            "2026-09-05",
		`{{ $now.plus({days: 1}).format('yyyy-MM-dd') }}`:            "2026-09-06",
		`{{ $now.minus({days: 1}).format('yyyy-MM-dd') }}`:           "2026-09-04",
		`{{ $now.plus({months: 1}).format('yyyy-MM-dd') }}`:          "2026-10-05",
		`{{ $now.plus(1, 'day').format('yyyy-MM-dd') }}`:             "2026-09-06",
		`{{ $now.startOf('month').format('yyyy-MM-dd') }}`:           "2026-09-01",
		`{{ $now.endOf('day').format('yyyy-MM-dd HH:mm:ss.SSS') }}`:  "2026-09-05 23:59:59.999",
		`{{ $now.toISODate() }}`:                                     "2026-09-05",
		`{{ $now.toISO() }}`:                                         "2026-09-05T14:30:00.000+00:00",
		`{{ $now.toISOString() }}`:                                   "2026-09-05T14:30:00.000Z",
		`{{ $now.toMillis() }}`:                                      float64(time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC).UnixMilli()),
		`{{ $now.year }}`:                                            float64(2026),
		`{{ $now.weekday }}`:                                         float64(6),
		`{{ DateTime.fromISO('2026-01-02T03:04:05Z').toISODate() }}`: "2026-01-02",
		`{{ $now.toDateTime().format('yyyy') }}`:                     "2026",
		`{{ '2026-01-02T03:04:05Z'.toDateTime().toISODate() }}`:      "2026-01-02",
	} {
		got := evaluateOne(t, template, ctx)
		if !sameValue(got, want) {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}

	// A lone date is a time.Time, so a Set node writes a timestamp rather than
	// the "{}" the unexported struct used to marshal to, and the DateTime and
	// IF nodes accept it.
	lone := evaluateOne(t, "{{ $now }}", ctx)
	if _, ok := lone.(time.Time); !ok {
		t.Fatalf("{{ $now }} = %#v (%T), want a time.Time", lone, lone)
	}
	encoded, err := json.Marshal(lone)
	if err != nil {
		t.Fatalf("marshalling a date failed: %v", err)
	}
	if string(encoded) != `"2026-09-05T14:30:00Z"` {
		t.Errorf("marshalled date = %s, want an ISO timestamp", encoded)
	}

	// Nested inside an object or a list it still marshals as text rather than
	// as "{}".
	nested := evaluateOne(t, "{{ {at: $now} }}", ctx)
	encoded, err = json.Marshal(nested)
	if err != nil {
		t.Fatalf("marshalling a nested date failed: %v", err)
	}
	if string(encoded) != `{"at":"2026-09-05T14:30:00.000+00:00"}` {
		t.Errorf("nested date = %s, want the ISO text", encoded)
	}
}

// TestNowAndTodayReadTheWorkflowTimezone is the other half: with no timezone
// $today falls on the wrong calendar day for most of the world.
func TestNowAndTodayReadTheWorkflowTimezone(t *testing.T) {
	t.Parallel()

	ctx := parityContext()
	ctx.Timezone = "America/Los_Angeles"
	// 08:45 UTC is 01:45 in Los Angeles, still the 19th there.
	ctx.Now = time.Date(2026, 9, 19, 8, 45, 5, 127*int(time.Millisecond), time.UTC)

	if got := evaluateOne(t, "{{ $today.toISO() }}", ctx); got != "2026-09-19T00:00:00.000-07:00" {
		t.Errorf("$today = %#v, want midnight in the workflow timezone", got)
	}
	if got := evaluateOne(t, "{{ $now.toISO() }}", ctx); got != "2026-09-19T01:45:05.127-07:00" {
		t.Errorf("$now = %#v, want the instant in the workflow timezone", got)
	}
	if got := evaluateOne(t, "{{ $now.format('yyyy-MM-dd') }}", ctx); got != "2026-09-19" {
		t.Errorf("$now.format = %#v, want the local calendar day", got)
	}
}

// TestEnvReadsTheAllowlistAndRefusesTheRest: $env was always empty, and an
// unknown key resolved silently to "".
func TestEnvReadsTheAllowlistAndRefusesTheRest(t *testing.T) {
	t.Parallel()

	if got := evaluateOne(t, "{{ $env.REGION }}", parityContext()); got != "eu-west-1" {
		t.Errorf("$env.REGION = %#v, want the allowlisted value", got)
	}
	if got := evaluateOne(t, "{{ $env['REGION'] }}", parityContext()); got != "eu-west-1" {
		t.Errorf("$env['REGION'] = %#v, want the allowlisted value", got)
	}
	// A key that is not in the allowlist is a loud error naming the variable,
	// not an empty string sent to an API as if it were the secret.
	for _, template := range []string{"{{ $env.HOME }}", "{{ $env['GOOGLE_API_KEY'] }}"} {
		if _, err := expression.Evaluate(template, parityContext()); err == nil {
			t.Errorf("Evaluate(%s) succeeded, want a refusal naming the allowlist variable", template)
		}
	}
	// The whole map is still readable, which is how a workflow enumerates it.
	if got := evaluateOne(t, "{{ Object.keys($env) }}", parityContext()); !sameValue(got, []any{"REGION"}) {
		t.Errorf("Object.keys($env) = %#v", got)
	}
}

// TestInputFollowsTheN8nApi: $input was a KilasFlow-only map of port to items.
func TestInputFollowsTheN8nApi(t *testing.T) {
	t.Parallel()

	ctx := parityContext()
	ctx.Input = map[string][]map[string]any{
		"main": {{"v": "a"}, {"v": "b"}, {"v": "c"}},
	}
	ctx.JSON = map[string]any{"v": "b"}

	for template, want := range map[string]any{
		"{{ $input.item.json.v }}":              "b",
		"{{ $input.first().json.v }}":           "a",
		"{{ $input.last().json.v }}":            "c",
		"{{ $input.all().length }}":             float64(3),
		"{{ $input.all().map(i => i.json.v) }}": []any{"a", "b", "c"},
		// The port map still resolves, which is the shape KilasFlow documented.
		"{{ $input.main[1].v }}":      "b",
		"{{ $input.main[0].json.v }}": "a",
		"{{ $items().length }}":       float64(3),
	} {
		got := evaluateOne(t, template, ctx)
		if !sameValue(got, want) {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}
}

// TestNodeJsonFollowsTheCurrentItem is the silent-wrong-answer finding: three
// items from Split Out used to read "a" three times.
func TestNodeJsonFollowsTheCurrentItem(t *testing.T) {
	t.Parallel()

	ctx := parityContext()
	ctx.NodeItems = map[string]expression.NodeItem{
		"Split Out": {
			JSON:  map[string]any{"v": "a"},
			Items: []map[string]any{{"v": "a"}, {"v": "b"}, {"v": "c"}},
			ItemOrigins: []string{
				expression.OriginKey("split", "main", 0, 0),
				expression.OriginKey("split", "main", 0, 1),
				expression.OriginKey("split", "main", 0, 2),
			},
		},
	}

	// Without lineage the position is the correspondence n8n uses.
	for index, want := range []any{"a", "b", "c"} {
		ctx.ItemIndex = index
		for _, template := range []string{`{{ $node["Split Out"].json.v }}`, `{{ $('Split Out').json.v }}`} {
			got := evaluateOne(t, template, ctx)
			if got != want {
				t.Errorf("item %d: Evaluate(%s) = %#v, want %#v", index, template, got, want)
			}
		}
	}

	// With lineage the paired item wins over the position.
	ctx.ItemIndex = 0
	ctx.NodeItems["Split Out"] = expression.NodeItem{
		JSON:   map[string]any{"v": "a"},
		Items:  []map[string]any{{"v": "a"}, {"v": "b"}, {"v": "c"}},
		Paired: map[string]any{"v": "c"},
	}
	for _, template := range []string{`{{ $node["Split Out"].json.v }}`, `{{ $('Split Out').item.json.v }}`} {
		if got := evaluateOne(t, template, ctx); got != "c" {
			t.Errorf("Evaluate(%s) = %#v, want the paired item", template, got)
		}
	}
}

// TestUndefinedSemanticsMatchJavaScript: an index past the end, or a field read
// on a scalar, used to abort the whole run.
func TestUndefinedSemanticsMatchJavaScript(t *testing.T) {
	t.Parallel()

	for template, want := range map[string]any{
		"{{ $json.tags[5] }}":             nil,
		"{{ $json.tags[5] ?? 'none' }}":   "none",
		"{{ $json.name.missingProp }}":    nil,
		"{{ $json.count.foo }}":           nil,
		"{{ $json.name[0] }}":             "A",
		"{{ $json.name[99] }}":            nil,
		"{{ $json.profile.nope.deeper }}": nil,
	} {
		got := evaluateOne(t, template, parityContext())
		if !sameValue(got, want) {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}
}

// TestParserUnderstandsQuotesAndUnicode: the paren and bracket scanners counted
// depth without looking at quotes, and identifiers were ASCII only.
func TestParserUnderstandsQuotesAndUnicode(t *testing.T) {
	t.Parallel()

	for template, want := range map[string]any{
		`{{ $json.name.split(')') }}`:        []any{"Ada Lovelace"},
		`{{ $json.name.replace('(', '[') }}`: "Ada Lovelace",
		`{{ $json['a]b'] }}`:                 "bracket",
		`{{ $json["título"] }}`:              "acentuado",
		`{{ $json.título }}`:                 "acentuado",
		"{{ $json.name.split(' ') }}":        []any{"Ada", "Lovelace"},
		"{{ 'it\\'s'.length }}":              float64(4),
		"{{ `a ${'}'} b` }}":                 "a } b",
	} {
		got := evaluateOne(t, template, parityContext())
		if !sameValue(got, want) {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}
}

// TestExpressionsCannotBecomeCode keeps the safety property the widened grammar
// must not have given up.
func TestExpressionsCannotBecomeCode(t *testing.T) {
	t.Parallel()

	for _, template := range []string{
		"{{ process.exit(1) }}",
		"{{ require('fs').readFileSync('/etc/passwd') }}",
		"{{ $json.name.constructor('return 1')() }}",
		"{{ $json.name.exec('rm -rf /') }}",
		"{{ globalThis.process }}",
		"{{ this.constructor }}",
		"{{ $json.count = 1 }}",
		"{{ (function(){ return 1 })() }}",
		"{{ import('fs') }}",
		"{{ eval('1+1') }}",
	} {
		if _, err := expression.Evaluate(template, parityContext()); err == nil {
			t.Errorf("Evaluate(%s) succeeded, want a refusal", template)
		}
	}
}

// TestAResolvedDateSurvivesJSONEncoding is the Set-node path without the node:
// Resolve a parameter tree, marshal it, and check the date is a timestamp.
// Before, the evaluator's own struct escaped and marshalled to "{}", so the
// value written to a sheet or an API body was the two characters `{}`.
func TestAResolvedDateSurvivesJSONEncoding(t *testing.T) {
	t.Parallel()

	ctx := parityContext()
	ctx.Now = time.Date(2026, 9, 19, 8, 35, 38, 0, time.UTC)
	ctx.Timezone = "Asia/Jakarta"

	resolved, err := expression.Resolve(map[string]any{
		"at":      map[string]any{"mode": "expression", "value": "{{ $now }}"},
		"today":   map[string]any{"mode": "expression", "value": "{{ $today }}"},
		"shifted": map[string]any{"mode": "expression", "value": "{{ $now.plusDays(1) }}"},
		"text":    map[string]any{"mode": "expression", "value": "run at {{ $now }}"},
	}, ctx)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("marshalling the resolved parameters failed: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decoding the resolved parameters failed: %v", err)
	}
	for key, want := range map[string]string{
		"at":      "2026-09-19T15:35:38+07:00",
		"today":   "2026-09-19T00:00:00+07:00",
		"shifted": "2026-09-20T15:35:38+07:00",
		"text":    "run at 2026-09-19T15:35:38.000+07:00",
	} {
		if decoded[key] != want {
			t.Errorf("resolved[%q] = %#v, want %#v", key, decoded[key], want)
		}
	}
}
