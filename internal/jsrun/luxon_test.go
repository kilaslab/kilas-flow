package jsrun_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// Luxon is the vendored bundle, run as it is; these tests hold what it
// computes on top of the runtime's Intl to what the same bundle computes
// under Node 24 (testdata/parity/luxon.json).

// Every probe runs in its own VM, since several change Settings.
func TestLuxonMatchesTheRecordedNodeGoldens(t *testing.T) {
	var golden struct {
		Probes []parityProbe `json:"probes"`
	}
	loadGolden(t, "luxon.json", &golden)
	checkProbes(t, golden.Probes, true)
}

func runInZone(t *testing.T, zone, source string) map[string]any {
	t.Helper()
	result, err := newRunner().Run(context.Background(), jsrun.Task{Source: source, Roots: jsrun.Roots{Timezone: zone}})
	if err != nil {
		t.Fatalf("zone %q: Run() error = %v", zone, err)
	}
	return result.Items[0].JSON
}

// DateTime is in the workflow's zone unless the code says otherwise, as in
// n8n, and a workflow with no zone is in UTC, never the server's own.
func TestDateTimeDefaultsToTheWorkflowTimezone(t *testing.T) {
	source := "const at = DateTime.local(2026, 3, 1, 10, 30)\nreturn [{ json: { iso: at.toISO(), zone: at.zoneName, fromISO: DateTime.fromISO('2026-07-01T12:00:00').offset } }]"
	for zone, want := range map[string]map[string]any{
		"Asia/Jakarta":     {"iso": "2026-03-01T10:30:00.000+07:00", "zone": "Asia/Jakarta", "fromISO": float64(420)},
		"America/New_York": {"iso": "2026-03-01T10:30:00.000-05:00", "zone": "America/New_York", "fromISO": float64(-240)},
		"":                 {"iso": "2026-03-01T10:30:00.000Z", "zone": "UTC", "fromISO": float64(0)},
	} {
		got := runInZone(t, zone, source)
		for key, value := range want {
			if got[key] != value {
				t.Errorf("zone %q: %s = %#v, want %#v", zone, key, got[key], value)
			}
		}
	}
}

