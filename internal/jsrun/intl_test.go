package jsrun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// The parity goldens in testdata/parity were recorded under Node 24 by
// scripts/js-parity/record.mjs. CI never runs Node: these tests compare the
// runtime against the committed files. A golden is Node's answer, never
// edited to match the runtime; where the runtime deliberately does not match,
// the probe is listed in refusedProbes and must fail with a named error.

// parityProbe is one recorded probe: a body run with a workflow zone, and
// what Node returned or threw.
type parityProbe struct {
	Name  string          `json:"name"`
	Zone  string          `json:"zone"`
	Code  string          `json:"code"`
	Want  json.RawMessage `json:"want"`
	Error string          `json:"error"`
}

func loadGolden(t *testing.T, name string, into any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "parity", name))
	if err != nil {
		t.Fatalf("reading the %s golden: %v", name, err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("decoding the %s golden: %v", name, err)
	}
}

// probeResult is what the runtime returned for one probe, or the error it
// threw as "Name: message".
type probeResult struct {
	Value any    `json:"v"`
	Error string `json:"e"`
}

// probeBody wraps probes into one Code-node body that runs each and returns
// every answer, so a batch costs one VM.
func probeBody(codes []string) string {
	var body strings.Builder
	body.WriteString("const out = []\n")
	for _, code := range codes {
		body.WriteString("out.push((function () { try { const value = (function () {\n")
		body.WriteString(code)
		body.WriteString("\n})(); return { v: value === undefined ? null : value } } catch (error) { return { e: String(error && error.name) + ': ' + String(error && error.message) } } })())\n")
	}
	body.WriteString("return [{ json: { out: JSON.parse(JSON.stringify(out)) } }]")
	return body.String()
}

// runProbes runs probe bodies in one VM with the given workflow zone.
func runProbes(t *testing.T, zone string, codes []string) []probeResult {
	t.Helper()
	result, err := newRunner().Run(context.Background(), jsrun.Task{Source: probeBody(codes), Roots: jsrun.Roots{Timezone: zone}})
	if err != nil {
		t.Fatalf("running %d probes in zone %q: %v", len(codes), zone, err)
	}
	encoded, err := json.Marshal(result.Items[0].JSON["out"])
	if err != nil {
		t.Fatal(err)
	}
	var results []probeResult
	if err := json.Unmarshal(encoded, &results); err != nil {
		t.Fatal(err)
	}
	return results
}

