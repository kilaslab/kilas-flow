import { describe, expect, it } from 'vitest';

import { appTokenSheet } from './app-css';
import { tokenEnv } from './css-tokens';
import { DEFAULT_THEME_COLOR, themeColorFrom } from './theme-color';

describe('theme-color meta', () => {
	const sheet = appTokenSheet();

	it('converts the computed --kf-bg of each theme to the hex a browser chrome accepts', () => {
		expect(themeColorFrom(tokenEnv(sheet, 'dark').resolve('--kf-bg'))).toBe('#0d1012');
		expect(themeColorFrom(tokenEnv(sheet, 'light').resolve('--kf-bg'))).toBe('#f9fafb');
	});

	it('defaults to the dark page background, which is what renders before hydration', () => {
		expect(DEFAULT_THEME_COLOR).toBe(themeColorFrom(tokenEnv(sheet, 'dark').resolve('--kf-bg')));
	});

	it('falls back to the default on an empty or unreadable value', () => {
		expect(themeColorFrom('')).toBe(DEFAULT_THEME_COLOR);
		expect(themeColorFrom('lab(50% 20 30)')).toBe(DEFAULT_THEME_COLOR);
	});
});
