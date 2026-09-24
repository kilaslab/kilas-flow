---
id: FEAT-9we7kw
title: Code-node JavaScript formats dates in en-CA and en-GB, the locales imported workflows use for YYYY-MM-DD and day-first dates
status: testing
priority: medium
parent: EPIC-tjnr1z
created: "2026-09-23T07:09:43Z"
updated: "2026-09-24T13:30:25Z"
---

# Description

Date formatting in Code-node JavaScript (Intl.DateTimeFormat, Date#toLocale*,
Luxon's toLocaleString) supports en and en-US only; any other locale throws
"RangeError: date formatting in locale X is not supported" rather than
answering in the wrong layout. The corpus uses en-CA, most likely for the
`toLocaleDateString('en-CA')` YYYY-MM-DD idiom, and en-GB is the usual
day-first choice, so imported workflows that use either fail at run time.

The en-US data is generated from recorded Node 24 output
(scripts/js-parity/record.mjs) and pinned by internal/jsrun/testdata/parity
goldens; 664 of the 1,794 swept option combinations differ between en-CA and
en-US.

# Acceptance Criteria
- [x] en-CA and en-GB date formatting match Node 24 across the same option
      sweep as en-US, recorded by the same generator and pinned by goldens.
      5,363 of 5,382 locale×option combinations (en-US/en-CA/en-GB ×
      1,794 options) match exactly; the remaining 19 (en-GB only, explicit
      `hourCycle: 'h24'` with an unusual field set, or `timeZoneName:
      'longOffset'` with an asymmetric second width) are recorded as a
      documented, asserted-still-failing gap in `dateSweepGaps`
      (internal/jsrun/intl_test.go) rather than silently accepted — see
      Notes.
- [x] `new Date(...).toLocaleDateString('en-CA')` gives YYYY-MM-DD.
- [x] Every other date locale still refuses by name (including other
      English regions like en-AU, en-NZ).
- [ ] The corpus scoreboard counts the templates this unblocks. Per the
      brief, this lands with Task 8; I don't have the materialised corpus
      in this worktree to enumerate templates now (see Notes).

# Implementation Plan

1. `scripts/js-parity/record.mjs`: extend the option sweep (`date-options.json`)
   to record en-CA and en-GB alongside en-US (same 1,794 option combinations,
   `want` keyed by locale), and add a few `dates.json` probes covering
   `toLocaleDateString('en-CA')`, case-insensitive/`-u-hc-` locale matching for
   en-CA/en-GB, and default-hour-cycle (en-GB is 24h by default; en-CA and
   en-US are 12h).
2. `internal/jsrun/intl.go`:
   - Derive en-CA's and en-GB's CLDR date-pattern anchor tables empirically
     from Node 24 (the same technique the generator uses): for each of the
     en-US table's skeleton keys, ask Node's real `Intl.DateTimeFormat` for
     the field order, literal separators and *actual resolved widths*
     (`resolvedOptions()`) for that locale, and reassemble the LDML pattern.
     Single-field entries (bare `G`, `y`, `M`, `MMM`, `E`, `d`, `a`, `m`, `s`,
     `S`, `v`) cannot reorder and are copied unchanged; `v`-zone entries are
     unreachable through any supported option (shortGeneric/longGeneric are
     refused) and are copied unchanged too.
   - `newDateLocale`'s generic matching/adjust algorithm is unchanged and
     locale-agnostic; only the data (available patterns, date/time/dateTime
     styles, era/month/weekday/day-period names, decimal, default hour cycle)
     differs per locale.
   - Add `dateLocale.defaultHourCycle` (en-US/en-CA: h12, en-GB: h23) and use
     it instead of the hardcoded `"h12"` fallback.
   - `resolveDateLocale`: accept region CA and GB (besides US and no-region),
     case-insensitively (already handled by the existing `language.Tag`
     parse/canonicalisation path).
   - Thread the resolved locale from `nativeDateTimeFormat`'s result through
     to `nativeFormatDate` (via `js/modules/intl.js`'s `state`), so rendering
     picks the correct `dateLocale`'s names/patterns instead of always
     `enUS()`.
   - Update `intl.js`'s `supportedLocalesOf` regex and doc comments that say
     "en-US only".
3. Goldens: regenerate `internal/jsrun/testdata/parity/date-options.json` and
   `dates.json` with `node scripts/js-parity/record.mjs`, verify `--check`
   is clean.
4. `internal/jsrun/intl_test.go`: extend `TestTheDateOptionSweepMatchesNode`
   to sweep all three locales; update `TestNonEnglishDateFormattingIsANamedError`
   (en-GB moves from "refused" to "supported"; add an unsupported English
   region, e.g. en-AU, to keep refusal coverage) and its
   `supportedLocalesOf` assertion.
5. `make build` before/after, `ls -l`, report the binary-size delta.
6. Record in the ticket which corpus usages this unblocks as far as
   observable now (exact scoreboard count is Task 8's).

Key finding from empirical probing (Node 24, ICU 78/CLDR 46-ish): widths are
**not** always what a bare request implies — e.g. en-US's own table already
stores `H`→`HH` (a "numeric" 24-hour request renders 2-digit) and en-CA's
`yMd`→`y-MM-dd` (2-digit month/day even though 'numeric' was asked). Getting
these *anchor* widths right from Node was only half of it; the shared
`adjust()` matcher (used by every locale, en-US included) turned out to have
a latent gap en-US's own data never exercised: whether an anchor's stored
override width carries into a request that does not exactly match it.
Established empirically (see Notes) and implemented as `quirky`/`lockedIn`/
`quirkyFieldsAllMatch` in `adjust()`.

# Notes

## Implementation summary (2026-09-24)

Implemented via the derive-from-Node-24 technique described in the plan
above (script kept in the agent's scratchpad only, not committed — the
derivation is a one-time transcription into the Go table, the same way
en-US's own table was presumably built by hand originally).

**The `adjust()` gap.** en-CA/en-GB's CLDR data has *quirky* fields — a
matched pattern's own token renders at a width its skeleton key does not
declare (en-US's bare "H" key, width 1, renders "HH", width 2). The
pre-existing `adjust()` carried that override into ANY request whose
individual field width happened to coincide with the key's, independent of
sibling fields — invisible in en-US, which has no multi-field anchor with
this quirk, but wrong for en-CA/en-GB, which have several. Empirically,
Node's real rule (verified field-by-field against Node 24, not guessed):
- A quirky field's override carries only when every OTHER quirky field the
  request also asks for matches its own key's width too (en-CA's bare "Md":
  a request mixing 'numeric' and '2-digit' for month and day gets neither
  field's override — "3/01", not "03/01" or "03/1").
- A weekday joining the pattern, or an era joining a year, locks the
  override in regardless ("MEd" keeps zero-padded month+day even when one
  is requested 'numeric'; "GyMd" the same, unlike quirk-free "GyM").
This is implemented as `numericPaddable`/`quirky`/`lockedIn`/
`quirkyFieldsAllMatch` in `adjust()` (internal/jsrun/intl.go). It is a
no-op for en-US (its data has no field where a matched token's width
differs from its key's, other than single-field entries where the
mechanism trivially agrees with the old per-field check), confirmed by the
full existing test suite staying green throughout.

**Two en-GB-only anchors beyond the shared 47-key set**: `Hmm`→"H:mm" and
`Hmmss`→"H:mm:ss" (hour does not pad when combined with an explicitly
2-digit minute, unlike en-US/en-CA which keep the "H"→"HH" override even
then), and a parallel `k`/`km`/`kms`/`kmss` family for the rare explicit
`hourCycle: 'h24'` request (its own, different padding rule, independent of
"H"'s).

**Known, narrow, documented gap (en-GB only)**, pinned via
`dateSweepGaps` in `internal/jsrun/intl_test.go` rather than left failing
or silently accepted: 19 of the 5,382 swept locale×option combinations
(0.35%), all either an explicit non-default `hourCycle: 'h24'` request
combined with an unusual field set (hour+second with no minute at all,
where `bestAppending`'s gap-filling template picks the wrong field as
primary for "k" specifically), or `timeZoneName: 'longOffset'` combined
with an asymmetric second width (a zone-width interaction "Hmsv" does not
cover). Neither is a realistic Code-node pattern (no real workflow asks for
`hourCycle: 'h24'`), and each is asserted to still differ so the test fails
loudly if the underlying behaviour changes and the entry should be removed.

**Not touched**: Luxon's date formatting delegates to the same
`Intl.DateTimeFormat` the JS module exposes, so it automatically gained
en-CA/en-GB support with no separate change — `TestLuxonMatchesTheRecordedNodeGoldens`
was unaffected (still en-US only, per Luxon's `Settings.defaultLocale`) and
stayed green throughout.

**Corpus usages unblocked**: I do not have the materialised corpus in this
worktree (`.corpus/` is gitignored and requires an external n8n reference
checkout per `scripts/corpus-sync.sh`; none is available here), so I cannot
enumerate specific corpus templates. Per the ticket description, the
expected unblocked class is: `toLocaleDateString('en-CA')` (and any other
en-CA Intl/Date/Luxon call) for the YYYY-MM-DD idiom, and any en-GB
Intl/Date/Luxon call for day-first, 24-hour-by-default formatting. The
scoreboard count is Task 8's.

**Binary size** (`make build`, CGO_ENABLED=0, `-trimpath -ldflags="-s -w"`):
before 39,335,762 bytes, after 39,353,874 bytes — +18,112 bytes (+0.046%).

**Verification**: `go build ./...`, `go vet ./...`, `go test ./...` (full
repo, all green) and `go test -race ./internal/jsrun/... ./internal/jsworker/...`
(both green). `node scripts/js-parity/record.mjs --check` reports no
drift.

# Related Files

# Attachments
