---
id: FEAT-a5fhjw
title: 'Open-source readiness: community health files, README metadata and repository settings'
status: doing
priority: high
labels:
    - dx
    - docs
created: "2026-09-20T04:23:35Z"
updated: "2026-09-20T04:23:35Z"
---

# Description

The repository is public and already carries the product, the LICENSE, CI and
release workflows and a documentation site in the tree, but it is missing the
files a stranger arriving from a search result looks for. The only contributing
page that exists is `docs/src/content/docs/contributing.md`, and it is about the
documentation site, not about the project.

Gaps at the time this ticket was opened:

- No `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md` or `CHANGELOG.md` at
  the root.
- No `.github/ISSUE_TEMPLATE/` and no pull request template, so the issue button
  opens a blank box that asks for nothing — not the version, not the deployment
  shape, not a reproduction.
- No `.editorconfig`, no `.github/dependabot.yml`.
- The GitHub repository has no description and no topics: it cannot be found by
  topic search and its link preview is empty.
- Private vulnerability reporting is disabled (`/private-vulnerability-reporting`
  returned `enabled: false`), and secret scanning, push protection and Dependabot
  security updates are all `disabled` in `security_and_analysis`. A `SECURITY.md`
  that promises a private channel has to have that channel actually switched on.

# Acceptance Criteria
- [ ] Root community health files exist and each claim in them is checkable
      against this tree.
- [ ] Issue forms ask for the version, the deployment shape and a reproduction,
      and route security reports away from public issues.
- [ ] README carries CI and licence badges and points at the contributing,
      conduct, security and changelog files.
- [ ] The repository has a description, topics, private vulnerability reporting
      enabled and Dependabot security updates enabled.
- [ ] `make docs-build` still passes (links from the docs site into these files
      resolve or are relative to the repository root, not to the site).

# Implementation Plan

1. Write the four root files against the commands that actually exist in the
   Makefile and the conventions the git log shows.
2. Add the issue forms, the PR template, `.editorconfig` and `dependabot.yml`.
3. Add badges, a Documentation section and a Community section to the README.
4. Set the repository metadata and turn on the security features the security
   policy names.

# Notes

Deliberately not added, and why:

- `GOVERNANCE.md` / `MAINTAINERS.md` — one maintainer, no committee. A governance
  document for a project with a single decision maker describes nothing.
- A CLA or DCO — the repository has no such requirement and Apache-2.0 does not
  need one; adding a gate that no contributor has been asked to sign would be
  ceremony.
- `.github/FUNDING.yml` — nobody has set up a funding channel.
- A GitHub Discussions forum — issues are enabled and the project answers there;
  a second empty venue splits the conversation.

The Go module path (`github.com/kilaslabs/kilas-flow`, an unclaimed account name)
is the other half of "published coordinates" and is tracked separately as
BUG-341sxn, because it is an atomic sweep of ~276 files rather than a document.

# Related Files

- `README.md`
- `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `CHANGELOG.md`
- `.github/ISSUE_TEMPLATE/`, `.github/PULL_REQUEST_TEMPLATE.md`
- `.github/dependabot.yml`, `.editorconfig`
- `scripts/check-coordinates.sh` (unrelated guard, referenced by BUG-341sxn)

# Attachments
