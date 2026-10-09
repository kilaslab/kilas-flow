import { describe, expect, it } from 'vitest';

import { brandDocument, type BrandTarget } from './brand-root';

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
