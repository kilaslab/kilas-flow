#!/bin/sh
# Fails when a published coordinate names a GitHub account the project does not
# own.
#
# The repository lives at github.com/kilaslab/kilas-flow. Its module path, image
# name and docs all once named an account nobody had registered, one letter away
# from the real owner's. Until that name was removed, a `go get` for the module
# path, a `docker pull` of the image the install guide tells operators to pin, and
# a clone of the repository URL all resolved through GitHub's rename redirects,
# and would have resolved to an attacker's repository the moment the name was
# claimed. The Go module path was the last of those to move (BUG-341sxn).
#
# What this checks:
#   1. No tracked file names that account, the module path in go.mod and every
#      import of it included.
#   2. The image and source coordinates in the Makefile are the canonical ones,
#      so a rewrite cannot quietly point the release pipeline elsewhere.
#
# Two things here are deliberate:
#
#   - `.pine/` is excluded from the scan: its tickets are the historical record
#     of the rename and quote the old path on purpose, to say what was moved.
#   - The name is assembled from two pieces below rather than written out,
#     because this file is inside its own scan. The comment on the Makefile
#     target explains why an exception for the guard's own prose would weaken it.
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_dir"

# The scan reads tracked files through git, so a directory added later is covered
# without editing a list here, and so a stale build artefact in the working tree
# (bin/, docs/dist/) cannot fail the check with a string nobody publishes.
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	echo "check-coordinates: not a git work tree; the namespace scan needs one" >&2
	exit 2
fi

unowned="kilaslab""s"
canonical_image='ghcr.io/kilaslab/kilasflow'
canonical_source='https://github.com/kilaslab/kilas-flow'

status=0

# 1. The unowned account name. `-I` skips binary files.
if found=$(git grep -nI -e "$unowned" -- . ':(exclude).pine'); then
	echo "check-coordinates: the unowned account name is still referenced:" >&2
	echo "$found" >&2
	status=1
fi

# 2. The canonical coordinates, checked as values rather than as prose.
if ! grep -q "IMAGE  *?= *$canonical_image\$" Makefile; then
	echo "check-coordinates: Makefile IMAGE is not $canonical_image" >&2
	status=1
fi
if ! grep -q "SOURCE_URL  *?= *$canonical_source\$" Makefile; then
	echo "check-coordinates: Makefile SOURCE_URL is not $canonical_source" >&2
	status=1
fi

if [ "$status" -eq 0 ]; then
	echo "check-coordinates: every published coordinate names $canonical_image's owner"
fi

exit "$status"
