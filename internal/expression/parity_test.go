package expression_test

import (
	"encoding/json"
	"strconv"
	"strings"
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

// TestCommonJavaScriptNamesResolve is the other half of parity: a workflow that
// uses a name JavaScript has should not fail on this runtime because the
// allowlist was written by hand and missed one.
func TestCommonJavaScriptNamesResolve(t *testing.T) {
	t.Parallel()

	for template, want := range map[string]any{
		"{{ 'a'.concat('b', 1) }}":                             "ab1",
		"{{ ['b', 'a'].sort() }}":                              []any{"a", "b"},
		"{{ [1, 2].concat([3], 4) }}":                          []any{float64(1), float64(2), float64(3), float64(4)},
		"{{ '  x  '.trimStart() }}":                            "x  ",
		"{{ 'x'.padStart(3, '0') }}":                           "00x",
		"{{ 'ab'.repeat(2) }}":                                 "abab",
		"{{ [1, [2, [3]]].flat() }}":                           []any{float64(1), float64(2), []any{float64(3)}},
		"{{ ['a', 'b'].at(-1) }}":                              "b",
		"{{ Number.isInteger(2) }}":                            true,
		"{{ Array.isArray($json.tags) }}":                      true,
		"{{ Array.from('ab') }}":                               []any{"a", "b"},
		"{{ 'a=1&b=2'.split('&').map(p => p.split('=')[1]) }}": []any{"1", "2"},
	} {
		got := evaluateOne(t, template, parityContext())
		if !sameValue(got, want) {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}
}

// TestInputAndNamespacesResolveToJSONValues is the `{}` escape class the
// ticket had already closed for dates: the lone-value switch in Evaluate knew
// about undefined and a date, so everything else the evaluator owns reached a
// Set node as the two characters `{}`. `$input` is a regression from the port
// map it returned before the rewrite, and a namespace is a bag of functions
// whose own JSON encoding is `{}` — but produced on purpose, not by marshalling
// an unexported struct.
func TestInputAndNamespacesResolveToJSONValues(t *testing.T) {
	t.Parallel()

	ctx := parityContext()
	ctx.Input = map[string][]map[string]any{
		"main": {{"v": "a"}, {"v": "b"}, {"v": "c"}},
	}
	const ports = `{"main":[{"v":"a"},{"v":"b"},{"v":"c"}]}`
	// A stringify result is itself a string, so its JSON encoding is quoted.
	encoded := func(text string) string {
		quoted, err := json.Marshal(text)
		if err != nil {
			t.Fatalf("marshalling %q failed: %v", text, err)
		}
		return string(quoted)
	}

	for template, want := range map[string]string{
		"{{ $input }}":                 ports,
		"{{ JSON.stringify($input) }}": encoded(ports),
		"{{ [$input] }}":               `[` + ports + `]`,
		"{{ JSON }}":                   `{}`,
		"{{ JSON.stringify(JSON) }}":   encoded("{}"),
		"{{ JSON.stringify(Math) }}":   encoded("{}"),
		"{{ JSON.stringify($env) }}":   encoded(`{"REGION":"eu-west-1"}`),
	} {
		got := evaluateOne(t, template, ctx)
		gotJSON, err := json.Marshal(got)
		if err != nil {
			t.Errorf("Evaluate(%s) = %#v (%T), which does not marshal: %v", template, got, got, err)
			continue
		}
		if string(gotJSON) != want {
			t.Errorf("Evaluate(%s) marshalled to %s, want %s", template, gotJSON, want)
		}
	}

	// A namespace mixed into text renders the way JavaScript's own toString
	// does rather than as Go's formatting of an unexported struct.
	for template, want := range map[string]any{
		"{{ Math + '' }}":       "[object Math]",
		"{{ JSON + '' }}":       "[object JSON]",
		"{{ 'ns=' + JSON }}":    "ns=[object JSON]",
		"{{ $input + '' }}":     ports,
		"{{ 'ports=' + $env }}": `ports={"REGION":"eu-west-1"}`,
	} {
		got := evaluateOne(t, template, ctx)
		if got != want {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}

	// A function is not a parameter value, and a lineage refusal is not an
	// item: both fail with the reason instead of being embedded and dropped.
	for name, template := range map[string]string{
		"a bare closure":      "{{ (x => x) }}",
		"a closure in a map":  "{{ {f: (x => x)} }}",
		"a closure in a list": "{{ [(x => x)] }}",
	} {
		_, err := expression.Evaluate(template, ctx)
		if err == nil {
			t.Errorf("%s: Evaluate(%s) succeeded, want a refusal", name, template)
			continue
		}
		if !strings.Contains(err.Error(), "function") {
			t.Errorf("%s: error = %v, want it to say a function is not a value", name, err)
		}
	}
	for name, template := range map[string]string{
		"a refusal in a list": `{{ [$('Many').item] }}`,
		"a refusal in a map":  `{{ {x: $('Many').item} }}`,
		"a refusal in text":   `{{ $('Many').item + '' }}`,
		"a refused read":      `{{ $('Many').item.json.n }}`,
	} {
		_, err := expression.Evaluate(template, nodeContext())
		if err == nil {
			t.Errorf("%s: Evaluate(%s) succeeded, want the lineage refusal", name, template)
			continue
		}
		if !strings.Contains(err.Error(), "3 items") {
			t.Errorf("%s: error = %v, want it to explain why there is no single item", name, err)
		}
	}

	// The node object itself is readable — only `.item` refuses — and the
	// refusal marker inside it is not part of the value, which is what n8n's
	// non-enumerable getter amounts to.
	node := evaluateOne(t, `{{ $('Many') }}`, nodeContext())
	nodeJSON, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshalling the node object failed: %v", err)
	}
	if !strings.Contains(string(nodeJSON), `"all"`) || strings.Contains(string(nodeJSON), "produced 3 items") {
		t.Errorf("$('Many') = %s, want the node view without the refusal marker", nodeJSON)
	}
}

// TestDelimiterScanUnderstandsNestedLiterals is the JSON-body spelling: the
// splitter cut the body at the first `}}` without knowing about strings,
// templates or object literals, so any valid body whose last nested literal
// ended in `}}` was truncated before the quote-aware parser ever ran — and the
// user-visible message blamed the expression rather than the delimiter.
func TestDelimiterScanUnderstandsNestedLiterals(t *testing.T) {
	t.Parallel()

	for template, want := range map[string]any{
		// The two spellings the ticket's JSON-body use case needs.
		"{{ JSON.stringify({a:{b:1}}) }}": `{"a":{"b":1}}`,
		`{{ JSON.parse('{"a":{"b":1}}') }}`: map[string]any{
			"a": map[string]any{"b": float64(1)},
		},
		"{{ JSON.stringify({body: {text: $json.name}}) }}": `{"body":{"text":"Ada Lovelace"}}`,
		"{{ {a:{b:{c:1}}}.a.b.c }}":                        float64(1),
		"{{ JSON.stringify({a:{b:1}, c:[1,{d:2}]}) }}":     `{"a":{"b":1},"c":[1,{"d":2}]}`,
		// A literal that contains the closer, in each quoting style.
		"{{ '}}' }}":          "}}",
		`{{ "}}" }}`:          "}}",
		"{{ `x}}y` }}":        "x}}y",
		"{{ 'a}}' + '}}b' }}": "a}}}}b",
		"{{ `a ${'}'} b` }}":  "a } b",
		// A closing brace inside a template's interpolation is the
		// interpolation's, not the expression's.
		"{{ `v=${ {b:1}.b }` }}": "v=1",
		// Text after the body is still text.
		"pre {{ JSON.stringify({a:{b:1}}) }} post": `pre {"a":{"b":1}} post`,
	} {
		got := evaluateOne(t, template, parityContext())
		if !sameValue(got, want) {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}

	// A body that really is unterminated still fails, naming the delimiter.
	if _, err := expression.Evaluate("{{ $json.name }", parityContext()); err == nil {
		t.Error("an unterminated expression was accepted")
	} else if !strings.Contains(err.Error(), "}}") {
		t.Errorf("error = %v, want it to name the missing closing braces", err)
	}
}

// TestAbstractEqualityCoercesBooleansAndCollections is the wrong-branch
// finding: `==` handled nullish, text/text and text/non-text, then fell through
// to strict equality, so a flag compared against 1 and a collection compared
// against false were both silently false where JavaScript says true.
//
// A collection coerces through the same rendering the rest of the package
// uses — a list joins with commas, which is exactly Array.prototype.join, and
// an object is its JSON text, which is the documented rendering this runtime
// gives an object inside a string rather than Go's map syntax.
func TestAbstractEqualityCoercesBooleansAndCollections(t *testing.T) {
	t.Parallel()

	ctx := parityContext()
	ctx.JSON = map[string]any{
		"active":  true,
		"off":     false,
		"count":   float64(42),
		"zero":    float64(0),
		"empty":   []any{},
		"pair":    []any{float64(1), float64(2)},
		"blank":   "",
		"text42":  "42",
		"missing": nil,
	}

	for template, want := range map[string]any{
		// A boolean is a number in an abstract comparison.
		"{{ true == 1 }}":          true,
		"{{ true == '1' }}":        true,
		"{{ 1 == true }}":          true,
		"{{ false == 0 }}":         true,
		"{{ $json.active == 1 }}":  true,
		"{{ $json.active != 1 }}":  false,
		"{{ $json.off == 0 }}":     true,
		"{{ $json.active === 1 }}": false,
		// A collection becomes a primitive: an array joins, an object is text.
		"{{ [] == false }}":            true,
		"{{ [] == 0 }}":                true,
		"{{ [] == '' }}":               true,
		"{{ [1,2] == '1,2' }}":         true,
		"{{ [1,2] != '1,2' }}":         false,
		"{{ ({a:1}) == '{\"a\":1}' }}": true,
		"{{ $json.empty == false }}":   true,
		"{{ $json.pair == '1,2' }}":    true,
		// The coercions that already worked keep working.
		"{{ $json.count == '42' }}": true,
		"{{ $json.blank == 0 }}":    true,
		"{{ '' == 0 }}":             true,
		"{{ $json.count == 42 }}":   true,
		"{{ $json.text42 == 42 }}":  true,
		// null is not 0, and an absent field is not an empty string.
		"{{ null == 0 }}":                  false,
		"{{ $json.missing == 0 }}":         false,
		"{{ $json.missing == '' }}":        false,
		"{{ $json.missing == null }}":      true,
		"{{ $json.missing == undefined }}": true,
	} {
		got := evaluateOne(t, template, ctx)
		if got != want {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}
}

// TestObjectKeysOrderListIndicesNumerically is the silent reordering finding:
// needObject turns a list into a map keyed by index and the keys were sorted as
// text, so Object.values of an eleven-element list came back permuted —
// [0,1,10,2,…] — and Object.entries(...)[10] was entry 9. A workflow that walks
// rows through one of these reorders its data with no error.
func TestObjectKeysOrderListIndicesNumerically(t *testing.T) {
	t.Parallel()

	rows := make([]any, 11)
	for index := range rows {
		rows[index] = float64(index)
	}
	ctx := parityContext()
	ctx.JSON = map[string]any{"rows": rows, "profile": map[string]any{"city": "London", "active": true}}

	inOrder := make([]any, 11)
	keys := make([]any, 11)
	entries := make([]any, 11)
	for index := range inOrder {
		inOrder[index] = float64(index)
		keys[index] = strconv.Itoa(index)
		entries[index] = []any{strconv.Itoa(index), float64(index)}
	}

	for template, want := range map[string]any{
		"{{ Object.values($json.rows) }}":        inOrder,
		"{{ Object.keys($json.rows) }}":          keys,
		"{{ Object.entries($json.rows) }}":       entries,
		"{{ Object.entries($json.rows)[10] }}":   []any{"10", float64(10)},
		"{{ Object.values($json.rows)[10] }}":    float64(10),
		"{{ Object.values($json.rows).length }}": float64(11),
		// A genuine map still reads in the deterministic sorted order.
		"{{ Object.keys($json.profile) }}":   []any{"active", "city"},
		"{{ Object.values($json.profile) }}": []any{true, "London"},
	} {
		got := evaluateOne(t, template, ctx)
		if !sameValue(got, want) {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}
}

// TestStringifyOmitsUndefinedProperties is the payload finding: normalising
// mapped the undefined sentinel to nil everywhere, so an optional field placed
// in a hand-built API body or prompt was sent as an explicit null — something
// n8n never sends and an upstream may read as a deliberate clear. An array slot
// is the one place JavaScript does write null, and that already worked.
func TestStringifyOmitsUndefinedProperties(t *testing.T) {
	t.Parallel()

	ctx := parityContext()
	ctx.JSON = map[string]any{"name": "Ada", "count": float64(42)}

	for template, want := range map[string]any{
		"{{ JSON.stringify({a: $json.missing, b: 1}) }}":      `{"b":1}`,
		"{{ JSON.stringify({a: undefined, b: undefined}) }}":  `{}`,
		"{{ JSON.stringify({o: {a: $json.missing, b: 2}}) }}": `{"o":{"b":2}}`,
		"{{ JSON.stringify([$json.missing, 1]) }}":            `[null,1]`,
		"{{ JSON.stringify({a: [undefined], b: 1}) }}":        `{"a":[null],"b":1}`,
		// A function is dropped from an object the same way.
		"{{ JSON.stringify({f: (x => x), b: 1}) }}": `{"b":1}`,
		"{{ JSON.stringify([(x => x)]) }}":          `[null]`,
		// The same rule through the n8n extension a prompt is built with.
		"{{ {a: $json.missing, b: 1}.toJsonString() }}":     `{"b":1}`,
		"{{ {a: $json.missing, b: 1}.toJsonString(true) }}": "{\n  \"b\": 1\n}",
	} {
		got := evaluateOne(t, template, ctx)
		if got != want {
			t.Errorf("Evaluate(%s) = %#v, want %#v", template, got, want)
		}
	}

	// The node view's refusal marker is not a property of the node either: n8n
	// keeps `.item` non-enumerable, so stringify never reaches it.
	node := evaluateOne(t, `{{ JSON.stringify($('Many')) }}`, nodeContext())
	text, _ := node.(string)
	if !strings.Contains(text, `"json"`) || !strings.Contains(text, `"all"`) {
		t.Errorf("JSON.stringify($('Many')) = %s, want the node's own fields", text)
	}
	if strings.Contains(text, `"item"`) || strings.Contains(text, "produced 3 items") {
		t.Errorf("JSON.stringify($('Many')) = %s, want the refusal marker left out", text)
	}
}