// sameJSON compares two values as JSON documents.
func sameJSON(got any, want json.RawMessage) bool {
	encoded, err := json.Marshal(got)
	if err != nil {
		return false
	}
	var left, right any
	if json.Unmarshal(encoded, &left) != nil || json.Unmarshal(want, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// refusedProbes are recorded probes the runtime deliberately answers with a
// named error instead of Node's value, by the substring the error carries.
var refusedProbes = map[string]string{}

// frameworkDefects are recorded probes that fail because of a defect outside
// the Intl and Luxon modules, by the error they fail with today. Each is a
// bug to fix, not a difference to keep: when the fix lands the probe matches
// Node, this test fails, and the entry must be deleted.
//
// goja passes a constructor's new.target on to every plain call made inside
// it, so runtime.js's RegExp guard, which tells `RegExp(…)` from
// `new RegExp(…)` by new.target, builds a broken RegExp when a constructor
// calls RegExp() plainly. Luxon's format parser does exactly that, so
// DateTime.fromFormat fails until the guard also checks its receiver.
var frameworkDefects = map[string]string{}

// checkProbes runs recorded probes, grouped by zone in batches, and compares
// each answer with Node's.
func checkProbes(t *testing.T, probes []parityProbe, eachInItsOwnVM bool) {
	t.Helper()
	byZone := map[string][]parityProbe{}
	var zones []string
	for _, probe := range probes {
		if _, seen := byZone[probe.Zone]; !seen {
			zones = append(zones, probe.Zone)
		}
		byZone[probe.Zone] = append(byZone[probe.Zone], probe)
	}
	for _, zone := range zones {
		group := byZone[zone]
		size := 25
		if eachInItsOwnVM {
			size = 1
		}
		for start := 0; start < len(group); start += size {
			batch := group[start:min(start+size, len(group))]
			codes := make([]string, len(batch))
			for index, probe := range batch {
				codes[index] = probe.Code
			}
			for index, got := range runProbes(t, zone, codes) {
				compareProbe(t, batch[index], got)
			}
		}
	}
}

func compareProbe(t *testing.T, probe parityProbe, got probeResult) {
	t.Helper()
	if defect, known := frameworkDefects[probe.Name]; known {
		if got.Error != defect {
			t.Errorf("%s: got %s; the framework defect it is listed for looks fixed, so delete its frameworkDefects entry", probe.Name, describeResult(got))
		}
		return
	}
	if refusal, refused := refusedProbes[probe.Name]; refused {
		if got.Error == "" || !strings.Contains(got.Error, refusal) {
			t.Errorf("%s: got %s, want the named error %q", probe.Name, describeResult(got), refusal)
		}
		return
	}
	if probe.Error != "" {
		if got.Error != probe.Error {
			t.Errorf("%s:\n got  %s\n want error %s", probe.Name, describeResult(got), probe.Error)
		}
		return
	}
	if got.Error != "" || !sameJSON(got.Value, probe.Want) {
		t.Errorf("%s:\n got  %s\n want %s", probe.Name, describeResult(got), probe.Want)
	}
}

func describeResult(result probeResult) string {
	if result.Error != "" {
		return "error " + result.Error
	}
	encoded, _ := json.Marshal(result.Value)
	return string(encoded)
}

func TestDatesMatchTheRecordedNodeGoldens(t *testing.T) {
	var golden struct {
		Probes []parityProbe `json:"probes"`
	}
	loadGolden(t, "dates.json", &golden)
	checkProbes(t, golden.Probes, false)
}

// dateSweepGaps are recorded sweep combinations en-GB does not answer as
// Node does, by locale and the exact JSON the options serialise to. Every
// one is a bug to close, not a difference to keep, and each needs an
// hourCycle: 'h24' explicit request (never a locale default) or an unusual
// zone style paired with an asymmetric second width — combinations a real
// Code node has no reason to construct. Two known shapes:
//
//   - hourCycle: 'h24' ("k") has its own padding rule, independent of "H"'s
//     h/km/kms/kmss cover it, but "hour+second" with no minute at all falls
//     through bestAppending's gap-filling template, which picks the wrong
//     field as primary for 'k' specifically ("17 (second: 9)" instead of
//     Node's "9 (hour: 17)").
//   - timeZoneName: 'longOffset' joined to hour+2-digit-minute+second
//     picks a different (padded) candidate than 'short'/'long'/'shortOffset'
//     do (all unpadded, matched by the "Hmsv" entry) — Node's real
//     availableFormats evidently treats the offset zone widths differently
//     again, one width this table does not have a dedicated entry for.
var dateSweepGaps = map[string]bool{}

func init() {
	for _, options := range []string{
		`{"hour":"numeric","hourCycle":"h24","second":"numeric"}`,
		`{"hour":"numeric","hourCycle":"h24","second":"2-digit"}`,
		`{"hour":"2-digit","hourCycle":"h24","second":"2-digit"}`,
		`{"hour":"numeric","hourCycle":"h24","minute":"2-digit","second":"numeric"}`,
		`{"fractionalSecondDigits":1,"hour":"numeric","hourCycle":"h24"}`,
		`{"fractionalSecondDigits":1,"hour":"numeric","hourCycle":"h24","minute":"2-digit"}`,
		`{"fractionalSecondDigits":1,"hour":"numeric","hourCycle":"h24","minute":"2-digit","second":"numeric"}`,
		`{"fractionalSecondDigits":1,"hour":"numeric","hourCycle":"h24","second":"2-digit"}`,
		`{"fractionalSecondDigits":1,"hour":"numeric","hourCycle":"h24","second":"numeric"}`,
		`{"fractionalSecondDigits":1,"hour":"2-digit","hourCycle":"h24","second":"2-digit"}`,
		`{"fractionalSecondDigits":3,"hour":"numeric","hourCycle":"h24"}`,
		`{"fractionalSecondDigits":3,"hour":"numeric","hourCycle":"h24","minute":"2-digit"}`,
		`{"fractionalSecondDigits":3,"hour":"numeric","hourCycle":"h24","minute":"2-digit","second":"numeric"}`,
		`{"fractionalSecondDigits":3,"hour":"numeric","hourCycle":"h24","second":"2-digit"}`,
		`{"fractionalSecondDigits":3,"hour":"numeric","hourCycle":"h24","second":"numeric"}`,
		`{"fractionalSecondDigits":3,"hour":"2-digit","hourCycle":"h24"}`,
		`{"fractionalSecondDigits":3,"hour":"2-digit","hourCycle":"h24","second":"2-digit"}`,
		`{"hour":"numeric","minute":"2-digit","second":"numeric","timeZoneName":"longOffset"}`,
		`{"hour":"numeric","hourCycle":"h23","minute":"2-digit","second":"numeric","timeZoneName":"longOffset"}`,
	} {
		dateSweepGaps["en-GB|"+options] = true
	}
}

// Every combination of the component options in the sweep, formatted at
// three instants, pins the pattern the options produce, in each of the
// three locales the runtime ships (FEAT-9we7kw: en-CA and en-GB alongside
// en-US), except the narrow dateSweepGaps above.
func TestTheDateOptionSweepMatchesNode(t *testing.T) {
	var golden struct {
		Instants []float64 `json:"instants"`
		Cases    []struct {
			Options map[string]any      `json:"options"`
			Want    map[string][]string `json:"want"`
		} `json:"cases"`
	}
	loadGolden(t, "date-options.json", &golden)
	instants, _ := json.Marshal(golden.Instants)
	locales := []string{"en-US", "en-CA", "en-GB"}
	encodedLocales, _ := json.Marshal(locales)
	const chunk = 400
	for start := 0; start < len(golden.Cases); start += chunk {
		cases := golden.Cases[start:min(start+chunk, len(golden.Cases))]
		options := make([]map[string]any, len(cases))
		for index, entry := range cases {
			options[index] = entry.Options
		}
		encoded, _ := json.Marshal(options)
		result := mustRun(t, newRunner(), jsrun.Task{Source: fmt.Sprintf(`const instants = %s
const locales = %s
return [{ json: { out: %s.map(function (options) {
  const byLocale = {}
  locales.forEach(function (locale) {
    try {
      const format = new Intl.DateTimeFormat(locale, Object.assign({ timeZone: 'UTC' }, options))
      byLocale[locale] = instants.map(function (at) { return format.format(at) })
    } catch (error) { byLocale[locale] = [String(error)] }
  })
  return byLocale
}) } }]`, instants, encodedLocales, encoded)})
		for index, got := range result.Items[0].JSON["out"].([]any) {
			byLocale := got.(map[string]any)
			encodedOptions, _ := json.Marshal(cases[index].Options)
			for _, locale := range locales {
				var have []string
				for _, text := range byLocale[locale].([]any) {
					have = append(have, text.(string))
				}
				want := cases[index].Want[locale]
				matches := reflect.DeepEqual(have, want)
				if gap := dateSweepGaps[locale+"|"+string(encodedOptions)]; gap {
					if matches {
						t.Errorf("%s options %v: now matches Node; delete its dateSweepGaps entry", locale, cases[index].Options)
					}
					continue
				}
				if !matches {
					t.Errorf("%s options %v:\n got  %q\n want %q", locale, cases[index].Options, have, want)
				}
			}
		}
	}
}

// Every zone Go's tz database carries answers with Node's identifier,
// offset and names, in winter and in summer.
func TestZoneNamesMatchNodeForEveryZone(t *testing.T) {
	type moment struct {
		Offset      float64 `json:"offset"`
		Short       string  `json:"short"`
		Long        string  `json:"long"`
		ShortOffset string  `json:"shortOffset"`
		LongOffset  string  `json:"longOffset"`
	}
	type zone struct {
		Canonical string `json:"canonical"`
		January   moment `json:"january"`
		July      moment `json:"july"`
	}
	var golden struct {
		January float64         `json:"january"`
		July    float64         `json:"july"`
		Zones   map[string]zone `json:"zones"`
	}
	loadGolden(t, "zones.json", &golden)
	var names []string
	for name := range golden.Zones {
		names = append(names, name)
	}
	const chunk = 150
	for start := 0; start < len(names); start += chunk {
		batch := names[start:min(start+chunk, len(names))]
		encoded, _ := json.Marshal(batch)
		result := mustRun(t, newRunner(), jsrun.Task{Source: fmt.Sprintf(`const at = [%v, %v]
function name(instant, timeZone, style) {
  return new Intl.DateTimeFormat('en-US', { timeZone, timeZoneName: style }).formatToParts(instant).find(function (part) { return part.type === 'timeZoneName' }).value
}
function offset(instant, timeZone) {
  const parts = {}
  new Intl.DateTimeFormat('en-US', { timeZone, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric' }).formatToParts(instant).forEach(function (part) { parts[part.type] = part.value })
  return Math.round((Date.UTC(+parts.year, parts.month - 1, +parts.day, +parts.hour, +parts.minute, +parts.second) - Math.floor(instant / 1000) * 1000) / 60000)
}
return [{ json: { out: %s.map(function (zone) {
  try {
    const moments = at.map(function (instant) { return { offset: offset(instant, zone), short: name(instant, zone, 'short'), long: name(instant, zone, 'long'), shortOffset: name(instant, zone, 'shortOffset'), longOffset: name(instant, zone, 'longOffset') } })
    return { canonical: new Intl.DateTimeFormat('en-US', { timeZone: zone }).resolvedOptions().timeZone, january: moments[0], july: moments[1] }
  } catch (error) { return { canonical: String(error) } }
}) } }]`, golden.January, golden.July, encoded)})
		for index, got := range result.Items[0].JSON["out"].([]any) {
			encodedGot, _ := json.Marshal(got)
			var have zone
			_ = json.Unmarshal(encodedGot, &have)
			if want := golden.Zones[batch[index]]; have != want {
				t.Errorf("%s:\n got  %+v\n want %+v", batch[index], have, want)
			}
		}
	}
}

func TestNumbersMatchTheRecordedNodeGoldens(t *testing.T) {
	var golden struct {
		Probes []parityProbe `json:"probes"`
	}
	loadGolden(t, "numbers.json", &golden)
	checkProbes(t, golden.Probes, false)
}

func TestCollationMatchesTheRecordedNodeGoldens(t *testing.T) {
	var golden struct {
		Probes []parityProbe `json:"probes"`
	}
	loadGolden(t, "collation.json", &golden)
	checkProbes(t, golden.Probes, false)
}

// Currencies format as Node formats them in every locale the runtime has a
// currency pattern for: symbol, placement and spacing. A locale it has none
// for is refused by name, never given another locale's pattern.
func TestCurrenciesMatchNodeWhereverTheyAreFormatted(t *testing.T) {
	var golden struct {
		Codes      []string `json:"currencyCodes"`
		Currencies []struct {
			Locale   string   `json:"locale"`
			Want     []string `json:"want"`
			Positive string   `json:"positive"`
		} `json:"currencies"`
	}
	loadGolden(t, "numbers.json", &golden)
	encodedCodes, _ := json.Marshal(golden.Codes)
	refused := func(text string) bool { return strings.Contains(text, "RangeError: currency formatting in locale") }
	const chunk = 13
	for start := 0; start < len(golden.Currencies); start += chunk {
		batch := golden.Currencies[start:min(start+chunk, len(golden.Currencies))]
		var locales []string
		for _, entry := range batch {
			locales = append(locales, entry.Locale)
		}
		encodedLocales, _ := json.Marshal(locales)
		result := mustRun(t, newRunner(), jsrun.Task{Source: fmt.Sprintf(`function format(locale, currency, currencyDisplay, value) {
  try { return new Intl.NumberFormat(locale, { style: 'currency', currency, currencyDisplay }).format(value) } catch (error) { return String(error) }
}
return [{ json: { out: %s.map(function (locale) {
  return {
    negative: %s.map(function (currency) {
      return ['symbol', 'narrowSymbol', 'code'].map(function (display) { return format(locale, currency, display, -1234.5) }).join(' | ')
    }),
    positive: ['USD', 'EUR'].map(function (currency) { return format(locale, currency, 'symbol', 1234567.5) }).join(' | '),
  }
}) } }]`, encodedLocales, encodedCodes)})
		for index, got := range result.Items[0].JSON["out"].([]any) {
			entry, recorded := got.(map[string]any), batch[index]
			if have := entry["positive"].(string); !refused(have) && have != recorded.Positive {
				t.Errorf("%s positive:\n got  %+q\n want %+q", recorded.Locale, have, recorded.Positive)
			}
			for position, value := range entry["negative"].([]any) {
				have, want := value.(string), recorded.Want[position]
				if !refused(have) && have != want {
					t.Errorf("%s %s:\n got  %+q\n want %+q", recorded.Locale, golden.Codes[position], have, want)
				}
			}
		}
	}
}

// Every currency Node knows shows Node's symbol and narrow symbol in every
// locale currencies are formatted in, and Node's default fraction digits.
func TestEveryCurrencyHasNodesSymbolsAndDigits(t *testing.T) {
	var golden struct {
		Codes   []string            `json:"codes"`
		Digits  []float64           `json:"digits"`
		Locales map[string][]string `json:"locales"`
	}
	loadGolden(t, "currencies.json", &golden)
	codes, _ := json.Marshal(golden.Codes)
	digits := mustRun(t, newRunner(), jsrun.Task{Source: fmt.Sprintf(`return [{ json: { digits: %s.map(function (currency) {
  return new Intl.NumberFormat('en', { style: 'currency', currency }).resolvedOptions().maximumFractionDigits
}) } }]`, codes)})
	for index, got := range digits.Items[0].JSON["digits"].([]any) {
		if got != golden.Digits[index] {
			t.Errorf("%s: %v fraction digits, want %v", golden.Codes[index], got, golden.Digits[index])
		}
	}
	var locales []string
	for locale := range golden.Locales {
		locales = append(locales, locale)
	}
	// A few locales per run keeps each run well inside the time limit under
	// the race detector.
	const chunk = 4
	for start := 0; start < len(locales); start += chunk {
		batch := locales[start:min(start+chunk, len(locales))]
		encodedLocales, _ := json.Marshal(batch)
		result := mustRun(t, newRunner(), jsrun.Task{Source: fmt.Sprintf(`const codes = %s
function symbol(locale, currency, currencyDisplay) {
  return new Intl.NumberFormat(locale, { style: 'currency', currency, currencyDisplay }).formatToParts(1).find(function (part) { return part.type === 'currency' }).value
}
return [{ json: { symbols: %s.map(function (locale) {
  return codes.map(function (currency) { return symbol(locale, currency, 'symbol') + '|' + symbol(locale, currency, 'narrowSymbol') })
}) } }]`, codes, encodedLocales)})
		for index, got := range result.Items[0].JSON["symbols"].([]any) {
			for position, value := range got.([]any) {
				if want := golden.Locales[batch[index]][position]; value != want {
					t.Errorf("%s %s: symbols %q, want %q", batch[index], golden.Codes[position], value, want)
				}
			}
		}
	}
}

// Intl.Locale's week information, which Luxon's locale weeks read, is
// Node's for every region, and for languages named without one.
func TestWeekInformationMatchesNodeForEveryRegion(t *testing.T) {
	var golden struct {
		Regions   map[string]string `json:"regions"`
		Languages map[string]string `json:"languages"`
	}
	loadGolden(t, "weeks.json", &golden)
	want := map[string]string{}
	for region, info := range golden.Regions {
		want["und-"+region] = info
	}
	for language, info := range golden.Languages {
		want[language] = info
	}
	var tags []string
	for tag := range want {
		tags = append(tags, tag)
	}
	encoded, _ := json.Marshal(tags)
	result := mustRun(t, newRunner(), jsrun.Task{Source: fmt.Sprintf(`return [{ json: { out: %s.map(function (tag) {
  try {
    const info = new Intl.Locale(tag).getWeekInfo()
    return info.firstDay + '|' + info.weekend.join(',')
  } catch (error) { return String(error) }
}) } }]`, encoded)})
	for index, got := range result.Items[0].JSON["out"].([]any) {
		if got != want[tags[index]] {
			t.Errorf("%s: week %v, want %v", tags[index], got, want[tags[index]])
		}
	}
}

// Numbers format in any locale x/text knows. The sweep pins the separators,
// grouping, digits and percent sign of a hundred-odd locales against Node.
func TestNumbersFormatInAnyCLDRLocale(t *testing.T) {
	var golden struct {
		Locales []struct {
			Locale string   `json:"locale"`
			Want   []string `json:"want"`
		} `json:"locales"`
	}
	loadGolden(t, "numbers.json", &golden)
	var locales []string
	for _, entry := range golden.Locales {
		locales = append(locales, entry.Locale)
	}
	encoded, _ := json.Marshal(locales)
	result := mustRun(t, newRunner(), jsrun.Task{Source: fmt.Sprintf(`return [{ json: { out: %s.map(function (locale) {
  try {
    return [
      new Intl.NumberFormat(locale).format(1234567.891),
      new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(1234.5),
      new Intl.NumberFormat(locale, { style: 'percent' }).format(0.256),
      new Intl.NumberFormat(locale).format(-1234.5),
      new Intl.NumberFormat(locale).format(1234),
      new Intl.NumberFormat(locale).format(1234567890.5),
      new Intl.NumberFormat(locale, { style: 'percent' }).format(-0.5),
    ]
  } catch (error) { return [String(error)] }
}) } }]`, encoded)})
	for index, got := range result.Items[0].JSON["out"].([]any) {
		var have []string
		for _, text := range got.([]any) {
			have = append(have, text.(string))
		}
		if want := golden.Locales[index].Want; !reflect.DeepEqual(have, want) {
			t.Errorf("%s:\n got  %+q\n want %+q", locales[index], have, want)
		}
	}
}

// The replaced locale methods look like the built-ins they replace: the same
// name, length and property attributes, as Node reports them.
func TestTheLocaleMethodsKeepTheirShape(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"const shape = (owner, key) => { const d = Object.getOwnPropertyDescriptor(owner, key); return [d.value.name, d.value.length, d.writable, d.enumerable, d.configurable].join(',') }",
		"return [{ json: {",
		"  methods: [shape(Number.prototype, 'toLocaleString'), shape(Date.prototype, 'toLocaleString'), shape(Date.prototype, 'toLocaleDateString'), shape(Date.prototype, 'toLocaleTimeString'), shape(String.prototype, 'localeCompare'), shape(BigInt.prototype, 'toLocaleString')].join(' '),",
		"  intl: [Intl.DateTimeFormat.length, Intl.NumberFormat.length, Intl.DateTimeFormat.name, Object.prototype.toString.call(Intl), Object.prototype.toString.call(new Intl.NumberFormat()), Intl.DateTimeFormat.prototype.formatToParts.length, Intl.NumberFormat.prototype.formatToParts.length].join(' '),",
		"} }]",
	}, "\n")})
	got := result.Items[0].JSON
	if want := "toLocaleString,0,true,false,true toLocaleString,0,true,false,true toLocaleDateString,0,true,false,true toLocaleTimeString,0,true,false,true localeCompare,1,true,false,true toLocaleString,0,true,false,true"; got["methods"] != want {
		t.Errorf("methods = %v, want %v", got["methods"], want)
	}
	if want := "0 0 DateTimeFormat [object Intl] [object Intl.NumberFormat] 1 1"; got["intl"] != want {
		t.Errorf("Intl = %v, want %v", got["intl"], want)
	}
}

