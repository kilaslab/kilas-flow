import { defineHastPlugin } from 'satteri';

/**
 * Prefix root-relative links written in page content with Astro's `base`.
 *
 * Why this exists
 * ---------------
 * Starlight prefixes the links it generates itself — the sidebar, the table of
 * contents, every stylesheet and script — with `base`. It does not touch the
 * body of a page: a link written as `/start/install/` is emitted exactly as
 * written. On a site served from the root that is correct; on a GitHub Pages
 * project site served from `/<repo>/` every one of those links is a 404.
 *
 * That failure is a nasty one because it cannot be seen in development, where
 * there is no base, and shows up only once the site is deployed. Making every
 * author remember to write the deployment path into every link is the other way
 * to solve it, and it hardcodes the current hosting decision into the prose —
 * to be undone by hand the day the site moves to a custom domain.
 *
 * Ordering
 * --------
 * starlight-links-validator appends its own visitor to `hastPlugins`, so this
 * one — passed in astro.config.mjs — runs first. The validator therefore checks
 * links that have already been prefixed, against routes that are also prefixed.
 * That ordering is what lets a configurable base and build-time link validation
 * coexist instead of one defeating the other.
 *
 * @param {string | undefined} rawBase
 */
export function baseLinks(rawBase) {
	// Normalise to a single leading slash with no trailing slash, so joining is
	// one rule rather than several cases. A base of '/' or nothing normalises to
	// the empty string, which makes every visit below a no-op — the correct
	// behaviour for a site served from the root.
	const base = `/${(rawBase ?? '').split('/').filter(Boolean).join('/')}`.replace(/^\/$/, '');

	return defineHastPlugin({
		name: 'kilasflow-base-links',
		element: {
			filter: ['a'],
			visit(node, ctx) {
				if (!base) return;
				const href = node.properties?.['href'];
				if (typeof href !== 'string') return;

				// Root-relative and not already prefixed. A protocol-relative URL
				// (`//example.com`) starts with a slash but points at another host,
				// which is the one case where "starts with /" is not enough to
				// conclude the link is internal.
				if (!href.startsWith('/') || href.startsWith('//')) return;
				if (href === base || href.startsWith(`${base}/`)) return;

				ctx.setProperty(node, 'href', base + href);
			},
		},
	});
}
