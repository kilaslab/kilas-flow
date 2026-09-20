#!/bin/sh
# Fails when a published coordinate names a GitHub account the project does not
# own.
#
# The repository lives at github.com/kilaslab/kilas-flow, but its module path,
# image name and docs once said "kilaslabs" — an account name nobody has
# registered. Until it is claimed, anyone can: a `go get` for the module path, a
# `docker pull` of the image the install guide tells operators to pin, and a
# clone of the repository URL all resolve through GitHub's rename redirects
# today and would resolve to an attacker's repository the moment the name is
# taken.
#
# What this checks:
#   1. No file in the list below mentions the unowned namespace.
#   2. The image and source coordinates in the Makefile are the canonical ones,
#      so a rewrite cannot quietly point the release pipeline elsewhere.
#
# What it deliberately does not check: the Go module path
# (github.com/kilaslabs/kilas-flow) in go.mod, *.go and the community-nodes page
# that documents the import. Renaming it is an atomic 233-file sweep that needs
# a quiet tree — see BUG-341sxn — and a guard that failed on every Go file until
# that lands would be a guard somebody disables.
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_dir"

unowned='kilaslabs'

# Files whose contents a consumer or a release pipeline reads as a coordinate.
files='Makefile Dockerfile compose.yaml compose.postgres.yaml .env.example README.md sdk/package.json sdk/README.md .github/workflows'

status=0

# 1. The unowned namespace.
if found=$(grep -rn --include='*' -e "$unowned" $files sdk/examples docs/src 2>/dev/null \
	| grep -v '^docs/src/content/docs/guides/community-nodes.md:'); then
	echo "check-coordinates: the unowned namespace '$unowned' is still referenced:" >&2
	echo "$found" >&2
	status=1
fi

# 2. The canonical coordinates, checked as values rather than as prose.
canonical_image='ghcr.io/kilaslab/kilasflow'
canonical_source='https://github.com/kilaslab/kilas-flow'

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
