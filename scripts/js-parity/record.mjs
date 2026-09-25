/**
 * Records the Node.js parity goldens for the embedded JavaScript runtime.
 *
 * DEV-ONLY. CI never runs Node: it compares internal/jsrun against the JSON
 * files this script writes under internal/jsrun/testdata/parity/, which are
 * committed. Run it by hand, with Node 24, only when a probe is added or the
 * vendored Luxon changes, and review the diff like any other code change:
 *
 *   node scripts/js-parity/record.mjs            # rewrite the goldens
 *   node scripts/js-parity/record.mjs --check    # report drift, write nothing
 *
 * Every Luxon probe runs the SAME vendored bundle the runtime embeds,
 * third_party/luxon/luxon.min.js, in a fresh vm context configured the way
 * the runtime configures it: the workflow's zone as Settings.defaultZone and
 * as the process time zone (so a bare Date or Intl call sees it too), and
 * en-US as the default locale.
 *
 * The script also regenerates the time-zone table embedded in
 * internal/jsrun/intl.go (between its BEGIN/END markers): the zone names Go's
 * own tz database carries, the identifier ICU reports for each, and the
 * English names Node prints for it in 2026. That table is data about the
 * zones, recorded from Node's observable output; no ICU or V8 source is read.
 */

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const out = path.join(root, 'internal/jsrun/testdata/parity');
const intlGo = path.join(root, 'internal/jsrun/intl.go');
const luxonSource = fs.readFileSync(path.join(root, 'third_party/luxon/luxon.min.js'), 'utf8');
const check = process.argv.includes('--check');

if (!process.versions.node.startsWith('24.')) {
	throw new Error(`record.mjs: run this with Node 24 (found ${process.versions.node})`);
}

// ---- Running a probe --------------------------------------------------------

// run evaluates one probe body in a fresh context whose clock zone is zone,
// with Luxon loaded and configured as the runtime configures it.
function run(zone, code) {
	process.env.TZ = zone || 'UTC';
	const context = vm.createContext({});
	const luxon = vm.runInContext(luxonSource + '\n;luxon', context);
	luxon.Settings.defaultZone = zone || 'UTC';
	luxon.Settings.defaultLocale = 'en-US';
	for (const name of ['DateTime', 'Duration', 'Interval', 'Info', 'Settings']) context[name] = luxon[name];
	Object.defineProperty(context, '$now', { get: () => luxon.DateTime.now() });
	Object.defineProperty(context, '$today', { get: () => luxon.DateTime.now().startOf('day') });
	context.require = (name) => {
		if (name === 'luxon') return luxon;
		throw new Error('record.mjs: no module ' + name);
	};
	try {
		const value = vm.runInContext('(function () {\n' + code + '\n})()', context);
		return { want: value === undefined ? null : JSON.parse(JSON.stringify(value)) };
	} catch (error) {
		return { error: String(error.name) + ': ' + String(error.message) };
	}
}

function record(probes) {
	return probes.map((probe) => ({ name: probe.name, zone: probe.zone || '', code: probe.code, ...run(probe.zone, probe.code) }));
}

// ---- Luxon ------------------------------------------------------------------

const presets = [
	'DATE_SHORT', 'DATE_MED', 'DATE_MED_WITH_WEEKDAY', 'DATE_FULL', 'DATE_HUGE',
	'TIME_SIMPLE', 'TIME_WITH_SECONDS', 'TIME_WITH_SHORT_OFFSET', 'TIME_WITH_LONG_OFFSET',
	'TIME_24_SIMPLE', 'TIME_24_WITH_SECONDS', 'TIME_24_WITH_SHORT_OFFSET', 'TIME_24_WITH_LONG_OFFSET',
	'DATETIME_SHORT', 'DATETIME_MED', 'DATETIME_MED_WITH_WEEKDAY', 'DATETIME_FULL', 'DATETIME_HUGE',
	'DATETIME_SHORT_WITH_SECONDS', 'DATETIME_MED_WITH_SECONDS', 'DATETIME_FULL_WITH_SECONDS', 'DATETIME_HUGE_WITH_SECONDS',
];

const tokens = 'G GG GGGGG y yy yyyy yyyyyy u uu uuu L LL LLL LLLL LLLLL M MM MMM MMMM MMMMM d dd o ooo E EEE EEEE EEEEE c ccc cccc ccccc ' +
	'q qq Q QQ W WW kk kkkk n nn ii iiii a h hh H HH m mm s ss S SSS u X x Z ZZ ZZZ ZZZZ ZZZZZ z';
const macros = 'D DD DDD DDDD t tt ttt tttt T TT TTT TTTT f ff fff ffff F FF FFF FFFF';

