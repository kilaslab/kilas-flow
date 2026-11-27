import * as m from '$lib/paraglide/messages.js';

/**
 * The Help menu's destinations (FEAT-p01rcw) — the one place that decides what
 * the menu links to, so the hrefs are testable without rendering a menu.
 *
 * `/docs` and `/api/openapi.json` are served by the Go binary, not by this
 * SvelteKit app: a plain `<a>` would be taken for a client-side route and land
 * on the SPA fallback, so the menu renders them with `rel="external"` (a full
 * page load, same tab). The changelog lives on GitHub and opens in a new tab.
 */
export type HelpLinkKey = 'api-reference' | 'openapi' | 'whats-new';

export interface HelpLink {
	key: HelpLinkKey;
	href: string;
	/** Another site: opens in a new tab. A same-origin server page reloads the document instead. */
	newTab: boolean;
	label: () => string;
}

/** The CHANGELOG.md the repository keeps; there is no hosted release-notes page. */
export const CHANGELOG_URL = 'https://github.com/kilaslab/kilas-flow/blob/main/CHANGELOG.md';

export const helpLinks: HelpLink[] = [
	{ key: 'api-reference', href: '/docs', newTab: false, label: m.nav_help_api_reference },
	{ key: 'openapi', href: '/api/openapi.json', newTab: false, label: m.nav_help_openapi },
	{ key: 'whats-new', href: CHANGELOG_URL, newTab: true, label: m.nav_help_whats_new }
];
