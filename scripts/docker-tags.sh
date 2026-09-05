#!/bin/sh
# Print the `docker buildx --tag` arguments for one release.
#
# The tag vocabulary lives here and only here, so that `make docker-release`, the
# release workflow and a human reproducing a published image cannot disagree about
# what a version tag is supposed to produce.
#
#   <image>:v1.4.2   The exact version. Immutable — it is never republished, and it
#                    is the tag a production deployment should pin. Nothing else
#                    here is safe to pin, because everything else moves.
#   <image>:v1.4     The minor series. Moves forward onto every later patch of
#                    v1.4, so a deployment tracking it picks up fixes without
#                    picking up features.
#   <image>:latest   The newest stable release. A convenience for trying the
#                    product; never a deployment target, because the next release
#                    can change anything under it.
#
# A prerelease — any version carrying a `-` suffix, such as v1.5.0-rc.1 — gets its
# exact tag and nothing else. Moving `v1.5` or `latest` onto a release candidate
# would hand an untested build to everyone who asked for the stable series, which
# is the whole reason those two tags are separated from the exact one.
#
# The image and version arrive in the environment, not as arguments, and that is
# deliberate. A git tag may legally contain a single quote, so interpolating one
# into a Make recipe would let a tag close the quoting and run a command inside
# the release pipeline. Make writes a target-specific export straight into the
# child's environment, where no shell parses it. Arguments are still accepted so
# a human can run this by hand to see what a version would publish.
#
# Usage: KILASFLOW_IMAGE=… KILASFLOW_VERSION=… scripts/docker-tags.sh
#    or: scripts/docker-tags.sh <image> <version>
set -eu

usage() {
	echo "usage: docker-tags.sh <image> <version>, or set KILASFLOW_IMAGE and KILASFLOW_VERSION" >&2
	exit 2
}

image=${1:-${KILASFLOW_IMAGE:-}}
version=${2:-${KILASFLOW_VERSION:-}}
[ -n "$image" ] || usage
[ -n "$version" ] || usage

# A mistyped or non-release tag must fail here rather than reach the registry.
# Publishing `latest` from a version this script did not understand is the failure
# worth spending these twenty lines to prevent.
reject() {
	echo "docker-tags: '$version' is not a vMAJOR.MINOR.PATCH release tag" >&2
	exit 1
}

is_number() {
	case "$1" in
	'' | *[!0-9]*) return 1 ;;
	esac
	return 0
}

core=${version%%-*}
prerelease=${version#"$core"}

numbers=${core#v}
[ "$numbers" != "$core" ] || reject

old_ifs=$IFS
IFS='.'
# Intentionally unquoted: splitting on the IFS just set is the point.
# shellcheck disable=SC2086
set -- $numbers
IFS=$old_ifs

[ "$#" -eq 3 ] || reject
is_number "$1" || reject
is_number "$2" || reject
is_number "$3" || reject

# One argument per line, so the caller's word splitting has nothing subtle to get
# wrong.
printf -- '--tag %s:%s\n' "$image" "$version"

if [ -z "$prerelease" ]; then
	printf -- '--tag %s:v%s.%s\n' "$image" "$1" "$2"
	printf -- '--tag %s:latest\n' "$image"
fi