const luxonProbes = [
	// The spike's probes (EPIC-tjnr1z, js-runtime-spike-results.md).
	{ name: 'spike: fromISO in utc, toFormat', code: "return DateTime.fromISO('2026-03-01T10:00:00Z', { zone: 'utc' }).toFormat('yyyy-LL-dd HH:mm')" },
	{ name: 'spike: setZone Asia/Jakarta, toISO', code: "return DateTime.fromISO('2026-03-01T10:00:00Z').setZone('Asia/Jakarta').toISO()" },
	{ name: 'spike: defaultZone Europe/Berlin, July and January offsets', code: "Settings.defaultZone = 'Europe/Berlin'\nreturn DateTime.fromISO('2026-07-01T12:00:00').offset + ' ' + DateTime.fromISO('2026-01-01T12:00:00').offset" },
	{ name: 'spike: toFormat DDD', code: "return DateTime.fromISO('2026-03-01T10:05:00Z', { zone: 'utc' }).toFormat('DDD')" },
	{ name: 'spike: toFormat cccc, LLLL d', code: "return DateTime.fromISO('2026-03-01T10:05:00Z', { zone: 'utc' }).toFormat('cccc, LLLL d')" },
	{ name: 'spike: setZone America/New_York, toFormat D t ZZZZ', code: "return DateTime.fromISO('2026-03-01T10:05:00Z').setZone('America/New_York').toFormat('D t ZZZZ')" },
	{ name: 'spike: toLocaleString DATETIME_MED', code: "return DateTime.fromISO('2026-03-01T10:05:00Z').setZone('America/New_York').toLocaleString(DateTime.DATETIME_MED)" },
	{ name: 'spike: plus 7 days, diff in hours', code: "const start = DateTime.fromISO('2026-03-01T10:00:00Z')\nreturn start.plus({ days: 7 }).diff(start, 'hours').hours + 'h'" },
	{ name: 'spike: toRelative', code: "Settings.now = () => Date.parse('2026-03-03T10:00:00Z')\nreturn DateTime.fromISO('2026-03-01T10:00:00Z').toRelative()" },
	{ name: "spike: 'x' + DateTime uses valueOf", code: "const at = DateTime.fromMillis(0)\nreturn [typeof ('x' + at), 'x' + at, +at, at.valueOf()]" },

	// The workflow zone is the default.
	{ name: 'the workflow zone is the default zone', zone: 'Asia/Jakarta', code: "const at = DateTime.fromISO('2026-03-01T10:00:00')\nreturn [at.zoneName, at.offset, at.toISO(), Settings.defaultZone.name, Settings.defaultLocale]" },
	{ name: 'an empty workflow zone is UTC', code: "const at = DateTime.fromISO('2026-03-01T10:00:00')\nreturn [at.zoneName, at.toISO(), DateTime.local(2026, 3, 1).toISO()]" },
	{ name: '$now and $today in the workflow zone', zone: 'Asia/Jakarta', code: "Settings.now = () => Date.parse('2026-03-01T20:00:00Z')\nreturn [$now.toISO(), $today.toISO(), $now.zoneName, $today.hour, $now.isValid]" },
	{ name: '$now and $today in America/New_York', zone: 'America/New_York', code: "Settings.now = () => Date.parse('2026-03-08T12:00:00Z')\nreturn [$now.toISO(), $today.toISO(), $now.offsetNameShort]" },
	{ name: "require('luxon') is the configured instance", zone: 'Europe/Berlin', code: "const luxon = require('luxon')\nreturn [luxon.DateTime === DateTime, luxon.Settings.defaultZone.name, luxon.DateTime.fromISO('2026-07-01T12:00:00').offset]" },
	{ name: 'JS Date defaults to the workflow zone when formatted', zone: 'Asia/Jakarta', code: "const at = new Date(Date.UTC(2026, 2, 1, 10, 5, 9))\nreturn [at.toLocaleString(), at.toLocaleDateString(), at.toLocaleTimeString(), DateTime.fromJSDate(at).toISO()]" },

	// Daylight-saving transitions.
	{ name: 'New York offsets around the March 2026 switch', code: "return ['2026-03-08T01:59:59', '2026-03-08T03:00:00', '2026-03-07T12:00:00', '2026-03-09T12:00:00'].map(text => DateTime.fromISO(text, { zone: 'America/New_York' }).offset)" },
	{ name: 'New York: a time in the March gap moves forward', code: "return DateTime.fromISO('2026-03-08T02:30:00', { zone: 'America/New_York' }).toISO()" },
	{ name: 'New York: an hour added across the March gap', code: "const before = DateTime.fromISO('2026-03-08T01:30:00', { zone: 'America/New_York' })\nreturn [before.plus({ hours: 1 }).toISO(), before.plus({ hours: 2 }).toISO(), before.plus({ days: 1 }).toISO(), before.plus({ minutes: 90 }).toISO()]" },
	{ name: 'New York: the day of the March switch is 23 hours', code: "const day = DateTime.fromISO('2026-03-08T12:00:00', { zone: 'America/New_York' })\nreturn [day.endOf('day').diff(day.startOf('day'), 'hours').hours, day.startOf('day').toISO(), day.endOf('day').toISO(), day.isInDST, day.minus({ days: 1 }).isInDST]" },
	{ name: 'New York: the ambiguous November hour takes the first offset', code: "const at = DateTime.fromISO('2026-11-01T01:30:00', { zone: 'America/New_York' })\nreturn [at.toISO(), at.plus({ hours: 1 }).toISO(), at.plus({ hours: 1 }).toFormat('h:mm a ZZZZ')]" },
	{ name: 'New York: the day of the November switch is 25 hours', code: "const day = DateTime.fromISO('2026-11-01T12:00:00', { zone: 'America/New_York' })\nreturn [day.endOf('day').diff(day.startOf('day'), 'hours').hours, Interval.fromDateTimes(day.startOf('day'), day.endOf('day')).length('hours')]" },
	{ name: 'New York: zone names either side of the switches', code: "return ['2026-03-08T01:00:00', '2026-03-08T04:00:00', '2026-11-01T00:30:00', '2026-11-01T03:00:00'].map(text => DateTime.fromISO(text, { zone: 'America/New_York' }).toFormat('yyyy-MM-dd HH:mm ZZ ZZZZ ZZZZZ'))" },
	{ name: 'Berlin offsets around the March 2026 switch', code: "return ['2026-03-29T01:59:59', '2026-03-29T03:00:00', '2026-10-25T02:30:00', '2026-10-25T03:00:00'].map(text => DateTime.fromISO(text, { zone: 'Europe/Berlin' }).offset)" },
	{ name: 'Berlin: a time in the March gap and an hour across it', code: "const gap = DateTime.fromISO('2026-03-29T02:30:00', { zone: 'Europe/Berlin' })\nconst before = DateTime.fromISO('2026-03-29T01:30:00', { zone: 'Europe/Berlin' })\nreturn [gap.toISO(), before.plus({ hours: 1 }).toISO(), before.plus({ days: 1 }).toISO()]" },
	{ name: 'Berlin: the October switch', code: "const day = DateTime.local(2026, 10, 25, 12, { zone: 'Europe/Berlin' })\nconst ambiguous = DateTime.local(2026, 10, 25, 2, 30, { zone: 'Europe/Berlin' })\nreturn [day.endOf('day').diff(day.startOf('day'), 'hours').hours, ambiguous.toISO(), ambiguous.plus({ hours: 1 }).toISO(), ambiguous.toFormat('HH:mm ZZZZ ZZZZZ')]" },
	{ name: 'Berlin: zone names in winter and summer', code: "return ['2026-01-15T12:00:00', '2026-07-15T12:00:00'].map(text => { const at = DateTime.fromISO(text, { zone: 'Europe/Berlin' }); return [at.offsetNameShort, at.offsetNameLong, at.isInDST, at.toFormat('ZZZZ|ZZZZZ|z')] })" },
	{ name: 'DST in the southern hemisphere (Sydney)', code: "return ['2026-01-15T12:00:00', '2026-07-15T12:00:00', '2026-04-05T02:30:00', '2026-10-04T02:30:00'].map(text => DateTime.fromISO(text, { zone: 'Australia/Sydney' }).toFormat('yyyy-MM-dd HH:mm ZZ ZZZZ'))" },
	{ name: 'Info.hasDST and isValidIANAZone', code: "return [Info.hasDST('Europe/Berlin'), Info.hasDST('Asia/Jakarta'), Info.isValidIANAZone('Asia/Jakarta'), Info.isValidIANAZone('asia/jakarta'), Info.isValidIANAZone('Mars/Base'), Info.normalizeZone('America/New_York').name]" },
	{ name: 'setZone keepLocalTime across a switch', code: "return DateTime.fromISO('2026-03-08T02:30:00', { zone: 'utc' }).setZone('America/New_York', { keepLocalTime: true }).toISO()" },

	// Formatting.
	{ name: 'every preset in UTC', code: `const at = DateTime.fromISO('2026-03-01T17:05:09.123Z')\nreturn ${JSON.stringify(presets)}.map(name => at.toLocaleString(DateTime[name]))` },
	...['Asia/Jakarta', 'America/New_York', 'Europe/Berlin', 'America/Los_Angeles', 'Asia/Kolkata', 'Australia/Sydney'].map((zone) => ({
		name: 'every preset in ' + zone,
		code: `return ['2026-01-15T17:05:09.123Z', '2026-07-15T08:45:30.5Z'].map(text => { const at = DateTime.fromISO(text).setZone(${JSON.stringify(zone)}); return ${JSON.stringify(presets)}.map(name => at.toLocaleString(DateTime[name])) })`,
	})),
	{ name: 'presets use the workflow zone', zone: 'Asia/Jakarta', code: "const at = DateTime.fromISO('2026-03-01T17:05:09Z')\nreturn [at.toLocaleString(DateTime.DATETIME_FULL), at.toLocaleString(DateTime.DATETIME_HUGE), at.toLocaleString(), at.toLocaleString({ weekday: 'long', hour: 'numeric' })]" },
	// en-CA and en-GB (FEAT-9we7kw fix round 1): Luxon's own preset and
	// toFormat paths delegate to the same Intl.DateTimeFormat the runtime
	// exposes, in UTC and in Europe/London — the zone whose short name is
	// itself locale-dependent (the zone-name overlay this round adds),
	// exercised here through Luxon's own formatting layer rather than
	// Intl directly.
	...['en-CA', 'en-GB'].map((locale) => ({
		name: 'every preset in ' + locale + ', UTC',
		code: `const at = DateTime.fromISO('2026-03-01T17:05:09.123Z').setLocale(${JSON.stringify(locale)})\nreturn ${JSON.stringify(presets)}.map(name => at.toLocaleString(DateTime[name]))`,
	})),
	...['en-CA', 'en-GB'].map((locale) => ({
		name: 'every preset in ' + locale + ', Europe/London',
		code: `return ['2026-01-15T17:05:09.123Z', '2026-07-15T08:45:30.5Z'].map(text => { const at = DateTime.fromISO(text).setZone('Europe/London').setLocale(${JSON.stringify(locale)}); return ${JSON.stringify(presets)}.map(name => at.toLocaleString(DateTime[name])) })`,
	})),
	...['en-CA', 'en-GB'].map((locale) => ({
		name: 'toFormat locale-sensitive tokens in ' + locale,
		code: `const at = DateTime.fromISO('2026-07-04T15:07:08.009Z').setZone('Europe/London').setLocale(${JSON.stringify(locale)})\nreturn [at.toFormat('cccc, LLLL d, yyyy'), at.toFormat('ccc d LLL'), at.toFormat('h:mm a'), at.toFormat('HH:mm'), at.toFormat('D'), at.toFormat('DD'), at.toFormat('t'), at.toFormat('T')]`,
	})),
	{ name: 'en-CA and en-GB refuse only what en-US refuses, and getLocale round-trips', code: "return ['en-CA', 'en-GB'].map(locale => { const at = DateTime.fromISO('2026-03-01T17:05:09Z').setLocale(locale)\n  return [at.locale, at.toFormat('cccc'), at.reconfigure({ locale }).locale] })" },
	{ name: 'every toFormat token', code: `return ['2026-03-01T05:05:09.123Z', '2026-12-31T23:59:59.9Z', '0033-07-04T12:00:00Z'].map(text => DateTime.fromISO(text, { zone: 'utc' }).toFormat(${JSON.stringify(tokens.split(' ').join("'|'"))}))` },
	...['Asia/Jakarta', 'America/New_York', 'Asia/Kolkata'].map((zone) => ({
		name: 'toFormat tokens and macros in ' + zone,
		code: `const at = DateTime.fromISO('2026-07-04T15:07:08.009Z').setZone(${JSON.stringify(zone)})\nreturn [at.toFormat(${JSON.stringify(tokens.split(' ').join("'|'"))}), ${JSON.stringify(macros.split(' '))}.map(token => at.toFormat(token))]`,
	})),
	{ name: 'toISO variants, SQL, RFC 2822, HTTP', zone: 'Asia/Jakarta', code: "const at = DateTime.fromISO('2026-03-01T17:05:09.123')\nreturn [at.toISO(), at.toISODate(), at.toISOTime(), at.toISOWeekDate(), at.toISO({ suppressMilliseconds: true }), at.toISO({ includeOffset: false }), at.toSQL(), at.toSQLDate(), at.toRFC2822(), at.toHTTP(), at.toMillis(), at.toSeconds(), at.toUnixInteger(), at.toObject(), at.toJSON(), String(at)]" },
	{ name: 'calendar fields', code: "const at = DateTime.fromISO('2024-02-29T12:00:00Z', { zone: 'utc' })\nreturn [at.weekNumber, at.weekYear, at.weekday, at.ordinal, at.daysInMonth, at.daysInYear, at.isInLeapYear, at.quarter, at.weeksInWeekYear, at.monthShort, at.monthLong, at.weekdayShort, at.weekdayLong, at.offsetNameShort]" },
	{ name: 'startOf and endOf', zone: 'America/Los_Angeles', code: "const at = DateTime.fromISO('2026-05-14T09:30:00')\nreturn ['year', 'quarter', 'month', 'week', 'day', 'hour'].map(unit => [at.startOf(unit).toISO(), at.endOf(unit).toISO()])" },
	{ name: 'parsing with fromFormat', zone: 'Asia/Jakarta', code: "return [DateTime.fromFormat('03/01/2026 5:05 PM', 'MM/dd/yyyy h:mm a').toISO(), DateTime.fromFormat('March 1, 2026', 'DDD').toISO(), DateTime.fromFormat('1 Mar 2026 17:05', 'd MMM yyyy HH:mm').toISO(), DateTime.fromFormat('2026-03-01 17:05 +0200', 'yyyy-MM-dd HH:mm ZZZ').toISO(), DateTime.fromFormat('Sunday, March 1, 2026', 'EEEE, MMMM d, yyyy').toISO(), DateTime.fromFormat('3/1/2026, 5:05 PM', 'D, t').toISO(), DateTime.fromFormat('nonsense', 'yyyy').invalidReason]" },
	{ name: 'parsing other formats', zone: 'Europe/Berlin', code: "return [DateTime.fromSQL('2026-03-01 17:05:09').toISO(), DateTime.fromRFC2822('Sun, 01 Mar 2026 17:05:09 +0000').toISO(), DateTime.fromHTTP('Sun, 01 Mar 2026 17:05:09 GMT').toISO(), DateTime.fromMillis(1772384709000).toISO(), DateTime.fromSeconds(1772384709).toISO(), DateTime.fromJSDate(new Date(1772384709000)).toISO(), DateTime.fromObject({ year: 2026, month: 3, day: 1, hour: 17 }).toISO(), DateTime.fromISO('2026-W09-7').toISODate(), DateTime.fromISO('nope').isValid]" },
	{ name: 'set, plus, minus and diff', zone: 'Asia/Kolkata', code: "const at = DateTime.fromISO('2026-01-31T10:00:00')\nreturn [at.plus({ months: 1 }).toISODate(), at.set({ day: 15, hour: 3 }).toISO(), at.minus({ years: 1, days: 2 }).toISO(), at.diff(DateTime.fromISO('2025-12-25T08:30:00'), ['days', 'hours', 'minutes']).toObject(), at.until(at.plus({ weeks: 2 })).length('days'), DateTime.max(at, at.plus({ hours: 1 })).toISO()]" },
	{ name: 'Info lists in English', code: "return [Info.months(), Info.months('short'), Info.monthsFormat('long'), Info.weekdays(), Info.weekdays('short'), Info.weekdays('narrow'), Info.meridiems(), Info.eras(), Info.eras('long'), Info.features()]" },
	{ name: 'Durations', code: "const length = Duration.fromObject({ hours: 25, minutes: 90 })\nreturn [length.toISO(), length.shiftTo('days', 'hours', 'minutes').toObject(), length.normalize().toObject(), length.as('minutes'), length.toFormat('hh:mm:ss'), Duration.fromISO('P1Y2M3DT4H').toObject(), Duration.fromMillis(3723004).toFormat('h:mm:ss.SSS'), length.rescale().toObject()]" },
	{ name: 'Intervals', zone: 'America/New_York', code: "const range = Interval.fromDateTimes(DateTime.fromISO('2026-03-07T00:00:00'), DateTime.fromISO('2026-03-10T00:00:00'))\nreturn [range.toISO(), range.length('hours'), range.count('days'), range.splitBy({ days: 1 }).map(part => part.start.toISO()), range.contains(DateTime.fromISO('2026-03-08T12:00:00')), range.toDuration(['days', 'hours']).toObject()]" },
	{ name: 'relative formatting in English', code: "Settings.now = () => Date.parse('2026-03-03T10:00:00Z')\nconst base = DateTime.fromISO('2026-03-01T10:00:00Z')\nreturn [base.toRelative(), base.plus({ days: 5 }).toRelative(), base.toRelative({ unit: 'hours' }), base.toRelativeCalendar(), base.plus({ days: 3 }).toRelativeCalendar(), base.toRelative({ style: 'short' }), base.minus({ months: 3 }).toRelative()]" },
	{ name: 'invalid DateTimes', code: "const bad = DateTime.fromObject({ month: 13 })\nreturn [bad.isValid, bad.invalidReason, bad.invalidExplanation, String(bad), bad.toISO(), DateTime.fromISO('2026-03-01T10:00:00', { zone: 'Mars/Base' }).invalidReason]" },
	{ name: 'Settings.defaultZone may be changed by the code', zone: 'Asia/Jakarta', code: "Settings.defaultZone = 'America/Los_Angeles'\nreturn [DateTime.local(2026, 7, 1, 12).toISO(), DateTime.now().zoneName]" },
	{ name: 'a fixed-offset zone', code: "const at = DateTime.fromISO('2026-03-01T10:05:00Z').setZone('UTC+7')\nreturn [at.toISO(), at.zoneName, at.toFormat('ZZZZ ZZZZZ'), at.toLocaleString(DateTime.DATETIME_FULL), at.offsetNameLong]" },
	{ name: 'the week in en-US starts on Sunday for locale weeks', code: "const at = DateTime.fromISO('2026-03-04T12:00:00Z', { zone: 'utc' })\nreturn [Info.getStartOfWeek(), Info.getMinimumDaysInFirstWeek(), Info.getWeekendWeekdays(), at.localWeekNumber, at.localWeekday, at.localWeekYear, at.startOf('week', { useLocaleWeeks: true }).toISODate(), at.toFormat('n nn ii')]" },
];

