---
id: BUG-a9d2hb
title: Intl zone names for Europe/Dublin are swapped on Linux, where Debian's tzdata stores winter as negative daylight time
status: todo
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
- [ ] Dublin's names match Node's whichever tzdata form the system has, or
      the test pins the zone database it compares against.

# Notes

# Related Files
- internal/jsrun/intl.go
- internal/jsrun/intl_test.go
