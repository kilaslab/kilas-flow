---
title: Contributing to these docs
description: How the site is built, the rule every page follows, and the decisions deliberately left open.
---

This site is an [Astro Starlight](https://starlight.astro.build) project in
`docs/`. It is a sibling of the editor in `web/`, not a part of it: `web/`'s
output is compiled into the Go binary, and documentation has no business being
there — it would put every wording change into the binary and into whatever the
release artefact costs to ship.

## Working on it

```sh
make docs        # dev server with hot reload
make docs-build  # production build, including link validation
```

Both need the dependencies installed once, with `pnpm install` in `docs/`. Node
and pnpm come from `devbox.json` and `web/package.json`'s `packageManager`
field, which are the same versions the rest of the repository uses; there is no
second toolchain here.

CI runs `make docs-build` on every change, so a broken build or a broken
internal link fails the run rather than reaching a reader.

## The rule every page follows

**Describe the system that exists.** If a feature is planned, either leave it
out or mark it plainly as not yet available. A page that oversells is worse than
a missing page, because a missing page sends the reader to the source and an
overselling page sends them into a debugging session.

This is not an abstract principle here. The repository has already had to spend
a change on making its README describe the system that exists rather than the
one that was planned. Where a feature is planned but not built, a page says so
in one plain sentence and names the ticket that tracks it, as
[Get notified of results](/guides/get-notified/) does for outbound execution
events.

Do not type a number the product can contradict. Counts of node types,
credential types and API operations are written between
`<!-- generated:... -->` markers by `make generate-docs-facts`, and
`make generate-docs-facts-check` fails CI when one is stale. If a page needs a
count that has no marker, leave the number out or add a fact to
`scripts/docs-facts`.

Verify before you write. The source is close by and the doc comments on the Go
packages are usually the answer.

## How the navigation works

The sidebar is written out by hand in `astro.config.mjs`, in groups that follow
what a reader is trying to do rather than where a file happens to live:
**Start**, then the three integrator tracks (**Embed in your SaaS**,
**Integrate via API**, **Build nodes for your API**), then **Workflows**,
**Operate**, **Reference** and **About these docs**.

Pages keep their URLs when the sidebar regroups them. `operate/tenants` sits
under Embed and `operate/acceptance-capstone` under About these docs, and both
still answer at the address they always had. Move a file only when you have to.
If you do, add a redirect in `astro.config.mjs` so an old link keeps working.

Reference is the one group built from its directory (`reference/`), because its
pages are generated. For every other page, **add it to the sidebar when you
create it.** The build fails with the list of pages that are in no group, so a
forgotten page is caught before a reader misses it.

The structure was settled before most of the prose so that new pages slot into
it rather than rearranging it. If you find yourself wanting to restructure, that
is worth doing, but do it as its own change, not folded into a page you happened
to be writing.

## Theme

Dark is the default, and that is a deliberate override rather than Starlight's
behaviour. Starlight's stylesheet is already dark-on-`:root` like the product's,
but its ThemeProvider resolves an unset preference from the operating system,
which would give a reader on a light desktop a light documentation site beside a
dark product. `src/components/ThemeProvider.astro` and `ThemeSelect.astro`
replace that with a dark default and a two-option picker; both files explain the
reasoning in full.

The palette in `src/styles/kilasflow.css` is copied from `web/src/app.css`, not
imported from it. Importing would couple this site's build to the SPA's Tailwind
setup, so a design change in the product could break the documentation build.
The two can therefore drift — if you change a colour in one, change it in the
other.

## Versioning policy

**Until the first release tag, this site documents one version: the current
state of `main`.** A page therefore describes what a build from `main` does,
and a feature that is planned but not shipped is marked plainly rather than
described as working — the rule above.

From the first release tag, the site serves two views through
[Starlight Versions](https://starlight.astro.build/guides/versioning/): the
newest tag as the default view, and `main` as the "next" view. A release is
made by tagging; docs for that release are the `main` tree at the tag, so
there is no docs freeze and no release branch to backport into. Fixes to a
released version's docs are cherry-picked to the versioned copy and land on
`main` in the same change.

What this rules out deliberately: versioning before there are two versions to
serve. A scheme designed against one version is a guess at which distinctions
readers will want, and maintaining `latest`/`next` overhead while `main` is
the only installable artifact buys nothing. The mechanism (the
`starlight-versions` plugin and a `src/content/docs/v<n>/` tree) is known and
small; adopting it is a single change once the first tag exists, not a
migration.

## Where the site is published

The site's address is
`https://kilaslab.github.io/kilas-flow/` — a project GitHub Pages site. The
build is wired for it: `site` and `base` default to that address in
`astro.config.mjs` (the environment can still override both, and CI does), so
a local build emits the sitemap and asset URLs exactly as the deployment
serves them, and `public/robots.txt` points crawlers at the sitemap. Turning
the deployment on is not a commit: it needs GitHub Pages enabled for the
repository with its source set to GitHub Actions, and `DOCS_PUBLISH = true`
in the repository's Actions variables — a one-time settings change nobody can
make from a pull request. Until it is made, the address is reserved but not
answering, and the README's tree links are the working copy.