// ---- Intl.DateTimeFormat and Date's locale methods -------------------------

const instants = [Date.UTC(2026, 2, 1, 17, 5, 9, 123), Date.UTC(2026, 10, 12, 0, 45, 30, 9), Date.UTC(1999, 0, 4, 9, 7, 3, 0)];

function optionSets() {
	const sets = [];
	const W = [undefined, 'narrow', 'short', 'long'];
	for (const weekday of W) for (const era of W) for (const year of [undefined, 'numeric', '2-digit']) for (const month of [undefined, 'numeric', '2-digit', 'narrow', 'short', 'long']) for (const day of [undefined, 'numeric', '2-digit']) {
		sets.push({ weekday, era, year, month, day });
	}
	for (const hour of [undefined, 'numeric', '2-digit']) for (const minute of [undefined, 'numeric', '2-digit']) for (const second of [undefined, 'numeric', '2-digit']) for (const fractionalSecondDigits of [undefined, 1, 3]) for (const hourCycle of [undefined, 'h11', 'h23', 'h24']) {
		sets.push({ hour, minute, second, fractionalSecondDigits, hourCycle });
	}
	for (const dayPeriod of ['narrow', 'short', 'long']) for (const hour of [undefined, 'numeric', '2-digit']) for (const minute of [undefined, '2-digit']) for (const hourCycle of [undefined, 'h23']) {
		sets.push({ dayPeriod, hour, minute, hourCycle });
	}
	for (const timeZoneName of ['short', 'long', 'shortOffset', 'longOffset']) for (const hour of [undefined, 'numeric']) for (const minute of [undefined, '2-digit']) for (const second of [undefined, 'numeric']) for (const hourCycle of [undefined, 'h23']) {
		sets.push({ hour, minute, second, hourCycle, timeZoneName });
	}
	for (const weekday of [undefined, 'short', 'long']) for (const year of [undefined, 'numeric']) for (const month of [undefined, 'numeric', 'short', 'long']) for (const day of [undefined, 'numeric']) for (const hour of [undefined, 'numeric']) for (const minute of [undefined, '2-digit']) for (const timeZoneName of [undefined, 'short']) {
		sets.push({ weekday, year, month, day, hour, minute, timeZoneName });
	}
	for (const hour12 of [true, false]) for (const set of [{ hour: 'numeric' }, { hour: '2-digit', minute: '2-digit' }, { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' }, { weekday: 'long', hour: 'numeric', timeZoneName: 'long' }]) {
		sets.push({ ...set, hour12 });
	}
	for (const dateStyle of [undefined, 'full', 'long', 'medium', 'short']) for (const timeStyle of [undefined, 'full', 'long', 'medium', 'short']) for (const hourCycle of [undefined, 'h11', 'h12', 'h23', 'h24']) for (const hour12 of [undefined, true, false]) {
		if (hourCycle && hour12 !== undefined) continue;
		sets.push({ dateStyle, timeStyle, hourCycle, hour12 });
	}
	const seen = new Set();
	return sets.map((set) => {
		const clean = {};
		for (const [key, value] of Object.entries(set)) if (value !== undefined) clean[key] = value;
		return clean;
	}).filter((set) => {
		const key = JSON.stringify(set);
		if (seen.has(key) || key === '{}') return false;
		seen.add(key);
		return true;
	});
}

// The locales date formatting is recorded for: en-US, and the two other
// English locales the runtime also ships (en-CA's YYYY-MM-DD short dates,
// en-GB's day-first ones).
const dateLocales = ['en-US', 'en-CA', 'en-GB'];

// sweepZones are the time zones the option sweep runs in: UTC, and
// Europe/London (FEAT-9we7kw fix round 1 — a non-UTC, non-integer-hour-free
// zone whose short zone name is itself locale-dependent, "GMT" or "BST" for
// en-US/en-CA versus en-GB, the zone-name overlay this round adds; running
// the whole option matrix there, not just the dedicated zone-name probes,
// is what actually pins the interaction between a locale's date/time
// pattern and its zone-name choice for every option combination, not only
// the ones that ask for timeZoneName explicitly).
const sweepZones = ['UTC', 'Europe/London'];

// The option sweep is one golden of its own: every set formatted at three
// instants, for every locale and zone above, which pins the pattern each
// combination produces in each.
function sweep() {
	process.env.TZ = 'UTC';
	return optionSets().flatMap((options) => sweepZones.map((zone) => {
		const want = {};
		for (const locale of dateLocales) {
			const format = new Intl.DateTimeFormat(locale, { timeZone: zone, ...options });
			want[locale] = instants.map((at) => format.format(new Date(at)));
		}
		return { options, zone, want };
	}));
}

const dateProbes = [
	{ name: 'Date.prototype.toLocale* without arguments use en-US and the workflow zone', zone: 'Asia/Jakarta', code: `return ${JSON.stringify(instants)}.map(at => { const date = new Date(at); return [date.toLocaleString(), date.toLocaleDateString(), date.toLocaleTimeString()] })` },
	{ name: 'Date.prototype.toLocale* in America/New_York', zone: 'America/New_York', code: `return ${JSON.stringify(instants)}.map(at => { const date = new Date(at); return [date.toLocaleString(), date.toLocaleDateString(), date.toLocaleTimeString()] })` },
	{ name: "Date.prototype.toLocaleDateString('en-US', options)", code: "const date = new Date(Date.UTC(2026, 2, 1, 17, 5, 9))\nreturn [date.toLocaleDateString('en-US', { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' }), date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' }), date.toLocaleDateString('en-US', { timeZone: 'Asia/Jakarta', dateStyle: 'medium' }), date.toLocaleDateString('en-US', { hour: 'numeric' }), date.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' }), date.toLocaleTimeString('en-US', { timeZone: 'America/Los_Angeles', timeZoneName: 'short' }), date.toLocaleString('en-US', { timeZone: 'UTC', hour12: false }), date.toLocaleString('en', { timeZone: 'Asia/Jakarta' }), date.toLocaleString(undefined, { timeZone: 'America/New_York', month: 'long' })]" },
	{ name: 'Date locale methods on an invalid date', code: "const date = new Date(NaN)\nreturn [date.toLocaleString(), date.toLocaleDateString('en-US'), date.toLocaleTimeString()]" },
	{ name: 'formatToParts for the options Luxon uses', code: "const date = new Date(Date.UTC(2026, 2, 1, 17, 5, 9, 123))\nreturn [\n  new Intl.DateTimeFormat('en-US', { hour12: false, timeZone: 'America/New_York', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', era: 'short' }).formatToParts(date),\n  new Intl.DateTimeFormat('en-US', { hourCycle: 'h23', timeZone: 'Asia/Jakarta', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', era: 'short' }).formatToParts(date),\n  new Intl.DateTimeFormat('en-US', { hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', timeZone: 'Europe/Berlin', timeZoneName: 'short' }).formatToParts(date),\n  new Intl.DateTimeFormat('en-US', { timeZone: 'America/Los_Angeles', dateStyle: 'full', timeStyle: 'long' }).formatToParts(date),\n  new Intl.DateTimeFormat('en-US', { timeZone: 'UTC', hour: 'numeric', minute: 'numeric', second: 'numeric', fractionalSecondDigits: 2, dayPeriod: 'long' }).formatToParts(date),\n]" },
	{ name: 'common option sets in several zones', code: "const zones = ['UTC', 'Asia/Jakarta', 'America/Los_Angeles', 'America/New_York', 'Europe/Berlin', 'Asia/Kolkata', 'Asia/Tokyo', 'Australia/Adelaide']\nconst sets = [{}, { dateStyle: 'full', timeStyle: 'full' }, { dateStyle: 'medium', timeStyle: 'short' }, { year: 'numeric', month: 'long', day: 'numeric', hour: 'numeric', minute: '2-digit', timeZoneName: 'short' }, { weekday: 'short', hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZoneName: 'long' }, { timeZoneName: 'shortOffset' }, { timeZoneName: 'longOffset', hour: 'numeric' }]\nreturn zones.map(timeZone => [Date.UTC(2026, 0, 15, 17, 5, 9), Date.UTC(2026, 6, 15, 3, 45, 0)].map(at => sets.map(set => new Intl.DateTimeFormat('en-US', { ...set, timeZone }).format(at))))" },
	{ name: 'resolvedOptions', zone: 'Asia/Jakarta', code: "return [{}, { hour: 'numeric' }, { hour: 'numeric', hourCycle: 'h23' }, { hour: 'numeric', hour12: false }, { hour: '2-digit', minute: 'numeric', hour12: true }, { dateStyle: 'short', timeStyle: 'short' }, { month: '2-digit', minute: 'numeric' }, { weekday: 'long', era: 'short', timeZoneName: 'short', timeZone: 'america/new_york' }, { second: 'numeric', fractionalSecondDigits: 2 }, { dayPeriod: 'short' }, { timeStyle: 'medium', hourCycle: 'h24' }].map(options => new Intl.DateTimeFormat('en-US', options).resolvedOptions())" },
	{ name: 'resolvedOptions for en and the default locale', code: "return [new Intl.DateTimeFormat('en').resolvedOptions().locale, new Intl.DateTimeFormat().resolvedOptions().locale, new Intl.DateTimeFormat(['en-US', 'de-DE']).resolvedOptions().locale, new Intl.DateTimeFormat('en-US-u-hc-h23', { hour: 'numeric' }).resolvedOptions(), new Intl.DateTimeFormat('en-US-u-hc-h23', { hour: 'numeric' }).format(Date.UTC(2026, 0, 1, 7))]" },
	{ name: 'time-zone identifiers are canonicalised', code: "return ['asia/jakarta', 'US/Eastern', 'Asia/Calcutta', 'Asia/Kolkata', 'Etc/UTC', 'utc', 'GMT', 'Europe/Kiev', 'Europe/Kyiv', 'Etc/GMT+5', 'EST5EDT', 'America/Buenos_Aires', 'AMERICA/NEW_YORK', '+07:00', '-0530', '+07', '-00:00'].map(timeZone => new Intl.DateTimeFormat('en-US', { timeZone }).resolvedOptions().timeZone)" },
	{ name: 'an offset time zone formats with its offset', code: "return ['+07:00', '-05:30'].map(timeZone => new Intl.DateTimeFormat('en-US', { timeZone, dateStyle: 'medium', timeStyle: 'full' }).format(Date.UTC(2026, 2, 1, 17, 5, 9)))" },
	{ name: 'an unknown time zone is a RangeError', code: "return new Intl.DateTimeFormat('en-US', { timeZone: 'Mars/Base' })" },
	{ name: 'an invalid option value is a RangeError', code: "return new Intl.DateTimeFormat('en-US', { month: 'longest' })" },
	{ name: 'dateStyle with a component is a TypeError', code: "return new Intl.DateTimeFormat('en-US', { dateStyle: 'short', hour: 'numeric' })" },
	{ name: 'formatting an invalid date is a RangeError', code: "return new Intl.DateTimeFormat('en-US').format(NaN)" },
	{ name: 'supportedLocalesOf and getCanonicalLocales', code: "return [Intl.DateTimeFormat.supportedLocalesOf(['en-US', 'en']), Intl.getCanonicalLocales(['EN-us', 'de-de', 'id-ID', 'zh-hant-tw'])]" },
	{ name: 'years before the common era and far from now', code: "const format = new Intl.DateTimeFormat('en-US', { timeZone: 'UTC' })\nconst withEra = new Intl.DateTimeFormat('en-US', { timeZone: 'UTC', year: 'numeric', era: 'short', month: 'short', day: 'numeric' })\nreturn [Date.UTC(-5, 5, 1), Date.UTC(0, 5, 1) - 1900 * 365.2425 * 864e5, 8.64e15, -8.64e15, Date.UTC(1, 0, 1)].map(at => [format.format(at), withEra.format(at)])" },
	{ name: 'a Date argument, a number and nothing', code: "const format = new Intl.DateTimeFormat('en-US', { timeZone: 'UTC', dateStyle: 'medium' })\nreturn [format.format(new Date(Date.UTC(2026, 2, 1))), format.format(Date.UTC(2026, 2, 1)), typeof format.format(), format.format.name, format.formatToParts.length]" },

	// en-CA and en-GB (FEAT-9we7kw): the corpus's other two English date
	// locales, matched the same way en-US is (case-insensitively, ignoring a
	// -u- extension) and refusing every other locale by name.
	{ name: "toLocaleDateString('en-CA') is YYYY-MM-DD", code: "const date = new Date(Date.UTC(2026, 2, 1, 17, 5, 9))\nreturn [date.toLocaleDateString('en-CA'), date.toLocaleDateString('en-ca'), date.toLocaleDateString('en-CA', { timeZone: 'UTC' }), new Date(Date.UTC(2026, 0, 4)).toLocaleDateString('en-CA')]" },
	{ name: 'en-GB is day-first and 24-hour by default', code: "const date = new Date(Date.UTC(2026, 2, 1, 17, 5, 9))\nreturn [date.toLocaleDateString('en-GB', { timeZone: 'UTC' }), date.toLocaleTimeString('en-GB', { timeZone: 'UTC' }), date.toLocaleString('en-GB', { timeZone: 'UTC' }), date.toLocaleDateString('en-gb', { timeZone: 'UTC' })]" },
	{ name: 'en-CA and en-GB dateStyle and timeStyle', code: "const date = new Date(Date.UTC(2026, 2, 1, 17, 5, 9))\nreturn ['en-CA', 'en-GB'].flatMap(locale => ['full', 'long', 'medium', 'short'].map(style => new Intl.DateTimeFormat(locale, { timeZone: 'UTC', dateStyle: style, timeStyle: style }).format(date)))" },
	{ name: 'en-CA and en-GB AM/PM markers, forced through hourCycle', code: "const at = [Date.UTC(2026, 0, 1, 5), Date.UTC(2026, 0, 1, 17)]\nreturn ['en-CA', 'en-GB'].flatMap(locale => at.map(ms => new Intl.DateTimeFormat(locale, { timeZone: 'UTC', hour: 'numeric', hourCycle: 'h12' }).format(ms)))" },
	{ name: 'en-CA and en-GB resolvedOptions default hour cycle', code: "return ['en-CA', 'en-GB'].map(locale => new Intl.DateTimeFormat(locale, { timeZone: 'UTC', hour: 'numeric' }).resolvedOptions())" },
	// supportedLocalesOf and the refusal of every other English region
	// (en-AU, de-DE) are deliberate divergences from Node — the runtime
	// only ever claims the three locales it ships data for — and are
	// pinned by TestNonEnglishDateFormattingIsANamedError in Go instead;
	// a parity probe would record Node's own (wider) answer and fail on
	// purpose.
	{ name: 'locale matching is the same for en-CA and en-GB as for en-US: case-insensitive, and a -u- extension does not change the locale', code: "return [new Intl.DateTimeFormat('en-ca').resolvedOptions().locale, new Intl.DateTimeFormat('EN-GB').resolvedOptions().locale, new Intl.DateTimeFormat('en-CA-u-hc-h23', { hour: 'numeric' }).resolvedOptions(), new Intl.DateTimeFormat('en-GB-u-hc-h12', { hour: 'numeric' }).format(Date.UTC(2026, 0, 1, 5)), Intl.DateTimeFormat.supportedLocalesOf(['en-US', 'en-CA', 'en-GB', 'en'])]" },
	// FEAT-9we7kw fix round 1: an explicit script subtag ("en-Latn-GB") is
	// not "en-GB" — the BCP 47 Lookup algorithm strips the tag from the
	// right one subtag at a time, so "en-Latn-GB" tries "en-Latn-GB",
	// "en-Latn", then "en", never "en-GB" (which is only on the fallback
	// path when no script was given at all). Node resolves en-Latn-GB,
	// en-Latn-CA and en-Latn-US alike to plain "en".
	{ name: 'an explicit script subtag resolves to en, not the region', code: "const at = new Date(Date.UTC(2026, 0, 15, 17, 5, 0))\nreturn ['en-Latn-GB', 'en-Latn-CA', 'en-Latn-US', 'en-Latn'].map(locale => [new Intl.DateTimeFormat(locale).resolvedOptions().locale, at.toLocaleDateString(locale), at.toLocaleString(locale)])" },
	// FEAT-9we7kw fix round 3: the hour Date#toLocale* adds for the caller
	// is not always lettered in the cycle the caller negotiated — h11 and
	// h24's own digits ("0" and "24" at midnight) come out only when the
	// locale's own -u-hc- extension asked for that family. The midnight
	// instants are what separates the four cycles, so they lead here.
	{ name: "Date#toLocale* letter the hour they default in the locale's own cycle, not the negotiated one", code: "const at = [Date.UTC(2026, 10, 12, 0, 45, 30), Date.UTC(2026, 0, 1, 12, 0, 0), Date.UTC(2026, 2, 1, 17, 5, 9)].map(ms => new Date(ms))\nreturn ['en', 'en-US', 'en-CA', 'en-GB', 'en-US-u-hc-h11', 'en-US-u-hc-h24', 'en-CA-u-hc-h11', 'en-CA-u-hc-h24', 'en-GB-u-hc-h11', 'en-GB-u-hc-h24'].flatMap(locale => [undefined, 'h11', 'h12', 'h23', 'h24'].flatMap(hourCycle => at.map(date => [date.toLocaleString(locale, { timeZone: 'UTC', hourCycle }), date.toLocaleTimeString(locale, { timeZone: 'UTC', hourCycle })].join(' | '))))" },
	{ name: 'the same hour, asked for explicitly, uses the cycle that was asked for', code: "const at = new Date(Date.UTC(2026, 10, 12, 0, 45, 30))\nreturn ['en-US', 'en-CA', 'en-GB', 'en-US-u-hc-h11', 'en-GB-u-hc-h24'].flatMap(locale => [undefined, 'h11', 'h12', 'h23', 'h24'].map(hourCycle => [at.toLocaleTimeString(locale, { timeZone: 'UTC', hourCycle, hour: 'numeric', minute: 'numeric', second: 'numeric' }), new Intl.DateTimeFormat(locale, { timeZone: 'UTC', hourCycle, hour: 'numeric', minute: 'numeric', second: 'numeric' }).format(at)].join(' | ')))" },
	{ name: 'toLocaleDateString refuses a timeStyle and toLocaleTimeString a dateStyle, even alongside its own style', code: "const at = new Date(Date.UTC(2026, 2, 1, 17, 5, 9))\nconst threw = (run) => { try { return run() } catch (error) { return String(error) } }\nreturn [\n  threw(() => at.toLocaleDateString('en-GB', { timeZone: 'UTC', timeStyle: 'short' })),\n  threw(() => at.toLocaleDateString('en-GB', { timeZone: 'UTC', dateStyle: 'short', timeStyle: 'short' })),\n  threw(() => at.toLocaleTimeString('en-CA', { timeZone: 'UTC', dateStyle: 'short' })),\n  threw(() => at.toLocaleTimeString('en-CA', { timeZone: 'UTC', dateStyle: 'short', timeStyle: 'short' })),\n  at.toLocaleString('en-CA', { timeZone: 'UTC', dateStyle: 'short', timeStyle: 'short' }),\n  new Intl.DateTimeFormat('en-GB', { timeZone: 'UTC', dateStyle: 'short', timeStyle: 'short' }).format(at),\n]" },
];

// ---- Intl.NumberFormat and Number.prototype.toLocaleString -----------------

const numberLocales = ['en-US', 'de-DE', 'id-ID', 'fr-FR', 'ja-JP', 'en-IN'];
const numberProbes = [
	{ name: 'toLocaleString without arguments is en-US', zone: 'Europe/Berlin', code: 'return [1234567.891, -1234.5, 0.000123, 1e21, 123456789012, -0, NaN, Infinity].map(value => value.toLocaleString())' },
	{ name: 'toLocaleString with locales', code: `return ${JSON.stringify(numberLocales.concat(['es-ES', 'pt-BR', 'de-CH', 'ar-EG', 'hi-IN', 'zh-CN']))}.map(locale => (1234567.891).toLocaleString(locale))` },
	...numberLocales.map((locale) => ({
		name: 'NumberFormat in ' + locale,
		code: `const f = (options, value) => new Intl.NumberFormat(${JSON.stringify(locale)}, options).format(value)\nreturn [\n  f({}, 1234567.891), f({}, -1234.5), f({}, 0.5), f({}, 1234), f({}, 0),\n  f({ style: 'percent' }, 0.256), f({ style: 'percent', minimumFractionDigits: 1 }, 0.25), f({ style: 'percent' }, -1.5),\n  f({ style: 'currency', currency: 'USD' }, 1234.5), f({ style: 'currency', currency: 'EUR' }, 1234.5), f({ style: 'currency', currency: 'IDR' }, 1234567.5), f({ style: 'currency', currency: 'JPY' }, 1234567.5),\n  f({ style: 'currency', currency: 'USD' }, -1234.5), f({ style: 'currency', currency: 'EUR', currencyDisplay: 'code' }, 1234.5), f({ style: 'currency', currency: 'USD', currencyDisplay: 'narrowSymbol' }, 1234.5),\n  f({ minimumFractionDigits: 2 }, 1234), f({ maximumFractionDigits: 0 }, 1234.5), f({ maximumFractionDigits: 0 }, 1235.5), f({ minimumFractionDigits: 2, maximumFractionDigits: 2 }, 1.005), f({ maximumFractionDigits: 1 }, 0.25), f({ minimumIntegerDigits: 3 }, 7), f({ useGrouping: false }, 1234567.5),\n  f({ style: 'currency', currency: 'IDR', minimumFractionDigits: 0, maximumFractionDigits: 0 }, 1234567.5), f({ maximumSignificantDigits: 3 }, 1234567.891), f({ minimumSignificantDigits: 5 }, 1.5), f({ signDisplay: 'always' }, 5), f({ signDisplay: 'exceptZero' }, 0),\n]`,
	})),
	{ name: 'formatToParts', code: "return [new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).formatToParts(-1234.5), new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).formatToParts(1234567.891), new Intl.NumberFormat('fr-FR', { style: 'percent', maximumFractionDigits: 1 }).formatToParts(0.1234), new Intl.NumberFormat('id-ID').formatToParts(-0.5)]" },
	{ name: 'resolvedOptions', code: "return [new Intl.NumberFormat().resolvedOptions(), new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'eur' }).resolvedOptions(), new Intl.NumberFormat('ja-JP', { style: 'currency', currency: 'JPY' }).resolvedOptions(), new Intl.NumberFormat('en-US', { style: 'percent', maximumSignificantDigits: 2 }).resolvedOptions(), new Intl.NumberFormat('id', { minimumFractionDigits: 2 }).resolvedOptions()]" },
	{ name: 'strings and bigints format exactly', code: "const format = new Intl.NumberFormat('en-US', { maximumFractionDigits: 20 })\nreturn [format.format('12345678901234567890.123456789'), format.format(12345678901234567890n), format.format('-0.1'), format.format('1e3'), format.format('abc'), format.format(2n ** 300n), format.format(-(10n ** 40n)), format.format('1' + '0'.repeat(400)), format.format('0.' + '0'.repeat(400) + '1'), format.format('123456789'.repeat(40)), format.format(' 42 '), format.format('0x1F'), format.format('')]" },
	{ name: 'rounding is half away from zero on the decimal the number prints as', code: "const f = (value, digits) => new Intl.NumberFormat('en-US', { maximumFractionDigits: digits }).format(value)\nreturn [f(1.005, 2), f(0.125, 2), f(0.135, 2), f(2.5, 0), f(-2.5, 0), f(1.45, 1), f(8.345, 2), f(1e-7, 20), f(123.456e10, 0)]" },
	{ name: 'invalid options are RangeErrors', code: "return new Intl.NumberFormat('en-US', { maximumFractionDigits: 101 })" },
	{ name: 'a missing currency is a TypeError', code: "return new Intl.NumberFormat('en-US', { style: 'currency' })" },
	{ name: 'an invalid currency code is a RangeError', code: "return new Intl.NumberFormat('en-US', { style: 'currency', currency: 'DOLLARS' })" },
	{ name: 'minimumFractionDigits above maximumFractionDigits is a RangeError', code: "return new Intl.NumberFormat('en-US', { minimumFractionDigits: 3, maximumFractionDigits: 1 })" },
];

// A broad sweep: the separators, grouping and percent sign of many locales.
const sweepLocales = ['af', 'am', 'ar', 'as', 'az', 'be', 'bg', 'bn', 'bn-BD', 'bn-IN', 'bs', 'ca', 'cs', 'cy', 'da', 'de', 'de-AT', 'de-BE', 'de-CH', 'de-DE', 'de-IT', 'de-LI', 'de-LU', 'el', 'en', 'es', 'et', 'eu', 'fa', 'fa-AF', 'fa-IR', 'fi', 'fil', 'fr', 'ga', 'gl', 'gu', 'he', 'hi', 'hi-IN', 'hr', 'hu', 'hy', 'id', 'id-ID', 'is', 'it', 'it-CH', 'ja', 'ja-JP', 'ka', 'kk', 'km', 'kn', 'ko', 'ky', 'lo', 'lt', 'lv', 'mk', 'ml', 'mn', 'mr', 'ms', 'ms-BN', 'ms-SG', 'my', 'nb', 'ne', 'ne-IN', 'nl', 'nl-BE', 'or', 'pa', 'pl', 'ps', 'pt', 'pt-AO', 'pt-BR', 'pt-CH', 'pt-CV', 'pt-GW', 'pt-LU', 'pt-MO', 'pt-MZ', 'pt-PT', 'pt-ST', 'pt-TL', 'hr-BA', 'sq-MK', 'it-SM', 'sv-AX', 'ro', 'ru', 'si', 'sk', 'sl', 'sq', 'sr', 'sr-Latn', 'sv', 'sv-FI', 'sw', 'sw-KE', 'ta', 'ta-LK', 'ta-MY', 'ta-SG', 'te', 'th', 'tr', 'uk', 'ur', 'ur-IN', 'uz', 'vi', 'zh', 'zh-HK', 'zh-Hant', 'zh-MO', 'zh-SG', 'zh-TW', 'zu']
	.concat(['AE', 'BH', 'DJ', 'DZ', 'EG', 'EH', 'ER', 'IL', 'IQ', 'JO', 'KM', 'KW', 'LB', 'LY', 'MA', 'MR', 'OM', 'PS', 'QA', 'SA', 'SD', 'SO', 'SS', 'SY', 'TD', 'TN', 'YE'].map((region) => 'ar-' + region))
	.concat(['419', 'AR', 'BO', 'CL', 'CO', 'CR', 'CU', 'DO', 'EC', 'ES', 'GQ', 'GT', 'HN', 'MX', 'NI', 'PA', 'PE', 'PH', 'PR', 'PY', 'SV', 'US', 'UY', 'VE'].map((region) => 'es-' + region))
	.concat(['BE', 'CA', 'CH', 'CI', 'CM', 'DZ', 'FR', 'LU', 'MA', 'SN', 'TN'].map((region) => 'fr-' + region))
	.concat(['001', '150', 'AT', 'AU', 'BE', 'CA', 'CH', 'DE', 'DK', 'FI', 'GB', 'GH', 'IE', 'IN', 'KE', 'MY', 'NG', 'NL', 'NZ', 'PH', 'PK', 'SE', 'SG', 'US', 'ZA'].map((region) => 'en-' + region));
const numberSweep = sweepLocales.map((locale) => ({
	locale,
	want: [
		new Intl.NumberFormat(locale).format(1234567.891),
		new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(1234.5),
		new Intl.NumberFormat(locale, { style: 'percent' }).format(0.256),
		new Intl.NumberFormat(locale).format(-1234.5),
		new Intl.NumberFormat(locale).format(1234),
		new Intl.NumberFormat(locale).format(1234567890.5),
		new Intl.NumberFormat(locale, { style: 'percent' }).format(-0.5),
	],
}));

// Currencies: where each locale puts a currency, and which symbol it uses.
const currencyLocales = ['en', 'en-US', 'en-GB', 'en-AU', 'en-CA', 'en-IN', 'en-SG', 'en-PH', 'de', 'de-DE', 'de-AT', 'de-CH', 'fr', 'fr-FR', 'fr-CA', 'fr-CH', 'es', 'es-ES', 'es-MX', 'es-AR', 'it', 'it-IT', 'pt', 'pt-BR', 'pt-PT', 'nl', 'nl-NL', 'nl-BE', 'pl', 'ru', 'sv', 'da', 'nb', 'fi', 'cs', 'tr', 'vi', 'uk', 'id', 'id-ID', 'ms', 'ms-MY', 'ja', 'ja-JP', 'zh', 'zh-CN', 'zh-TW', 'zh-HK', 'ko', 'hi', 'th', 'fil'];
const currencyCodes = ['USD', 'EUR', 'IDR', 'JPY', 'GBP', 'INR', 'CNY', 'KRW', 'BRL', 'AUD', 'CAD', 'CHF', 'SGD', 'MYR', 'THB', 'PHP', 'VND', 'MXN', 'RUB', 'TRY', 'SAR', 'AED', 'HKD', 'TWD', 'KWD', 'CLP', 'HUF', 'XAF'];
const currencySweep = currencyLocales.map((locale) => ({
	locale,
	want: currencyCodes.map((currency) => ['symbol', 'narrowSymbol', 'code'].map((currencyDisplay) =>
		new Intl.NumberFormat(locale, { style: 'currency', currency, currencyDisplay }).format(-1234.5)).join(' | ')),
	positive: ['USD', 'EUR'].map((currency) => new Intl.NumberFormat(locale, { style: 'currency', currency }).format(1234567.5)).join(' | '),
}));

// Every currency Node knows: its symbol and narrow symbol in each locale
// above, and its default fraction digits.
const allCurrencies = Intl.supportedValuesOf('currency');
const currencySymbols = {
	codes: allCurrencies,
	digits: allCurrencies.map((currency) => new Intl.NumberFormat('en', { style: 'currency', currency }).resolvedOptions().maximumFractionDigits),
	locales: Object.fromEntries(currencyLocales.map((locale) => [locale, allCurrencies.map((currency) => ['symbol', 'narrowSymbol'].map((currencyDisplay) =>
		new Intl.NumberFormat(locale, { style: 'currency', currency, currencyDisplay }).formatToParts(1).find((part) => part.type === 'currency').value).join('|'))])),
};

// ---- Week information --------------------------------------------------------

// Intl.Locale's week information for every two-letter region, and for
// languages named without a region, whose region is inferred.
const letters = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ';
const weekOf = (tag) => {
	const info = new Intl.Locale(tag).getWeekInfo();
	return info.firstDay + '|' + info.weekend.join(',');
};
const weeks = {
	regions: Object.fromEntries([...letters].flatMap((a) => [...letters].map((b) => a + b)).map((region) => [region, weekOf('und-' + region)])),
	languages: Object.fromEntries(['en', 'de', 'fr', 'es', 'pt', 'id', 'ms', 'ja', 'zh', 'ko', 'ar', 'fa', 'he', 'hi', 'th', 'vi', 'ru', 'tr', 'nl', 'sv', 'ug', 'und'].map((tag) => [tag, weekOf(tag)])),
};

// ---- Collation --------------------------------------------------------------

const collationProbes = [
	{ name: 'the default order', code: "return ['b', 'A', 'a', 'B', 'ä', 'z', 'Z', '10', '9', '2', 'é', 'e', 'E', '_x', ' y', 'ß', 'ss'].sort((x, y) => x.localeCompare(y))" },
	{ name: 'numeric', code: "const items = ['item10', 'item9', 'item1', 'Item2', 'item02']\nreturn [items.slice().sort((x, y) => x.localeCompare(y, undefined, { numeric: true })), items.slice().sort((x, y) => x.localeCompare(y)), 'a10'.localeCompare('a9', 'en', { numeric: true }), 'a10'.localeCompare('a9')]" },
	{ name: 'sensitivity', code: "const pairs = [['a', 'A'], ['a', 'á'], ['a', 'b'], ['a', 'a'], ['résumé', 'RESUME'], ['Å', 'a']]\nreturn ['base', 'accent', 'case', 'variant'].map(sensitivity => pairs.map(([x, y]) => x.localeCompare(y, 'en', { sensitivity })))" },
	{ name: 'locale tailorings', code: "return [['ä', 'z', 'de'], ['ä', 'z', 'sv'], ['ch', 'h', 'cs'], ['ñ', 'o', 'es'], ['ı', 'i', 'tr'], ['aa', 'z', 'da']].map(([x, y, locale]) => x.localeCompare(y, locale))" },
	{ name: 'numeric with sensitivity', code: "return ['File 10.txt', 'file 9.txt', 'FILE 9.TXT'].sort((x, y) => x.localeCompare(y, 'en', { numeric: true, sensitivity: 'base' }))" },
];

// ---- Time-zone names and the zone table --------------------------------------

function goZoneNames() {
	const goroot = execFileSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).trim();
	const zip = fs.readFileSync(path.join(goroot, 'lib/time/zoneinfo.zip'));
	let end = zip.length - 22;
	while (end >= 0 && zip.readUInt32LE(end) !== 0x06054b50) end--;
	const count = zip.readUInt16LE(end + 10);
	let offset = zip.readUInt32LE(end + 16);
	const names = [];
	for (let index = 0; index < count; index++) {
		const nameLength = zip.readUInt16LE(offset + 28);
		const extra = zip.readUInt16LE(offset + 30);
		const comment = zip.readUInt16LE(offset + 32);
		names.push(zip.toString('utf8', offset + 46, offset + 46 + nameLength));
		offset += 46 + nameLength + extra + comment;
	}
	return names.filter((name) => !name.endsWith('/')).sort();
}

const january = Date.UTC(2026, 0, 15, 12);
const july = Date.UTC(2026, 6, 15, 12);

// zoneLocales are the locales the zone-name table is recorded for. Only
// 'short' and 'long' vary by locale (verified against Node across every
// zone Go's tz database carries — 'shortOffset'/'longOffset' never do,
// which is why the runtime computes those from the raw UTC offset
// directly, the same for every locale); en-CA and en-GB each need their
// own table, in full, not a short list of named exceptions: en-GB's short
// names differ from en-US's for roughly a third of all zones (curated
// abbreviations for zones near the UK — "CET"/"CEST", "BST" — but GMT
// offsets, not en-US's "EST"/"PST" &c., for North American and most other
// zones en-US does have abbreviations for), and en-CA differs for a
// handful (Newfoundland's "NST"/"NDT", and a few "&"-vs-"and" spellings).
const zoneLocales = ['en-US', 'en-CA', 'en-GB'];

function zoneName(locale, at, timeZone, style) {
	return new Intl.DateTimeFormat(locale, { timeZone, timeZoneName: style }).formatToParts(at).find((part) => part.type === 'timeZoneName').value;
}

function offsetMinutes(at, timeZone) {
	const parts = Object.fromEntries(new Intl.DateTimeFormat('en-US', { timeZone, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric' }).formatToParts(at).map((part) => [part.type, part.value]));
	const local = Date.UTC(Number(parts.year), Number(parts.month) - 1, Number(parts.day), Number(parts.hour), Number(parts.minute), Number(parts.second));
	return Math.round((local - Math.floor(at / 1000) * 1000) / 60000);
}

function zonesGolden(names, locale) {
	const zones = {};
	for (const name of names) {
		let canonical;
		try {
			canonical = new Intl.DateTimeFormat(locale, { timeZone: name }).resolvedOptions().timeZone;
		} catch {
			continue; // Node does not know it (Go's "Factory"); the runtime refuses it too.
		}
		const at = (instant) => ({
			offset: offsetMinutes(instant, name),
			short: zoneName(locale, instant, name, 'short'),
			long: zoneName(locale, instant, name, 'long'),
			shortOffset: zoneName(locale, instant, name, 'shortOffset'),
			longOffset: zoneName(locale, instant, name, 'longOffset'),
		});
		zones[name] = { canonical, january: at(january), july: at(july) };
	}
	return zones;
}

// zoneInfo derives, per zone, the {canonical, longStd, longDst, shortStd,
// shortDst} tuple the embedded tables render: long/short names for
// whichever of January/July is the zone's standard time and whichever (if
// either differs) is its daylight time, each left empty when it equals the
// offset form Node falls back to (the runtime falls back the same way). A
// zone that keeps no daylight time in the recorded year borrows the
// daylight names of the zones sharing its standard names, when they agree,
// for the years in which it did keep one.
function zoneInfo(zones) {
	const info = {};
	for (const [name, zone] of Object.entries(zones)) {
		const [standard, daylight] = zone.january.offset <= zone.july.offset ? [zone.january, zone.july] : [zone.july, zone.january];
		const hasDaylight = zone.january.offset !== zone.july.offset;
		const keep = (value, fallback) => (value === fallback ? '' : value);
		info[name] = {
			canonical: zone.canonical,
			longStd: keep(standard.long, standard.longOffset),
			shortStd: keep(standard.short, standard.shortOffset),
			longDst: hasDaylight ? keep(daylight.long, daylight.longOffset) : null,
			shortDst: hasDaylight ? keep(daylight.short, daylight.shortOffset) : null,
		};
	}
	const daylightFor = new Map();
	for (const zone of Object.values(info)) {
		if (zone.longDst === null || zone.longStd === '') continue;
		const key = zone.longStd + '|' + zone.shortStd;
		const names = zone.longDst + '|' + zone.shortDst;
		if (!daylightFor.has(key)) daylightFor.set(key, new Set());
		daylightFor.get(key).add(names);
	}
	for (const zone of Object.values(info)) {
		if (zone.longDst === null) {
			const candidates = daylightFor.get(zone.longStd + '|' + zone.shortStd);
			const [longDst, shortDst] = candidates && candidates.size === 1 ? [...candidates][0].split('|') : ['', ''];
			zone.longDst = longDst;
			zone.shortDst = shortDst;
		}
	}
	return info;
}

// renderZoneGroups renders a zoneInfo() result the way intl.go's zone
// tables embed it: grouped by identical name tuple, an "@" line (long
// standard, long daylight, short standard, short daylight) followed by the
// zones sharing it.
function renderZoneGroups(info) {
	const groups = new Map();
	for (const [name, zone] of Object.entries(info)) {
		const key = [zone.longStd, zone.longDst, zone.shortStd, zone.shortDst].join('|');
		if (!groups.has(key)) groups.set(key, []);
		groups.get(key).push(zone.canonical === name ? name : name + '=' + zone.canonical);
	}
	const lines = [];
	for (const key of [...groups.keys()].sort()) {
		lines.push('@' + key);
		let line = '';
		for (const entry of groups.get(key)) {
			if (line && (line + ' ' + entry).length > 110) {
				lines.push(line);
				line = entry;
			} else {
				line = line ? line + ' ' + entry : entry;
			}
		}
		if (line) lines.push(line);
	}
	return lines.join('\n');
}

// zoneTable renders the base (en-US) table.
function zoneTable(zones) {
	return renderZoneGroups(zoneInfo(zones));
}

// sameZoneInfo reports whether a and b (zoneInfo() entries) render the
// same long and short names — the "no locale-specific override needed"
// test both zoneOverlay and zonesDiffering below are built from.
function sameZoneInfo(a, b) {
	return !!a && !!b && a.longStd === b.longStd && a.longDst === b.longDst && a.shortStd === b.shortStd && a.shortDst === b.shortDst;
}

// zoneOverlay renders only the zones where locale's info differs from
// en-US's (by canonical id and every rendered name — keyed here by the raw
// zone name zonesGolden was called with, matching info's own keys), which
// is what the runtime falls back from when a zone has no locale-specific
// entry. Small for en-CA, substantial for en-GB (see zoneLocales above).
function zoneOverlay(zones, base) {
	const info = zoneInfo(zones);
	const overlay = {};
	for (const [name, zone] of Object.entries(info)) {
		if (!sameZoneInfo(base[name], zone)) overlay[name] = zone;
	}
	return renderZoneGroups(overlay);
}

// zonesDiffering is zoneOverlay's own golden-shaped counterpart: the raw
// zonesGolden() entries (not zoneInfo()'s derived tuple) for the same
// zones zoneOverlay would render, so the Go test can tell "no override" —
// answer exactly as en-US does — from "override happens to equal the
// base" without recomputing the borrowing pass zoneInfo does.
function zonesDiffering(zones, base) {
	const info = zoneInfo(zones);
	const out = {};
	for (const [name, zone] of Object.entries(info)) {
		if (!sameZoneInfo(base[name], zone)) out[name] = zones[name];
	}
	return out;
}

// ---- Writing ----------------------------------------------------------------

const meta = {
	recordedWith: `node ${process.versions.node}, icu ${process.versions.icu}, cldr ${process.versions.cldr}, tz ${process.versions.tz}`,
	luxon: '3.7.2 (third_party/luxon/luxon.min.js)',
	note: 'Recorded by scripts/js-parity/record.mjs. CI compares against this file and never runs Node.',
};

const zoneNames = goZoneNames();
const zones = zonesGolden(zoneNames, 'en-US');
const zonesCA = zonesGolden(zoneNames, 'en-CA');
const zonesGB = zonesGolden(zoneNames, 'en-GB');
const zoneInfoUS = zoneInfo(zones);
const zonesCADiff = zonesDiffering(zonesCA, zoneInfoUS);
const zonesGBDiff = zonesDiffering(zonesGB, zoneInfoUS);
const files = {
	'luxon.json': { meta, probes: record(luxonProbes) },
	'dates.json': { meta, probes: record(dateProbes) },
	'date-options.json': { meta, instants, cases: sweep() },
	'numbers.json': { meta, probes: record(numberProbes), locales: numberSweep, currencyCodes, currencies: currencySweep },
	'currencies.json': { meta, ...currencySymbols },
	'weeks.json': { meta, ...weeks },
	'collation.json': { meta, probes: record(collationProbes) },
	'zones.json': { meta, january, july, zones, zonesCA: zonesCADiff, zonesGB: zonesGBDiff },
};

// serialize writes one probe, case or zone per line, so a golden stays small
// and its diff shows exactly what changed.
function serialize(content) {
	const lines = ['{'];
	const keys = Object.keys(content);
	keys.forEach((key, index) => {
		const value = content[key];
		const comma = index < keys.length - 1 ? ',' : '';
		const entries = Array.isArray(value) && key !== 'codes' && key !== 'digits' ? value.map((item) => JSON.stringify(item))
			: key === 'zones' || key === 'zonesCA' || key === 'zonesGB' || (key === 'locales' && !Array.isArray(value)) || key === 'languages' ? Object.keys(value).map((name) => JSON.stringify(name) + ': ' + JSON.stringify(value[name])) : null;
		if (!entries) {
			lines.push('\t' + JSON.stringify(key) + ': ' + JSON.stringify(value) + comma);
			return;
		}
		const [open, close] = Array.isArray(value) ? ['[', ']'] : ['{', '}'];
		lines.push('\t' + JSON.stringify(key) + ': ' + open);
		entries.forEach((entry, position) => lines.push('\t\t' + entry + (position < entries.length - 1 ? ',' : '')));
		lines.push('\t' + close + comma);
	});
	lines.push('}');
	return lines.join('\n') + '\n';
}

let drift = false;
fs.mkdirSync(out, { recursive: true });
for (const [name, content] of Object.entries(files)) {
	const text = serialize(content);
	const target = path.join(out, name);
	const previous = fs.existsSync(target) ? fs.readFileSync(target, 'utf8') : '';
	if (previous === text) continue;
	drift = true;
	console.log((check ? 'drift: ' : 'wrote: ') + path.relative(root, target));
	if (!check) fs.writeFileSync(target, text);
}

// replaceMarkedBlock rewrites the text between "// BEGIN <label>" and
// "// END <label>" in source to `const <constName> = \`\n<text>\n\`\n\n`,
// the same shape zoneTableText already used, and reports whether that
// changed anything.
function replaceMarkedBlock(source, label, constName, text) {
	const begin = source.indexOf('// BEGIN ' + label);
	const end = source.indexOf('// END ' + label);
	if (begin < 0 || end < 0) throw new Error(`record.mjs: intl.go has no ${label} markers`);
	const beginLine = source.indexOf('\n', begin) + 1;
	const block = `const ${constName} = \`\n${text}\n\`\n\n`;
	if (source.slice(beginLine, end) === block) return { source, changed: false };
	return { source: source.slice(0, beginLine) + block + source.slice(end), changed: true };
}

let source = fs.readFileSync(intlGo, 'utf8');
for (const [label, constName, text] of [
	['zone table', 'zoneTableText', zoneTable(zones)],
	// The en-CA and en-GB overlays: only the zones whose short or long
	// name differs from en-US's (zoneOverlay), which is most of them for
	// en-GB and a handful for en-CA — see zoneLocales above.
	['zone table (en-CA overlay)', 'zoneTableTextCA', zoneOverlay(zonesCA, zoneInfoUS)],
	['zone table (en-GB overlay)', 'zoneTableTextGB', zoneOverlay(zonesGB, zoneInfoUS)],
]) {
	const result = replaceMarkedBlock(source, label, constName, text);
	source = result.source;
	if (result.changed) {
		drift = true;
		console.log((check ? 'drift: ' : 'wrote: ') + `internal/jsrun/intl.go (${label})`);
	}
}
if (!check) fs.writeFileSync(intlGo, source);
if (check && drift) process.exitCode = 1;
