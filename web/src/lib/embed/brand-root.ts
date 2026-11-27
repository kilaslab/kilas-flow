// The embed shell carries the host's accent as `--kf-accent-src` on its
// [data-kf-brand] element, but overlays that bits-ui portals to <body> (the
// version-history sheet, the paste report) render outside that element. While
// the embed route is mounted the same value also sits on the document root, so
// every accent step re-derives there too (app.css declares the derived tokens
// on :root and on [data-kf-brand]). The accent arrives already sanitised by
// sanitizeBranding.
//
// The theme, font and radius follow the same rule for the same reason: a
// portalled dialog must not come up dark inside a light editor. The canvas
// reads its colours from the same tokens (app.css maps every --xy-* variable
// onto a --kf-* one), so `data-theme` switches the canvas too.

import type { EmbedBranding, EmbedFontFamily, EmbedTheme } from './session.svelte';

export interface BrandTarget {
	setAttribute(name: string, value: string): void;
	removeAttribute(name: string): void;
	style: { setProperty(name: string, value: string): void; removeProperty(name: string): string };
}

/** Brands `root` with `accent` and returns the function that removes it again. */
export function brandDocument(root: BrandTarget, accent: string | undefined): () => void {
	if (!accent) return () => {};
	root.setAttribute('data-kf-brand', '');
	root.style.setProperty('--kf-accent-src', accent);
	return () => {
		root.removeAttribute('data-kf-brand');
		root.style.removeProperty('--kf-accent-src');
	};
}

/** The font stacks a session may pick. None loads anything from another origin. */
const FONT_STACKS: Record<Exclude<EmbedFontFamily, 'default'>, string> = {
	system: "system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif",
	serif: "ui-serif, Georgia, Cambria, 'Times New Roman', serif"
};

/**
 * The CSS custom properties a branding sets, in addition to the accent:
 * the interface font and the radius scale (surfaces at `radius`, controls at
 * 60% and chips at 40% of it, the proportions app.css uses by default).
 */
export function appearanceProperties(branding: Pick<EmbedBranding, 'fontFamily' | 'radius'>): Record<string, string> {
	const properties: Record<string, string> = {};
	if (branding.fontFamily && branding.fontFamily !== 'default') {
		properties['--font-sans'] = FONT_STACKS[branding.fontFamily];
		properties['--kf-font-sans'] = FONT_STACKS[branding.fontFamily];
	}
	if (typeof branding.radius === 'number') {
		const surface = branding.radius;
		properties['--radius'] = `${surface}px`;
		properties['--kf-radius-md'] = `${surface}px`;
		properties['--kf-radius-sm'] = `${Math.round(surface * 0.6)}px`;
		properties['--kf-radius-xs'] = `${Math.round(surface * 0.4)}px`;
	}
	return properties;
}

/** The palette a theme resolves to, given whether the viewer prefers light. */
export function resolveTheme(theme: EmbedTheme | undefined, prefersLight: boolean): 'light' | 'dark' | undefined {
	if (theme === 'system') return prefersLight ? 'light' : 'dark';
	return theme;
}

/**
 * Applies a resolved theme and the appearance properties to `root`, and
 * returns the function that removes them again.
 */
export function themeDocument(
	root: BrandTarget,
	theme: 'light' | 'dark' | undefined,
	properties: Record<string, string>
): () => void {
	if (theme) root.setAttribute('data-theme', theme);
	for (const [name, value] of Object.entries(properties)) root.style.setProperty(name, value);
	return () => {
		if (theme) root.removeAttribute('data-theme');
		for (const name of Object.keys(properties)) root.style.removeProperty(name);
	};
}
