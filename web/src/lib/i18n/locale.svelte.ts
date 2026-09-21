import * as m from '$lib/paraglide/messages.js';
import {
	baseLocale,
	isLocale,
	locales,
	overwriteGetLocale,
	type Locale
} from '$lib/paraglide/runtime.js';

/**
 * The one owner of the runtime locale.
 *
 * paraglide is wired with `strategy: ['baseLocale']`, so its own detection
 * cannot read navigator, a cookie, a URL pattern or localStorage — none of
 * those is right for a product that embeds the same editor into a customer's
 * page, where the host decides the language. This module holds the choice in
 * a rune and hands it to the generated runtime through `overwriteGetLocale`,
 * which is the single seam paraglide offers for it: every message function
 * calls `getLocale()` behind the scenes, so one override moves the whole UI.
 *
 * This is also the only module that imports the generated runtime. Components
 * import their message functions and never `getLocale`, so the locale has one
 * writer and one reader.
 */
export const LOCALE_STORAGE_KEY = 'kilasflow.locale';

let current = $state<Locale>(pickLocale([readStoredLocale(), baseLocale]));

overwriteGetLocale(() => current);

/**
 * The first candidate the catalogs actually carry, else the base locale.
 *
 * Pure, and ordered by the caller, so a surface that has a better source than
 * the stored preference — the embed frame, which is told by its host — can put
 * that first and still fall back through the same rule.
 */
export function pickLocale(candidates: (string | null | undefined)[]): Locale {
	for (const candidate of candidates) {
		if (isLocale(candidate)) return candidate;
	}
	return baseLocale;
}

export function currentLocale(): Locale {
	return current;
}

/**
 * Applies a locale the catalogs carry, and reports whether it did. A locale
 * from outside — a stale stored value, a host that speaks a language this
 * build does not ship — is refused rather than silently accepted, which would
 * otherwise move `lang` and leave every message in the base locale.
 */
export function setLocale(next: string): boolean {
	if (!isLocale(next)) return false;
	current = next;
	return true;
}

/** No-op outside the browser: a Node prerender has no document to stamp. */
export function applyDocumentLanguage(value: Locale): void {
	if (typeof document === 'undefined') return;
	document.documentElement.lang = value;
}

/**
 * Guarded and wrapped because neither half of the environment is a given:
 * adapter-static prerenders the fallback page in Node, where `window` does not
 * exist, and a browser in private mode throws on every storage call.
 */
export function readStoredLocale(): Locale | null {
	if (typeof window === 'undefined') return null;
	try {
		const stored = localStorage.getItem(LOCALE_STORAGE_KEY);
		return isLocale(stored) ? stored : null;
	} catch {
		return null;
	}
}

export function rememberLocale(value: string): void {
	if (typeof window === 'undefined') return;
	try {
		localStorage.setItem(LOCALE_STORAGE_KEY, value);
	} catch {
		// Blocked storage: the choice still holds for this session.
	}
}

/**
 * The switcher's options. The map is written out rather than derived from a
 * key, because a declared reference is what keeps the tree-shaker able to drop
 * every message a build does not reach.
 *
 * Labels are endonyms — "English" and "Bahasa Indonesia" read the same in both
 * catalogs, which is what makes the switcher usable to someone who cannot read
 * the language the UI is currently in.
 */
const LANGUAGE_LABELS: Record<Locale, () => string> = {
	en: m.nav_language_english,
	id: m.nav_language_indonesian
};

export function localeOptions(): { value: Locale; label: string }[] {
	return locales.map((value) => ({ value, label: LANGUAGE_LABELS[value]() }));
}
