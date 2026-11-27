// @ts-check
import { readdirSync } from 'node:fs';
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLinksValidator from 'starlight-links-validator';
import starlightLlmsTxt from 'starlight-llms-txt';
import { satteri } from '@astrojs/markdown-satteri';
import { baseLinks } from './plugins/base-links.mjs';

// This site is deliberately a sibling of `web/`, never a part of it. `web/` is
// the SvelteKit SPA whose output is copied into `internal/web/dist` and embedded
// by `//go:embed all:dist`, so anything that lands in that tree lands in the
// binary. Documentation changes far more often than the product and would pay
// for that on every release, in bytes and in rebuilds, for no gain — the reader
// of the documentation is on the public web, not inside the binary. Nothing
// under `docs/` is referenced by the SPA build, the Makefile's `build-web`
// target, or any embed directive, and nothing here should ever be.

// A project GitHub Pages site is served from a subpath — `/<repo>/` — while a
// rootless `astro dev` serves from the root. Astro needs both `site` and `base`
// to be right or every generated asset URL is correct in one of those and broken
// in the other, which is the single most common way this deployment fails and
// the most annoying to notice, because local preview looks perfect.
//
// Both now default to the Pages URL of the repository this project publishes
// from — `kilaslab/kilas-flow`, as go.mod, the README and the CI workflow name
// it — so a bare `make docs-build` produces the same URLs the deployment serves,
// sitemap included (Starlight's built-in sitemap integration skips itself until
// `site` is set, which is why the site was sitemap-less for as long as these
// were undefined). The environment still wins: the CI job sets both from
// `actions/configure-pages`, which derives them from the repository actually
// being built, so publishing from a fork or a renamed repository needs no edit
// here. The git remote has been known to lie about the name (`k-flow` vs
// `kilas-flow`), which is why the override exists at all.
const site = process.env.DOCS_SITE || 'https://kilaslab.github.io';
const base = process.env.DOCS_BASE || '/kilas-flow';