// errorOf runs a body that must fail and returns its error.
func errorOf(t *testing.T, source string) error {
	t.Helper()
	_, err := runAll(t, newRunner(), source, nil)
	if err == nil {
		t.Fatalf("%q: Run() succeeded, want an error", source)
	}
	return err
}

// Dates format in en-US, en-CA or en-GB only. A request for another locale
// is refused by name, never answered in English, or in one of the three
// shipped English locales' layout; English as written in Australia (en-AU
// writes the day first too, like en-GB, but has its own CLDR data) is
// another locale too.
func TestNonEnglishDateFormattingIsANamedError(t *testing.T) {
	for source, locale := range map[string]string{
		"return [{ json: { v: new Date(0).toLocaleDateString('de-DE') } }]":                                  "de-DE",
		"return [{ json: { v: new Date(0).toLocaleString('id-ID', { timeZone: 'Asia/Jakarta' }) } }]":        "id-ID",
		"return [{ json: { v: new Date(0).toLocaleTimeString(['fr-FR', 'en-US']) } }]":                       "fr-FR",
		"return [{ json: { v: new Intl.DateTimeFormat('en-AU').format(0) } }]":                               "en-AU",
		"return [{ json: { v: new Intl.DateTimeFormat('ja-JP', { dateStyle: 'full' }).formatToParts(0) } }]": "ja-JP",
	} {
		err := errorOf(t, source)
		if want := "RangeError: date formatting in locale " + locale + " is not supported"; !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error = %v, want %q", source, err, want)
		}
	}
	got := mustRun(t, newRunner(), jsrun.Task{Source: "return [{ json: { us: new Date(0).toLocaleDateString('en-US', { timeZone: 'UTC' }), en: new Date(0).toLocaleDateString('en', { timeZone: 'UTC' }), supported: Intl.DateTimeFormat.supportedLocalesOf(['de-DE', 'en-US', 'en-CA', 'en-GB', 'en', 'en-AU']) } }]"}).Items[0].JSON
	if got["us"] != "1/1/1970" || got["en"] != "1/1/1970" || fmt.Sprint(got["supported"]) != "[en-US en-CA en-GB en]" {
		t.Errorf("English dates = %#v, want en, en-US, en-CA and en-GB formatted and reported as the only supported locales", got)
	}
}

