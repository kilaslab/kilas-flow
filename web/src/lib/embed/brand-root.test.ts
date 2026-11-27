import { describe, expect, it } from 'vitest';

import { appearanceProperties, brandDocument, resolveTheme, themeDocument, type BrandTarget } from './brand-root';

function fakeRoot(): BrandTarget & { attrs: Map<string, string>; props: Map<string, string> } {
	const attrs = new Map<string, string>();
	const props = new Map<string, string>();
	return {
		attrs,
		props,
		setAttribute: (name, value) => void attrs.set(name, value),
		removeAttribute: (name) => void attrs.delete(name),
		style: {
			setProperty: (name, value) => void props.set(name, value),
			removeProperty: (name) => {
				const old = props.get(name) ?? '';
				props.delete(name);
				return old;
			}
		}
	};
}

// Overlays (the version-history sheet, the paste report) are portalled to
// <body>, outside the embed shell, so the host accent has to sit on <html> as
// well or they render in the default kunyit.
describe('brandDocument', () => {
	it('puts the host accent on the document root while the embed is mounted', () => {
		const root = fakeRoot();
		const cleanup = brandDocument(root, '#0ea5e9');
		expect(root.attrs.get('data-kf-brand')).toBe('');
		expect(root.props.get('--kf-accent-src')).toBe('#0ea5e9');
		cleanup();
		expect(root.attrs.has('data-kf-brand')).toBe(false);
		expect(root.props.has('--kf-accent-src')).toBe(false);
	});

	it('leaves the root alone when the host sends no accent', () => {
		const root = fakeRoot();
		brandDocument(root, undefined)();
		expect(root.attrs.size).toBe(0);
		expect(root.props.size).toBe(0);
	});
});

// A light-first host picks the palette, font and radius; portalled overlays
// read them from <html>, so they go there as well as on the shell.
describe('themeDocument', () => {
	it('sets the resolved theme and appearance on the root and removes them again', () => {
		const root = fakeRoot();
		const cleanup = themeDocument(root, 'light', appearanceProperties({ fontFamily: 'serif', radius: 4 }));
		expect(root.attrs.get('data-theme')).toBe('light');
		expect(root.props.get('--kf-font-sans')).toContain('serif');
		expect(root.props.get('--kf-radius-md')).toBe('4px');
		expect(root.props.get('--kf-radius-sm')).toBe('2px');
		cleanup();
		expect(root.attrs.size).toBe(0);
		expect(root.props.size).toBe(0);
	});

	it('leaves the root alone when the session names no theme or appearance', () => {
		const root = fakeRoot();
		themeDocument(root, undefined, appearanceProperties({}))();
		expect(root.attrs.size).toBe(0);
		expect(root.props.size).toBe(0);
	});
});

describe('resolveTheme', () => {
	it('follows the viewer only for system', () => {
		expect(resolveTheme('system', true)).toBe('light');
		expect(resolveTheme('system', false)).toBe('dark');
		expect(resolveTheme('light', false)).toBe('light');
		expect(resolveTheme('dark', true)).toBe('dark');
		expect(resolveTheme(undefined, true)).toBeUndefined();
	});
});

describe('appearanceProperties', () => {
	it('keeps the product font for default and allows square corners', () => {
		expect(appearanceProperties({ fontFamily: 'default' })).toEqual({});
		expect(appearanceProperties({ radius: 0 })).toMatchObject({ '--kf-radius-md': '0px', '--kf-radius-xs': '0px' });
	});
});
