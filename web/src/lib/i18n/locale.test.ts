import { afterEach, describe, expect, it, vi } from 'vitest';

import * as m from '$lib/paraglide/messages.js';
import { baseLocale, strategy } from '$lib/paraglide/runtime.js';
import {
	applyDocumentLanguage,
	currentLocale,
	localeOptions,
	pickLocale,
	readStoredLocale,
	rememberLocale,
	setLocale
} from '$lib/i18n/locale.svelte';

describe('the locale layer', () => {
	afterEach(() => {
		vi.unstubAllGlobals();
		setLocale('en');
	});

	it('pins paraglide to the base-locale strategy so its detection can never read navigator or write storage', () => {
		// The strategy now lives in two places — vite.config.ts and the
		// i18n:compile CLI flags — and both consumers of this test file (check
		// and test) run a compile first, so this pins the generated artifact
		// rather than either declaration. If someone adds url/cookie/
		// localStorage/preferredLanguage, the layer stops being the only owner
		// of the locale and this fails.
		expect(strategy).toEqual(['baseLocale']);
	});

	it('drives every message from the in-repo locale state', () => {
		expect(m.common_save()).toBe('Save');

		expect(setLocale('id')).toBe(true);
		expect(m.common_save()).toBe('Simpan');

		expect(setLocale('en')).toBe(true);
		expect(m.common_save()).toBe('Save');
	});

	it('ignores a locale the catalogs do not carry', () => {
		expect(setLocale('id')).toBe(true);

		expect(setLocale('fr')).toBe(false);
		expect(currentLocale()).toBe('id');
		expect(m.common_save()).toBe('Simpan');
	});

	it('resolves preference, then storage, then the base locale', () => {
		// The first candidate that is a locale we ship wins; the caller orders
		// them, so the embed can put its own locale ahead of a stored choice.
		expect(pickLocale(['id', 'fr', null])).toBe('id');
		expect(pickLocale([null, undefined, 'fr'])).toBe(baseLocale);
		expect(pickLocale([])).toBe(baseLocale);
	});

	it('remembers the chosen locale, and ignores a stored value the catalogs do not carry', () => {
		// localStorage is stubbed rather than provided by jsdom: web/ has no
		// jsdom dependency and this module must survive a Node prerender.
		const store = new Map<string, string>();
		const localStorage = {
			getItem: (key: string) => store.get(key) ?? null,
			setItem: (key: string, value: string) => void store.set(key, value)
		};
		vi.stubGlobal('window', { localStorage });
		vi.stubGlobal('localStorage', localStorage);

		rememberLocale('id');
		expect(readStoredLocale()).toBe('id');

		store.set('kilasflow.locale', 'fr');
		expect(readStoredLocale()).toBeNull();
		expect(pickLocale([readStoredLocale(), baseLocale])).toBe(baseLocale);
	});

	it('stamps lang on the document when the locale is applied', () => {
		vi.stubGlobal('document', { documentElement: { lang: '' } });

		applyDocumentLanguage('id');
		expect(document.documentElement.lang).toBe('id');

		applyDocumentLanguage('en');
		expect(document.documentElement.lang).toBe('en');
	});

	it('offers every shipped locale, with endonyms that read the same in both', () => {
		expect(localeOptions().map((option) => option.value)).toEqual(['en', 'id']);
		expect(localeOptions().map((option) => option.label)).toEqual(['English', 'Bahasa Indonesia']);
	});
});
