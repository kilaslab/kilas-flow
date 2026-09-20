---
id: BUG-341sxn
title: Rename the Go module path off the unclaimed kilaslabs namespace
status: todo
priority: high
labels:
    - dx
    - security
    - deferred
parent: EPIC-cfe7ny
created: "2026-09-20T00:39:41Z"
updated: "2026-09-20T00:39:41Z"
---

Source: FEAT-edxxj7 finding "All published coordinates point at an unowned GitHub namespace kilaslabs". The distribution
coordinates were moved to the real owner (`kilaslab`, see FEAT-edxxj7) but the Go module path cannot be moved in the
same wave.

Deferred by decision (Main, 2026-09-20): `module github.com/kilaslabs/kilas-flow` in go.mod appears in 233 .go files as
an import path. A rename is an atomic repo-wide sweep, and with ~10 sibling agents holding uncommitted Go edits a
half-applied sweep is a guaranteed build break — the same failure mode as the LoadOptions field deletion earlier in
this epic, where one commit removed a struct field other slices still referenced. It needs a quiet tree, one owner, and
no concurrent Go writers.

Impact while it is deferred: `go get github.com/kilaslabs/kilas-flow/pkg/sdk` for community node authors resolves only
through GitHub's rename redirect for the repository, and `github.com/kilaslabs` is an unclaimed account name, so whoever
registers it could serve that module path. docs/guides/community-nodes.md documents the import path, so the doc and
go.mod must be changed together.

Work:
1. Confirm `github.com/kilaslabs/kilas-flow` cannot be claimed by anyone else (claim the org, or accept the risk).
2. `gofmt`-safe sweep: go.mod, every import in .go files, docs/guides/community-nodes.md, docs/reference/* that quote
   the path, sdk/ if it references it, and any `-ldflags`/build metadata.
3. `go build ./... && go vet ./... && go test ./...` on a quiet tree, plus `make docs-build` for the links.
4. Add the module path to the coordinates guard added by FEAT-edxxj7 (scripts/check-coordinates.sh excludes it today).

Acceptance:
- `grep -rn "kilaslabs" --include="*.go" --include="go.mod" .` returns nothing.
- The tree builds, tests pass, docs links resolve.
- The coordinates guard covers the module path with no exclusion.
