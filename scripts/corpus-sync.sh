#!/usr/bin/env bash
#
# Materialise the n8n importer regression corpus into a gitignored directory.
#
# The corpus is measured, never committed. Its two third-party sources may not
# live in this repository: n8n's own fixtures are LicenseRef-n8n-sustainable-use,
# and the WAHA templates repository carries no licence at all, which is stricter
# still. See .pine/memory/licensing.md.
#
# Usage:
#   KILASFLOW_N8N_REFERENCE=/path/to/n8n scripts/corpus-sync.sh
#   KILASFLOW_N8N_REFERENCE=/path/to/n8n scripts/corpus-sync.sh --update
#
#   --update  regenerate MANIFEST.json from what was fetched, instead of
#             verifying against it. Use only when deliberately re-pinning.
#
# Environment:
#   KILASFLOW_N8N_REFERENCE  path to the read-only n8n reference checkout.
#                            Required. Deliberately has no default: a machine
#                            path committed here would make foreign source a
#                            build input.
#   KILASFLOW_CORPUS_DIR     where fixtures land (default: <repo>/.corpus).
#   KILASFLOW_CORPUS_CACHE   where upstream clones are kept
#                            (default: <repo>/.corpus-cache).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="${REPO_ROOT}/internal/interop/n8n/corpus/MANIFEST.json"
CORPUS_DIR="${KILASFLOW_CORPUS_DIR:-${REPO_ROOT}/.corpus}"
CACHE_DIR="${KILASFLOW_CORPUS_CACHE:-${REPO_ROOT}/.corpus-cache}"

# Pinned upstream. A moving reference makes the baseline unfalsifiable.
WAHA_TEMPLATES_REPO="https://github.com/devlikeapro/waha-n8n-templates.git"
WAHA_TEMPLATES_COMMIT="1bd5536eef88f81692daaaba74d3bf6d621fb94f"
N8N_COMMIT="40dfa42ced26ba6fe01e511ec685f01ea77c0a83"

UPDATE=0
for argument in "$@"; do
	case "$argument" in
	--update) UPDATE=1 ;;
	*)
		echo "unknown argument: $argument" >&2
		exit 2
		;;
	esac
done

if [[ -z "${KILASFLOW_N8N_REFERENCE:-}" ]]; then
	cat >&2 <<'MESSAGE'
KILASFLOW_N8N_REFERENCE is not set.

It must point at the read-only n8n reference checkout, which lives outside this
repository and is never a build input. See .pine/memory/n8n-reference.md for the
location on this machine.

  KILASFLOW_N8N_REFERENCE=/path/to/n8n make corpus
MESSAGE
	exit 2
fi

N8N_REFERENCE="${KILASFLOW_N8N_REFERENCE%/}"
if [[ ! -d "${N8N_REFERENCE}/.git" ]]; then
	echo "KILASFLOW_N8N_REFERENCE=${N8N_REFERENCE} is not a git checkout" >&2
	exit 2
fi

actual_n8n_commit="$(git -C "${N8N_REFERENCE}" rev-parse HEAD)"
if [[ "${actual_n8n_commit}" != "${N8N_COMMIT}" ]]; then
	echo "reference checkout is at ${actual_n8n_commit}, corpus is pinned to ${N8N_COMMIT}" >&2
	echo "check out the pinned commit, or re-pin with --update and explain the baseline shift" >&2
	exit 1
fi

# --- Fetch the WAHA templates at the pinned commit -------------------------

mkdir -p "${CACHE_DIR}"
WAHA_TEMPLATES_DIR="${CACHE_DIR}/waha-n8n-templates"
if [[ ! -d "${WAHA_TEMPLATES_DIR}/.git" ]]; then
	echo "cloning ${WAHA_TEMPLATES_REPO}"
	git clone --quiet "${WAHA_TEMPLATES_REPO}" "${WAHA_TEMPLATES_DIR}"
fi
git -C "${WAHA_TEMPLATES_DIR}" fetch --quiet origin "${WAHA_TEMPLATES_COMMIT}" 2>/dev/null ||
	git -C "${WAHA_TEMPLATES_DIR}" fetch --quiet origin
git -C "${WAHA_TEMPLATES_DIR}" checkout --quiet "${WAHA_TEMPLATES_COMMIT}"

# --- Collect ---------------------------------------------------------------

rm -rf "${CORPUS_DIR}"
mkdir -p "${CORPUS_DIR}/waha-templates" "${CORPUS_DIR}/nodes-base"

# An n8n workflow document has both `nodes` and `connections`. The WAHA
# templates directory also holds two Typebot exports, which are not workflows
# and must not be counted; testing the shape is more durable than listing them.
is_workflow_document() {
	python3 - "$1" <<'PYTHON'
import json
import sys

try:
    with open(sys.argv[1], "rb") as handle:
        document = json.load(handle)
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(document, dict) and isinstance(document.get("nodes"), list) and "connections" in document else 1)
PYTHON
}

