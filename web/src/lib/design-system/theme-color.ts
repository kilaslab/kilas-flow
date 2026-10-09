// The browser-chrome colour (<meta name="theme-color">) follows the page
// background token rather than a hard-coded hex, so it changes with the theme.
// A computed custom property is a token stream (`oklch(0.172 0.007 235)`), and
// theme-color wants a colour every browser parses, hence the hex conversion.

import { parseCssColor, toHex } from './color';

/** Dark --kf-bg: what the page paints before any script runs (dark-first). */
export const DEFAULT_THEME_COLOR = '#0d1012';

export function themeColorFrom(value: string): string {
	try {
		const hex = toHex(parseCssColor(value));
		return hex.length === 7 ? hex : hex.slice(0, 7);
	} catch {
		return DEFAULT_THEME_COLOR;
	}
}

export function readThemeColor(root: HTMLElement = document.documentElement): string {
	return themeColorFrom(getComputedStyle(root).getPropertyValue('--kf-bg'));
}

/**
 * Calls `onChange` with the current theme colour now and whenever the root's
 * theme hooks change (data-theme or class), so a theme toggle (FEAT-a3dwj2)
 * needs no wiring of its own. Returns the disconnect function.
 */
export function watchThemeColor(onChange: (hex: string) => void, root: HTMLElement = document.documentElement): () => void {
	onChange(readThemeColor(root));
	const observer = new MutationObserver(() => onChange(readThemeColor(root)));
	observer.observe(root, { attributes: true, attributeFilter: ['data-theme', 'class'] });
	return () => observer.disconnect();
}