// Called without arguments, the locale methods speak en-US, whatever the
// server's own locale; dates are in the workflow's zone.
func TestToLocaleStringWithoutArgumentsIsEnUS(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{
		Source: "const at = new Date(Date.UTC(2026, 2, 1, 17, 5, 9))\nreturn [{ json: { number: (1234567.891).toLocaleString(), negative: (-0.5).toLocaleString(), big: (12345678901234567890n).toLocaleString(), date: at.toLocaleString(), day: at.toLocaleDateString(), time: at.toLocaleTimeString(), list: [1234.5, at].toLocaleString() } }]",
		Roots:  jsrun.Roots{Timezone: "Asia/Jakarta"},
	})
	got := result.Items[0].JSON
	for key, want := range map[string]string{
		"number": "1,234,567.891", "negative": "-0.5", "big": "12,345,678,901,234,567,890",
		"date": "3/2/2026, 12:05:09 AM", "day": "3/2/2026", "time": "12:05:09 AM", "list": "1,234.5,3/2/2026, 12:05:09 AM",
	} {
		if got[key] != want {
			t.Errorf("%s = %#v, want %q", key, got[key], want)
		}
	}
}

// localeCompare honours the locale and the numeric and sensitivity options,
// and refuses by name the options x/text's collation cannot honour.
func TestLocaleCompareHonoursOrRefusesItsOptions(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"return [{ json: {",
		"  natural: ['item10', 'item9', 'item1'].sort((a, b) => a.localeCompare(b, undefined, { numeric: true })),",
		"  plain: ['item10', 'item9', 'item1'].sort((a, b) => a.localeCompare(b)),",
		"  base: 'résumé'.localeCompare('RESUME', 'en', { sensitivity: 'base' }),",
		"  accent: 'résumé'.localeCompare('RESUME', 'en', { sensitivity: 'accent' }),",
		"  swedish: 'ä'.localeCompare('z', 'sv'), german: 'ä'.localeCompare('z', 'de'),",
		"} }]",
	}, "\n")})
	got := result.Items[0].JSON
	for key, want := range map[string]string{
		"natural": "[item1 item9 item10]", "plain": "[item1 item10 item9]", "base": "0", "accent": "1", "swedish": "1", "german": "-1",
	} {
		if fmt.Sprint(got[key]) != want {
			t.Errorf("%s = %v, want %s", key, got[key], want)
		}
	}
	for option, want := range map[string]string{
		"{ ignorePunctuation: true }": "ignorePunctuation is not supported",
		"{ caseFirst: 'upper' }":      "caseFirst upper is not supported",
		"{ usage: 'search' }":         "collation usage search is not supported",
		"{ sensitivity: 'loose' }":    "Value loose out of range for Intl.Collator options property sensitivity",
	} {
		err := errorOf(t, "return [{ json: { v: 'a'.localeCompare('b', 'en', "+option+") } }]")
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v, want %q", option, err, want)
		}
	}
}

