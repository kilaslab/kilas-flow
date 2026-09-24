#!/usr/bin/env bash
#
# Materialise the Code-node compatibility corpus into a gitignored directory.
#
# The corpus is the JavaScript of real n8n templates: every JavaScript Code
# node and every Sort node with a code comparator among the most-viewed
# templates on api.n8n.io. It measures how much of that code the embedded
# runtime (internal/jsrun) parses, accepts and runs. The bodies are other
# people's work, published as templates, so they are measured and never
# committed: only their digests are, in internal/jsrun/corpus/MANIFEST.json.
# See .pine/memory/licensing.md.
#
# Usage:
#   scripts/code-corpus-sync.sh              fetch the pinned templates and verify
#   scripts/code-corpus-sync.sh --update     rank the top N again and re-pin
#   scripts/code-corpus-sync.sh --update --top 200
#
#   --update  rank the N most-viewed templates now and rewrite MANIFEST.json
#             from what was fetched, instead of verifying against it. The
#             ranking moves every day, so this shifts the baseline: do it
#             deliberately and say why in the commit.
#   --top N   how many templates to rank with --update (default 500).
#
# Environment:
#   KILASFLOW_CODE_CORPUS_DIR  where fixtures land
#                              (default: <repo>/internal/jsrun/corpus/fixtures).
#   KILASFLOW_N8N_TEMPLATES_API  the template API
#                              (default: https://api.n8n.io/api/templates).
#
# Exit status: 0 when the corpus is ready, 1 when it does not match the pins,
# 2 on a usage error, and 3 when the template API could not be reached, so a
# scheduled job can tell an outage from a changed template.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="${REPO_ROOT}/internal/jsrun/corpus/MANIFEST.json"
CORPUS_DIR="${KILASFLOW_CODE_CORPUS_DIR:-${REPO_ROOT}/internal/jsrun/corpus/fixtures}"
API="${KILASFLOW_N8N_TEMPLATES_API:-https://api.n8n.io/api/templates}"

UPDATE=0
TOP=500
while [[ $# -gt 0 ]]; do
	case "$1" in
	--update) UPDATE=1 ;;
	--top)
		shift
		TOP="${1:-}"
		if ! [[ "${TOP}" =~ ^[1-9][0-9]*$ ]]; then
			echo "--top needs a positive number" >&2
			exit 2
		fi
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 2
		;;
	esac
	shift
done

python3 - "${CORPUS_DIR}" "${MANIFEST}" "${UPDATE}" "${TOP}" "${API}" <<'PYTHON'
import concurrent.futures
import hashlib
import json
import os
import shutil
import sys
import time
import urllib.error
import urllib.request

corpus_dir, manifest_path, update, top, api = sys.argv[1:6]
update = update == "1"
top = int(top)
PAGE = 100
UNREACHABLE = 3


class Unreachable(Exception):
    pass


def get(url):
    """GET url as JSON, retrying transient failures. A failure that outlasts
    the retries means the API is unreachable, which is not the same verdict
    as a template that changed."""
    last = None
    for attempt in range(4):
        try:
            request = urllib.request.Request(url, headers={"User-Agent": "kilasflow-code-corpus-sync"})
            with urllib.request.urlopen(request, timeout=60) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            if error.code == 404:
                raise LookupError(url)
            last = error
        except (urllib.error.URLError, TimeoutError, ConnectionError, json.JSONDecodeError) as error:
            last = error
        time.sleep(1 + attempt * 2)
    raise Unreachable(f"{url}: {last}")


def is_javascript_code(node):
    if node.get("type") != "n8n-nodes-base.code":
        return False
    language = (node.get("parameters") or {}).get("language", "")
    return language in ("", "javaScript")


def is_python_code(node):
    return node.get("type") == "n8n-nodes-base.code" and not is_javascript_code(node)


def is_code_comparator(node):
    return node.get("type") == "n8n-nodes-base.sort" and (node.get("parameters") or {}).get("type") == "code"


def fixture_for(template_id):
    """The fixture for one template: only what a run of its code needs. The
    rest of the template (descriptions, view counts, credentials' names) is
    left behind, so a digest moves only when the code or its data does."""
    document = get(f"{api}/workflows/{template_id}")
    workflow = ((document.get("workflow") or {}).get("workflow")) or {}
    nodes = workflow.get("nodes") or []
    code = []
    python = 0
    for index, node in enumerate(nodes):
        if is_python_code(node):
            python += 1
        if not (is_javascript_code(node) or is_code_comparator(node)):
            continue
        code.append({
            "index": index,
            "name": node.get("name", ""),
            "type": node.get("type"),
            "typeVersion": node.get("typeVersion", 1),
            "parameters": node.get("parameters") or {},
        })
    if not code:
        return None, python
    fixture = {
        "template": int(template_id),
        "nodes": code,
        "nodeNames": [node.get("name", "") for node in nodes],
        "connections": workflow.get("connections") or {},
        "pinData": workflow.get("pinData") or {},
    }
    return fixture, python