waha_count=0
while IFS= read -r source; do
	if ! is_workflow_document "${source}"; then
		continue
	fi
	relative="${source#"${WAHA_TEMPLATES_DIR}"/}"
	destination="${CORPUS_DIR}/waha-templates/${relative}"
	mkdir -p "$(dirname "${destination}")"
	cp "${source}" "${destination}"
	waha_count=$((waha_count + 1))
done < <(find "${WAHA_TEMPLATES_DIR}" -name '*.json' -not -path '*/.git/*' | sort)

base_count=0
for node in HttpRequest If Set; do
	while IFS= read -r source; do
		case "${source}" in
		*.node.json) continue ;;
		esac
		if ! is_workflow_document "${source}"; then
			continue
		fi
		relative="${source#"${N8N_REFERENCE}"/packages/nodes-base/nodes/}"
		destination="${CORPUS_DIR}/nodes-base/${relative}"
		mkdir -p "$(dirname "${destination}")"
		cp "${source}" "${destination}"
		base_count=$((base_count + 1))
	done < <(find "${N8N_REFERENCE}/packages/nodes-base/nodes/${node}/test" -name '*.json' | sort)
done

echo "collected ${waha_count} WAHA template workflows and ${base_count} nodes-base fixtures"

# --- Verify or re-pin ------------------------------------------------------

python3 - "${CORPUS_DIR}" "${MANIFEST}" "${UPDATE}" "${WAHA_TEMPLATES_COMMIT}" "${N8N_COMMIT}" <<'PYTHON'
import hashlib
import json
import os
import sys

corpus_dir, manifest_path, update, waha_commit, n8n_commit = sys.argv[1:6]
update = update == "1"

entries = []
for root, _, names in os.walk(corpus_dir):
    for name in sorted(names):
        if not name.endswith(".json"):
            continue
        path = os.path.join(root, name)
        relative = os.path.relpath(path, corpus_dir)
        with open(path, "rb") as handle:
            digest = hashlib.sha256(handle.read()).hexdigest()
        entries.append({"path": relative.replace(os.sep, "/"), "sha256": digest})
entries.sort(key=lambda entry: entry["path"])

if update:
    manifest = {
        "comment": (
            "Pins for the n8n importer regression corpus. The fixtures themselves are "
            "third-party and are never committed: n8n's are LicenseRef-n8n-sustainable-use "
            "and the WAHA templates repository carries no licence at all. Only these "
            "digests are. Regenerate with scripts/corpus-sync.sh --update, and explain the "
            "baseline shift when you do."
        ),
        "sources": {
            "waha-templates": {
                "repository": "https://github.com/devlikeapro/waha-n8n-templates",
                "commit": waha_commit,
                "licence": "none declared (GitHub reports license: null) — never committed",
            },
            "nodes-base": {
                "origin": "the read-only n8n reference checkout, via KILASFLOW_N8N_REFERENCE",
                "commit": n8n_commit,
                "licence": "LicenseRef-n8n-sustainable-use — never committed",
            },
        },
        "fixtures": entries,
    }
    with open(manifest_path, "w") as handle:
        json.dump(manifest, handle, indent=2)
        handle.write("\n")
    print(f"re-pinned {len(entries)} fixtures into {os.path.relpath(manifest_path)}")
    sys.exit(0)

if not os.path.exists(manifest_path):
    print(f"no manifest at {manifest_path}; run with --update to create one", file=sys.stderr)
    sys.exit(1)

with open(manifest_path) as handle:
    manifest = json.load(handle)

expected = {entry["path"]: entry["sha256"] for entry in manifest["fixtures"]}
actual = {entry["path"]: entry["sha256"] for entry in entries}

problems = []
for path, digest in sorted(expected.items()):
    if path not in actual:
        problems.append(f"missing: {path}")
    elif actual[path] != digest:
        problems.append(f"changed: {path}\n    manifest {digest}\n    fetched  {actual[path]}")
for path in sorted(set(actual) - set(expected)):
    problems.append(f"unexpected: {path}")

if problems:
    print("corpus does not match the manifest:", file=sys.stderr)
    for problem in problems:
        print(f"  {problem}", file=sys.stderr)
    print(
        "\nUpstream changed, or the pin is wrong. Re-pinning with --update shifts the\n"
        "baseline, so do it deliberately and say why in the commit.",
        file=sys.stderr,
    )
    sys.exit(1)

print(f"verified {len(entries)} fixtures against the manifest")
PYTHON

echo "corpus ready at ${CORPUS_DIR}"