// The sidebar is written out by hand, group by group, because the grouping is
// the information architecture: an integrator looks for "embed", "API" or
// "build nodes" before looking for a directory name, and the directories were
// never arranged that way. Pages therefore keep their URLs (nothing is moved,
// so no link in the wild breaks) and the sidebar says where they belong.
//
// The price of a hand-written list is that a new page can be forgotten. The
// check below turns that into a failed build: every page under
// `src/content/docs/` must be listed in the sidebar below or sit in a directory
// the sidebar autogenerates, so the navigation cannot grow an orphan. The home
// page and the 404 page are the two that are deliberately in no group.
const sidebar = [
	{
		label: 'Start',
		items: [
			{ label: 'What KilasFlow is', slug: 'start/what-kilasflow-is' },
			{ label: 'Install', slug: 'start/install' },
			{ label: 'Your first workflow', slug: 'start/first-workflow' },
		],
	},
	{
		label: 'Embed in your SaaS',
		items: [
			{ label: 'SaaS integration playbook', slug: 'guides/saas-playbook' },
			{ label: 'Embedding and multi-tenancy', slug: 'guides/embedding' },
			{ label: 'Embed sessions in depth', slug: 'guides/embed-sessions' },
			{ label: 'Tenants, provisioning and identity', slug: 'operate/tenants' },
			{ label: 'Tenancy and the embed boundary', slug: 'concepts/tenancy-and-embedding' },
			{ label: 'White-label, localisation and go-live', slug: 'guides/white-label' },
		],
	},
	{
		label: 'Integrate via API',
		items: [
			{ label: 'Trigger workflows from your app', slug: 'guides/trigger-workflows' },
			{ label: 'Get notified of results', slug: 'guides/get-notified' },
			{ label: 'Tenant notifications', slug: 'operate/notifications' },
			{ label: 'Using the host SDK', slug: 'guides/sdk' },
			{ label: 'Idempotent requests', slug: 'guides/idempotency' },
			{ label: 'Import and export n8n workflows', slug: 'guides/n8n-migration' },
		],
	},
	{
		label: 'Build nodes for your API',
		items: [
			{ label: 'Choose a path', slug: 'guides/build-nodes-for-your-api' },
			{ label: 'Author a node pack', slug: 'guides/node-authoring' },
			{ label: 'Generate a pack from OpenAPI', slug: 'guides/openapi-pack' },
			{ label: 'App events as triggers', slug: 'guides/app-events' },
			{ label: 'Tenant-scoped nodes', slug: 'guides/tenant-scoped-nodes' },
			{ label: 'Community nodes', slug: 'guides/community-nodes' },
			{ label: 'JavaScript sidecar', slug: 'operate/javascript-sidecar' },
		],
	},
	{
		label: 'Workflows',
		items: [
			{ label: 'Architecture', slug: 'concepts/architecture' },
			{ label: 'The execution model', slug: 'concepts/execution-model' },
			{ label: 'Items and lineage', slug: 'concepts/items-and-lineage' },
			{ label: 'Expressions', slug: 'concepts/expressions' },
			{ label: 'The node registry', slug: 'concepts/node-registry' },
			{ label: 'Credentials', slug: 'concepts/credentials' },
			{ label: 'Triggers and webhooks', slug: 'concepts/webhooks' },
			{ label: 'Safety boundaries', slug: 'concepts/safety-boundaries' },
			{ label: 'Datastore concurrency', slug: 'concepts/datastore-concurrency' },
			{ label: 'Code (JavaScript)', slug: 'guides/code-javascript' },
		],
	},
	{
		label: 'Operate',
		items: [
			{ label: 'Deployment', slug: 'operate/deployment' },
			{ label: 'Deploy for SaaS', slug: 'operate/deploy-for-saas' },
			{ label: 'Configuration', slug: 'operate/configuration' },
			{ label: 'Configuration reference', slug: 'operate/configuration-reference' },
			{ label: 'Security posture', slug: 'operate/security' },
			{ label: 'Metrics', slug: 'operate/metrics' },
			{ label: 'Backups and upgrades', slug: 'operate/upgrades' },
			{ label: 'Key rotation', slug: 'operate/key-rotation' },
			{ label: 'Tenant deletion', slug: 'operate/tenant-deletion' },
			{ label: 'Runtime benchmark vs n8n', slug: 'operate/benchmark' },
		],
	},
	// Starlight 0.39 removed the `{ label, autogenerate }` shorthand; a group now
	// always has `items`, and autogenerate is an entry inside it. Reference is
	// the one group left on autogenerate: its pages are generated, and a
	// regenerated page must appear without a config edit.
	{ label: 'Reference', items: [{ autogenerate: { directory: 'reference' } }] },
	{
		label: 'About these docs',
		items: [
			{ label: 'Contributing', slug: 'contributing' },
			{ label: 'The epic acceptance capstone', slug: 'operate/acceptance-capstone' },
		],
	},
];

assertNoOrphanPages(sidebar);

/**
 * Fail the build when a page is in no sidebar group.
 *
 * @param {Array<{ items?: any[] }>} groups
 */
function assertNoOrphanPages(groups) {
	const listed = new Set();
	const generatedDirectories = [];
	const collect = (entries) => {
		for (const entry of entries) {
			if (entry.slug) listed.add(entry.slug);
			if (entry.autogenerate) generatedDirectories.push(`${entry.autogenerate.directory}/`);
			if (entry.items) collect(entry.items);
		}
	};
	collect(groups);

	const outsideAnyGroup = new Set(['index', '404']);
	const root = new URL('./src/content/docs/', import.meta.url);
	const orphans = [];
	const walk = (directory, prefix) => {
		for (const entry of readdirSync(directory, { withFileTypes: true })) {
			if (entry.isDirectory()) {
				walk(new URL(`${entry.name}/`, directory), `${prefix}${entry.name}/`);
				continue;
			}
			const match = /^(.*)\.(md|mdx)$/.exec(entry.name);
			if (!match) continue;
			const slug = `${prefix}${match[1]}`;
			if (outsideAnyGroup.has(slug) || listed.has(slug)) continue;
			if (generatedDirectories.some((dir) => slug.startsWith(dir))) continue;
			orphans.push(slug);
		}
	};
	walk(root, '');
	if (orphans.length > 0) {
		throw new Error(
			`These pages are in no sidebar group: ${orphans.join(', ')}. Add each to the sidebar in docs/astro.config.mjs.`,
		);
	}
}

