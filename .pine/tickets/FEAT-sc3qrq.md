---
id: FEAT-sc3qrq
title: 'n8n-style CLI workflows: import/export files, `--all` and directory forms, bulk operations, list filters'
status: todo
priority: medium
labels:
    - cli
    - n8n
    - import-export
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T03:06:00Z"
---

# Description

n8n users expect `import:workflow`/`export:workflow` with files and directories, plus list filters. KilasFlow's CLI works on one id at a time, and its export is a wrapper n8n can't import.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: cli-skills; finding ids: CLI-9). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** `n8n import:workflow --input=f.json` and `--separate --input=dir/`, `n8n export:workflow --all --output=`, `n8n export:credentials`, `n8n update:workflow --all --active=false`, `n8n execute --id`, `list:workflow`. Public API: `GET /workflows?name=&active=&tags=`, `DELETE /executions/{id}`, tags, variables, `X-N8N-API-KEY`.

# Steps to Reproduce

1. `kilasflow api import-workflow --body @n8n.json`: the raw n8n file must first be wrapped in `{format,workflow,name}` by hand. 2. `kilasflow workflow export <wf> --out f.json` → `flag provided but not defined: -out`. 3. `kilasflow api export-workflow --path id=<wf> --query format=n8n --out f.json`. 4. `kilasflow api --list` (81 ops). 5. `curl -H "X-N8N-API-KEY: <key>" :18097/api/v1/workflows`.

# Expected

`workflow import --file <n8n.json>` (guarded), `workflow export --out` writing the bare n8n document with lossy[] reported on stderr or in the envelope, `--all` and directory forms, and `workflow list --name/--active`.

# Actual

There's no `workflow import` verb; the skills and docs call it deliberately absent. Step 3 writes `{$schema,format,workflow,lossy,supportedMappings}`, which n8n's "Import from file" doesn't accept. Getting a pure n8n file needs jq. There's no bulk import or export (a directory, `--all`), so backup and migration go one workflow at a time. `list-workflows` takes only `limit` and `cursor`: no `name`, `active` or tag filter. With more than 100 workflows on the shared server, finding one by name means paging everything. There's no execution delete, no tags, no variables. `X-N8N-API-KEY` gets a 401 (only `Authorization: Bearer` works).

# Acceptance Criteria
- [ ] `workflow import --file <n8n.json>` (guarded) and `--dir`
- [ ] `workflow export --out` writes the bare n8n document (the lossy notes go to stderr or the envelope); `--all --out-dir`
- [ ] `workflow list --name --active`, and bulk activate/deactivate with confirmation

# Implementation Plan

Add the two verbs. Give `export-workflow` a `?raw=true` or `Accept` variant. Add name and active filters to list-workflows.

# Notes

Related tickets: BUG-6gkd12

Related (from the audit): BUG-6gkd12 (round-trip fidelity, a different aspect)

# Related Files

cs/e-n8n.json (the wrapper), cs/cmdlog.txt 09:11:30, the `api --list` output in the transcript, the OpenAPI parameters for list-workflows (`limit`, `cursor` only).

# Attachments