def encode(fixture):
    return (json.dumps(fixture, indent=2, sort_keys=True, ensure_ascii=False) + "\n").encode("utf-8")


def ranked_ids(count):
    ids = []
    page = 1
    while len(ids) < count:
        rows = min(PAGE, count - len(ids))
        listing = get(f"{api}/search?sort=views:desc&rows={PAGE}&page={page}")
        found = [int(entry["id"]) for entry in listing.get("workflows") or []]
        if not found:
            break
        ids.extend(found[:rows])
        page += 1
    return ids


try:
    if update:
        ids = ranked_ids(top)
        print(f"ranked the {len(ids)} most-viewed templates")
    else:
        if not os.path.exists(manifest_path):
            print(f"no manifest at {manifest_path}; run with --update to create one", file=sys.stderr)
            sys.exit(1)
        with open(manifest_path) as handle:
            manifest = json.load(handle)
        ids = [entry["template"] for entry in manifest["templates"]]

    fetched = {}
    python_nodes = 0
    missing = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        futures = {pool.submit(fixture_for, template_id): template_id for template_id in ids}
        for future in concurrent.futures.as_completed(futures):
            template_id = futures[future]
            try:
                fixture, python = future.result()
            except LookupError:
                missing.append(template_id)
                continue
            python_nodes += python
            if fixture is not None:
                fetched[template_id] = encode(fixture)
except Unreachable as error:
    print(f"the template API could not be reached: {error}", file=sys.stderr)
    sys.exit(UNREACHABLE)

shutil.rmtree(corpus_dir, ignore_errors=True)
os.makedirs(corpus_dir)
entries = []
for template_id in sorted(fetched):
    payload = fetched[template_id]
    with open(os.path.join(corpus_dir, f"{template_id}.json"), "wb") as handle:
        handle.write(payload)
    fixture = json.loads(payload)
    entries.append({
        "template": template_id,
        "sha256": hashlib.sha256(payload).hexdigest(),
        "codeNodes": len(fixture["nodes"]),
    })

if update:
    manifest = {
        "comment": (
            "Pins for the Code-node compatibility corpus (EPIC-tjnr1z P7). The fixtures are "
            "the JavaScript of public n8n templates, other people's work, so they are never "
            "committed: only these digests are. Regenerate with scripts/code-corpus-sync.sh "
            "--update, and explain the baseline shift when you do."
        ),
        "source": {
            "api": "https://api.n8n.io/api/templates",
            "ranking": "search?sort=views:desc",
            "rankedAt": time.strftime("%Y-%m-%d", time.gmtime()),
            "ranked": len(ids),
            "missing": sorted(missing),
            "pythonCodeNodes": python_nodes,
            "licence": "template authors' own work, published on n8n.io — never committed",
        },
        "templates": entries,
    }
    with open(manifest_path, "w") as handle:
        json.dump(manifest, handle, indent=2)
        handle.write("\n")
    total = sum(entry["codeNodes"] for entry in entries)
    print(f"re-pinned {len(entries)} templates holding {total} code nodes into {os.path.relpath(manifest_path)}")
    sys.exit(0)

expected = {entry["template"]: entry["sha256"] for entry in manifest["templates"]}
actual = {entry["template"]: entry["sha256"] for entry in entries}
problems = []
for template_id, digest in sorted(expected.items()):
    if template_id in missing:
        problems.append(f"withdrawn upstream: template {template_id}")
    elif template_id not in actual:
        problems.append(f"no longer carries code: template {template_id}")
    elif actual[template_id] != digest:
        problems.append(f"changed: template {template_id}\n    manifest {digest}\n    fetched  {actual[template_id]}")

if problems:
    print("the corpus does not match the manifest:", file=sys.stderr)
    for problem in problems:
        print(f"  {problem}", file=sys.stderr)
    print(
        "\nA template changed or was withdrawn upstream. Re-pinning with --update shifts\n"
        "the baseline, so do it deliberately and say why in the commit.",
        file=sys.stderr,
    )
    sys.exit(1)

print(f"verified {len(entries)} templates against the manifest")
PYTHON

echo "code corpus ready at ${CORPUS_DIR}"