// A number option the runtime cannot honour is refused by name, never
// formatted some other way.
func TestNumberOptionsThatCannotBeHonouredAreNamedErrors(t *testing.T) {
	for options, want := range map[string]string{
		"'en-US', { style: 'currency', currency: 'USD', currencyDisplay: 'name' }":    "currencyDisplay name is not supported",
		"'en-US', { style: 'currency', currency: 'USD', currencySign: 'accounting' }": "currencySign accounting is not supported",
		"'en-US', { style: 'unit', unit: 'kilometer' }":                               "the unit style of Intl.NumberFormat is not supported",
		"'en-US', { notation: 'compact' }":                                            "notation compact is not supported",
		"'en-US', { roundingIncrement: 5, maximumFractionDigits: 2 }":                 "roundingIncrement 5 is not supported",
		"'en-US', { roundingPriority: 'morePrecision' }":                              "roundingPriority morePrecision is not supported",
		"'en-US', { numberingSystem: 'arab' }":                                        "the numbering system arab is not supported",
		"'sw-KE', { style: 'currency', currency: 'KES' }":                             "currency formatting in locale sw-KE is not supported",
		"'en-US', { style: 'currency', currency: 'DEM' }":                             "the currency DEM is not supported",
	} {
		err := errorOf(t, "return [{ json: { v: new Intl.NumberFormat("+options+").format(1) } }]")
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v, want %q", options, err, want)
		}
	}
}
