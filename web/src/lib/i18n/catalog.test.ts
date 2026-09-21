import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import * as m from '$lib/paraglide/messages.js';

/**
 * Key parity, which is the one thing the compiler cannot check for us.
 *
 * paraglide falls back to the base locale when a message is missing, so an
 * untranslated key renders as English rather than failing — the bug looks like
 * a translation someone forgot rather than a broken build. This walks the
 * catalog directory instead of listing the areas, so an area added by a later
 * stage is covered the moment its files land.
 */
const messagesRoot = fileURLToPath(new URL('../../../messages', import.meta.url));
const settings = JSON.parse(
	readFileSync(
		fileURLToPath(new URL('../../../project.inlang/settings.json', import.meta.url)),
		'utf8'
	)
) as { baseLocale: string; locales: string[] };

const locales = settings.locales;
const otherLocales = locales.filter((locale) => locale !== settings.baseLocale);

function areasIn(locale: string): string[] {
	return readdirSync(`${messagesRoot}/${locale}`)
		.filter((entry) => entry.endsWith('.json'))
		.map((entry) => entry.slice(0, -'.json'.length))
		.sort();
}

function keysIn(locale: string, area: string): string[] {
	const file = `${messagesRoot}/${locale}/${area}.json`;
	return Object.keys(JSON.parse(readFileSync(file, 'utf8'))).sort();
}

describe('the message catalogs', () => {
	it('every base-locale key has a translation in every other locale', () => {
		const baseAreas = areasIn(settings.baseLocale);
		expect(baseAreas.length).toBeGreaterThan(0);

		for (const locale of otherLocales) {
			// A missing file counts as every key in it missing, which is what
			// the compiler does: it silently falls back to the base locale.
			const localeAreas = areasIn(locale);
			expect(localeAreas, `${locale} carries a different set of areas`).toEqual(baseAreas);

			for (const area of baseAreas) {
				const baseKeys = keysIn(settings.baseLocale, area);
				const localeKeys = keysIn(locale, area);
				const missing = baseKeys.filter((key) => !localeKeys.includes(key));
				const extra = localeKeys.filter((key) => !baseKeys.includes(key));
				expect(missing, `${locale}/${area}.json is missing keys`).toEqual([]);
				expect(extra, `${locale}/${area}.json has keys the base locale does not`).toEqual([]);
			}
		}
	});

	it('a plural message selects one form for one and another for many', () => {
		const one = m.common_item_count({ count: 1 });
		const many = m.common_item_count({ count: 4 });
		expect(one).not.toBe(many);
		expect(one).toBe('1 item');

		// Indonesian has a single plural category, so both counts must render
		// the same form rather than falling through to the message id.
		const indonesianOne = m.common_item_count({ count: 1 }, { locale: 'id' });
		const indonesianMany = m.common_item_count({ count: 4 }, { locale: 'id' });
		expect(indonesianOne).toBe('1 item');
		expect(indonesianMany).toBe('4 item');
	});
});
