---
id: BUG-a9d2hb
title: Intl zone names for Europe/Dublin are swapped on Linux, where Debian's tzdata stores winter as negative daylight time
status: testing
priority: low
parent: EPIC-tjnr1z
created: "2026-09-24T13:54:27Z"
updated: "2026-09-24T13:54:27Z"
---

# Description

`internal/jsrun`'s `TestZoneNamesMatchNodeForEveryZone` fails on Linux in
the golang:1.27-bookworm image. It fails on main too (checked 2026-09-24,
main at 1c51601), not only on a branch. For Europe/Dublin and Eire, the
winter and summer names come out swapped: January gives "GMT+0" / "Irish
Standard Time" where Node gives "GMT" / "Greenwich Mean Time", and July the
other way round.

Debian's tzdata stores Dublin in the "vanguard" form, where winter is the
negative daylight saving time. Go's embedded data and macOS store it the
other way. Found while running FEAT-21h6xp's Linux container matrix; it
passes on macOS.

# Acceptance Criteria
- [x] Dublin's names match Node's whichever tzdata form the system has, or
      the test pins the zone database it compares against.

# Notes

## Plan

Name the higher of the two offsets in a saving pair as daylight, instead of
trusting the tzdata isdst flag. Do not import `time/tzdata`: that would pin
every zone to Go's rearguard copy and grow a small binary by 413008 bytes
(plain 1938802, with the import 2351810; the zip itself is 408467). The
system database stays the source of the offsets, which already agree across
forms. An interval with no further transition keeps the database flag, so a
settled standard change is not read as a saving.

## Progress (2026-09-25)

`onDaylightTime` compares the instant's offset with the neighbouring interval
the file flags the other way, and uses the daylight name only when this
offset is the higher one. Europe/Dublin and Eire then print GMT / Greenwich
Mean Time in January and GMT+1 / Irish Standard Time in July on both forms.
A synthetic version-1 TZif holds each form, so the check does not depend on
the host database.

Africa/Casablanca and Africa/El_Aaiun are the other negative-saving zones.
Their table entries have no long or short name, so January and July render
as the offset (GMT+1 in 2026, outside Ramadan). Both forms describe that
same offset. They match the Node golden on macOS (rearguard) and on Debian
(vanguard).

Africa/Windhoek on Debian has sat at +02 since 2017 (standard), with the
previous interval an hour lower and flagged as a saving. Treating that
history as the other half of a pair turned Central Africa Time into
GMT+02:00. An interval that does not end keeps the database's own flag.

`TestZoneNamesMatchNodeForEveryZone` is the check that no other zone's
January or July name moved. It passes on macOS, in `golang:1.27-bookworm`,
and in `gcr.io/distroless/static-debian12:nonroot`. The stock distroless
image does carry Debian tzdata: a probe there shows Dublin's January isdst
set and Windhoek's previous offset at +01, the vanguard shape. The zone-name
tests were run as that image's entrypoint against its own zoneinfo.

# Related Files
- internal/jsrun/intl.go
- internal/jsrun/intl_test.go