export default defineConfig({
	site,
	base,

	// Starlight prefixes the links it generates with `base`, but leaves links
	// written in a page body alone, which would 404 on a subpath deployment. This
	// plugin closes that gap so a page can link to `/start/install/` and be right
	// wherever the site is served from. See plugins/base-links.mjs for why it is
	// registered here rather than as a Starlight plugin — the link validator
	// appends its own visitor after these, and that ordering is the point.
	//
	// Astro 7 made Sätteri the default Markdown processor, so this is a Sätteri
	// hast plugin. The older `markdown.rehypePlugins` array is a different
	// pipeline that would have to be installed separately.
	markdown: {
		processor: satteri({ hastPlugins: [baseLinks(base)] }),
	},

	integrations: [
		starlight({
			title: 'KilasFlow',
			description:
				'An embeddable open-source workflow engine for APIs, AI agents, and SaaS products.',
			social: [
				{
					icon: 'github',
					label: 'GitHub',
					href: 'https://github.com/kilaslab/kilas-flow',
				},
			],

			// Every page's "Edit this page" link opens the page's source at the
			// commit CI builds from. Starlight appends the page's file path
			// relative to this project's root (src/content/docs/…), so the base
			// names the docs/ directory in the repository and nothing deeper.
			editLink: {
				baseUrl: 'https://github.com/kilaslab/kilas-flow/edit/main/docs',
			},

			// Starlight always stamps `data-theme` on `<html>`, and with the stock
			// components the value follows the operating system. The product does
			// not: `web/src/app.css` puts the dark palette on bare `:root` and makes
			// light an opt-in. A reader whose OS is set to light would therefore get
			// a light documentation site next to a dark product and reasonably
			// conclude they were looking at two different things. These two
			// overrides move the default to dark and leave light as a deliberate
			// choice. See `src/components/ThemeProvider.astro` for the mechanism.
			components: {
				ThemeProvider: './src/components/ThemeProvider.astro',
				ThemeSelect: './src/components/ThemeSelect.astro',
			},

			customCss: ['./src/styles/kilasflow.css'],

			// Code blocks pick up the same jade-on-graphite palette as the rest of
			// the page through CSS variables set in kilasflow.css, so only the
			// geometry is set here.
			expressiveCode: {
				themes: ['github-dark-default', 'github-light'],
				styleOverrides: { borderRadius: '0.5rem' },
			},

			sidebar,

			plugins: [
				// Coding agents are a first-class audience of this product — the
				// binary embeds a skills bundle and serves the same verbs over an
				// MCP server — so the site publishes itself to them too. This
				// plugin serves /llms.txt (a curated table of contents), and
				// /llms-full.txt with every page's body, per llmstxt.org. The
				// integrator path leads: a host's engineer lands on the start
				// track, the playbook and the three tracks' entry pages before
				// the operator material, and an agent reading the site gets the
				// same order.
				starlightLlmsTxt({
					promote: [
						'index*',
						'start/**',
						'guides/saas-playbook',
						'guides/embedding',
						'concepts/tenancy-and-embedding',
						'guides/trigger-workflows',
						'guides/get-notified',
						'guides/build-nodes-for-your-api',
					],
					details:
						'The documentation describes the system that exists on main — features not yet shipped are marked as such rather than omitted. Counts the prose quotes (node types, operations, credential types) are generated from the product by scripts/docs-facts and cannot drift.',
					optionalLinks: [
						{
							label: 'Agent skills bundle',
							url: 'https://github.com/kilaslab/kilas-flow/tree/main/skills',
							description:
								'The skills embedded in the binary; `kilasflow skills install --target claude` (or codex, agents) installs them for a coding agent. `kilasflow mcp serve` exposes the same verbs over stdio.',
						},
					],
				}),
				// A later ticket renaming or moving a page must not be able to leave a
				// link pointing at nothing. This fails `astro build`, so the CI job
				// below catches it before a reader does. It runs on build only —
				// `astro dev` does not validate — which is why the Make target that
				// CI calls is the build and not the dev server.
				starlightLinksValidator({
					// The install page tells a reader to open http://localhost:5173 and
					// http://localhost:8080. Those are instructions to type into a
					// browser, not navigation within this site, and the reader who is
					// following along wants them clickable. Every other check stays on:
					// internal links, anchors and relative links are all still validated.
					errorOnLocalLinks: false,
				}),
			],
		}),
	],
});