// $now and $today are Luxon DateTimes in the workflow's zone, read fresh
// each time: $now is the current instant, $today its midnight.
func TestNowAndTodayAreLuxonInTheWorkflowZone(t *testing.T) {
	before := time.Now()
	got := runInZone(t, "Asia/Jakarta", strings.Join([]string{
		"const first = $now",
		"return [{ json: {",
		"  now: DateTime.isDateTime($now), today: DateTime.isDateTime($today),",
		"  zone: $now.zoneName, todayZone: $today.zoneName, midnight: $today.toFormat('HH:mm:ss.SSS'),",
		"  sameDay: $today.hasSame($now, 'day'), fresh: first !== $now, millis: $now.toMillis(),",
		"} }]",
	}, "\n"))
	after := time.Now()
	for key, want := range map[string]any{"now": true, "today": true, "zone": "Asia/Jakarta", "todayZone": "Asia/Jakarta", "midnight": "00:00:00.000", "sameDay": true, "fresh": true} {
		if got[key] != want {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}
	millis := int64(got["millis"].(float64))
	if millis < before.UnixMilli() || millis > after.UnixMilli() {
		t.Errorf("$now = %d ms, want the time of the run (between %d and %d)", millis, before.UnixMilli(), after.UnixMilli())
	}
}

// Loading Luxon is setup the code is not charged for, when the analysis saw
// the code name it. On a slow machine, and under the race detector, loading
// the bundle alone takes longer than this limit, so the body passes only
// if the load is outside it.
func TestAFiftyMillisecondLimitCoversALuxonBodyUnderRace(t *testing.T) {
	runner := jsrun.NewRunner(jsrun.Options{Limits: jsrun.Limits{Timeout: 50 * time.Millisecond}})
	for run := range 3 {
		result, err := runner.Run(context.Background(), jsrun.Task{
			Source: "return [{ json: { day: DateTime.fromISO('2026-03-01T10:00:00Z').setZone('Asia/Jakarta').toFormat('cccc') } }]",
			Roots:  jsrun.Roots{Timezone: "Asia/Jakarta"},
		})
		if err != nil {
			t.Fatalf("run %d: Run() error = %v", run, err)
		}
		if result.Items[0].JSON["day"] != "Sunday" {
			t.Fatalf("run %d: items = %#v, want Luxon's answer", run, result.Items)
		}
	}
}

// A body that never names Luxon never loads it: the globals stay lazy
// accessors until one is read. Reading one loads it then, configured, and
// the globals become plain values the code may replace.
func TestLuxonLoadsOnlyWhenTheBodyNamesIt(t *testing.T) {
	// The global's name is built at run time, so the analysis cannot see it.
	lazy := "const name = 'Date' + 'Time'\nconst lazy = typeof Object.getOwnPropertyDescriptor(globalThis, name).get === 'function'\n"
	got := runInZone(t, "Europe/Berlin", lazy+strings.Join([]string{
		"const loaded = globalThis[name]",
		"return [{ json: {",
		"  lazy, after: typeof Object.getOwnPropertyDescriptor(globalThis, name).get,",
		"  offset: loaded.fromISO('2026-07-01T12:00:00').offset, same: loaded === globalThis[name],",
		"} }]",
	}, "\n"))
	for key, want := range map[string]any{"lazy": true, "after": "undefined", "offset": float64(120), "same": true} {
		if got[key] != want {
			t.Errorf("lazy load: %s = %#v, want %#v", key, got[key], want)
		}
	}

	named := runInZone(t, "", lazy+"return [{ json: { lazy, now: typeof DateTime.now } }]")
	if named["lazy"] != false || named["now"] != "function" {
		t.Errorf("a body naming DateTime: %#v, want Luxon loaded before it ran", named)
	}

	replaced := runInZone(t, "", strings.Join([]string{
		"const name = 'Dura' + 'tion'",
		"globalThis[name] = 'mine'",
		"return [{ json: { value: globalThis[name] } }]",
	}, "\n"))
	if replaced["value"] != "mine" {
		t.Errorf("assigning a lazy global: %#v, want the code's own value", replaced)
	}
}

// require('luxon') is the configured instance whether the analysis saw it or
// the name was built at run time.
func TestRequireLuxonIsTheConfiguredInstanceHoweverItIsNamed(t *testing.T) {
	for _, source := range []string{
		"const luxon = require('luxon')\nreturn [{ json: { zone: luxon.Settings.defaultZone.name, locale: luxon.Settings.defaultLocale, same: luxon.DateTime === DateTime } }]",
		"const luxon = require('lu' + 'xon')\nreturn [{ json: { zone: luxon.Settings.defaultZone.name, locale: luxon.Settings.defaultLocale, same: luxon.DateTime === globalThis['Date' + 'Time'] } }]",
	} {
		got := runInZone(t, "Asia/Jakarta", source)
		for key, want := range map[string]any{"zone": "Asia/Jakarta", "locale": "en-US", "same": true} {
			if got[key] != want {
				t.Errorf("%q: %s = %#v, want %#v", source, key, got[key], want)
			}
		}
	}
}

// Luxon's own English fallbacks answer English requests, as in Node. A
// request in another language reaches the runtime's Intl, which refuses it by
// name rather than let Luxon answer in English.
func TestLuxonInAnotherLanguageIsANamedError(t *testing.T) {
	for source, want := range map[string]string{
		"return [{ json: { v: DateTime.fromISO('2026-03-01').setLocale('de').toFormat('DDD') } }]":                      "date formatting in locale de is not supported",
		"return [{ json: { v: DateTime.fromISO('2026-03-01').setLocale('id').toLocaleString(DateTime.DATE_FULL) } }]":   "date formatting in locale id is not supported",
		"return [{ json: { v: DateTime.fromISO('2026-03-01').setLocale('de').toRelative({ base: DateTime.now() }) } }]": "in locale de is not supported",
		"return [{ json: { v: new Intl.RelativeTimeFormat('de').format(-2, 'day') } }]":                                 "relative time formatting in locale de is not supported",
		"return [{ json: { v: Duration.fromObject({ hours: 2, minutes: 5 }).toHuman() } }]":                             "the unit style of Intl.NumberFormat is not supported",
		"return [{ json: { v: Interval.fromISO('2026-03-01/2026-03-05').toLocaleString(DateTime.DATE_MED) } }]":         "formatRange is not supported",
	} {
		_, err := runAll(t, newRunner(), source, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: Run() error = %v, want it to name %q", source, err, want)
		}
	}
}
