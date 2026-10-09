import { describe, expect, it } from 'vitest';

import { composite, contrast, deltaEOK, parseCssColor, relativeLuminance, toHex, toSrgb } from './color';

// Known vectors pin the conversion: the two kunyit accents and their hex
// values are quoted in the audit (§7), and the WCAG luminance of black and
// white bound the contrast scale. If a matrix constant drifts, these move.
describe('parseCssColor + toHex', () => {
	it.each([
		['oklch(0.82 0.14 95)', '#e1c34b'],
		['oklch(0.82 0.15 95)', '#e3c23b'],
		['oklch(1 0 0)', '#ffffff'],
		['oklch(0 0 0)', '#000000'],
		['#0ea5e9', '#0ea5e9'],
		['#fff', '#ffffff'],
		['rgb(14 165 233)', '#0ea5e9'],
		['rgb(14, 165, 233)', '#0ea5e9'],
		['rgba(14, 165, 233, 0.5)', '#0ea5e980'],
		['color(srgb 0.054902 0.647059 0.913725)', '#0ea5e9']
	])('%s renders as %s', (input, hex) => {
		expect(toHex(parseCssColor(input))).toBe(hex);
	});

	it('round-trips a hex colour through OKLCH without drift', () => {
		const c = parseCssColor('#dc2626');
		expect(c.l).toBeCloseTo(0.577, 2);
		expect(c.h).toBeCloseTo(27.3, 0);
		expect(toHex(c)).toBe('#dc2626');
	});

	it('reads an alpha channel as a number or a percentage', () => {
		expect(parseCssColor('oklch(0.08 0.01 235 / 0.55)').alpha).toBeCloseTo(0.55, 5);
		expect(parseCssColor('oklch(1 0.005 170 / 13%)').alpha).toBeCloseTo(0.13, 5);
		expect(parseCssColor('#00000080').alpha).toBeCloseTo(128 / 255, 5);
		expect(parseCssColor('transparent').alpha).toBe(0);
	});
});

describe('relative colour syntax', () => {
	it('keeps the origin channels when they are named back', () => {
		const c = parseCssColor('oklch(from oklch(0.82 0.14 95) l c h)');
		expect(c.l).toBeCloseTo(0.82, 6);
		expect(c.c).toBeCloseTo(0.14, 6);
		expect(c.h).toBeCloseTo(95, 6);
	});

	it('evaluates calc, min, max, clamp and bare parenthesised arithmetic', () => {
		const c = parseCssColor('oklch(from oklch(0.82 0.14 95) calc(l + -0.05) min(c, 0.11) calc(h + -20))');
		expect(c.l).toBeCloseTo(0.77, 6);
		expect(c.c).toBeCloseTo(0.11, 6);
		expect(c.h).toBeCloseTo(75, 6);
		// The accent-fg flip: a light accent (l > .60) gives ink, a dark one white.
		const ink = parseCssColor('oklch(from oklch(0.82 0.14 95) clamp(0.22, (0.60 - l) * 1000, 0.99) 0 h)');
		expect(ink.l).toBeCloseTo(0.22, 6);
		const white = parseCssColor('oklch(from #1d4ed8 clamp(0.22, (0.60 - l) * 1000, 0.99) 0 h)');
		expect(white.l).toBeCloseTo(0.99, 6);
		expect(parseCssColor('oklch(from #1d4ed8 max(l, 0.9) c h)').l).toBeCloseTo(0.9, 6);
	});

	it('takes an alpha after the slash and resolves the alpha keyword', () => {
		expect(parseCssColor('oklch(from #0ea5e9 l c h / 12%)').alpha).toBeCloseTo(0.12, 6);
		expect(parseCssColor('oklch(from oklch(0.5 0.1 20 / 0.4) l c h / alpha)').alpha).toBeCloseTo(0.4, 6);
	});
});

describe('color-mix(in oklch)', () => {
	it('mixing with transparent keeps the colour and scales its alpha', () => {
		const c = parseCssColor('color-mix(in oklch, oklch(0.7 0.18 25) 14%, transparent)');
		expect(c.l).toBeCloseTo(0.7, 6);
		expect(c.c).toBeCloseTo(0.18, 6);
		expect(c.h).toBeCloseTo(25, 6);
		expect(c.alpha).toBeCloseTo(0.14, 6);
	});

	it('mixes two opaque colours along the shorter hue arc', () => {
		const c = parseCssColor('color-mix(in oklch, oklch(0.6 0.1 350) 50%, oklch(0.8 0.2 10))');
		expect(c.l).toBeCloseTo(0.7, 6);
		expect(c.c).toBeCloseTo(0.15, 6);
		expect(c.h).toBeCloseTo(0, 6);
	});

	it('defaults the second weight to the remainder and an omitted pair to 50/50', () => {
		expect(parseCssColor('color-mix(in oklch, oklch(0.2 0 0), oklch(0.6 0 0) 25%)').l).toBeCloseTo(0.3, 6);
		expect(parseCssColor('color-mix(in oklch, oklch(0.2 0 0), oklch(0.6 0 0))').l).toBeCloseTo(0.4, 6);
	});
});

describe('contrast maths', () => {
	it('bounds the WCAG scale at 21:1 and 1:1', () => {
		expect(relativeLuminance(toSrgb(parseCssColor('#ffffff')))).toBeCloseTo(1, 6);
		expect(contrast(parseCssColor('#000'), parseCssColor('#fff'))).toBeCloseTo(21, 4);
		expect(contrast(parseCssColor('#777'), parseCssColor('#777'))).toBeCloseTo(1, 6);
	});

	it('matches a published pair (#767676 on white is 4.54:1)', () => {
		expect(contrast(parseCssColor('#767676'), parseCssColor('#ffffff'))).toBeCloseTo(4.54, 2);
	});

	it('composites a translucent layer in gamma sRGB, the way a browser paints it', () => {
		const out = composite(parseCssColor('#ffffff80'), parseCssColor('#000000'));
		expect(toHex(out)).toBe('#808080');
		expect(out.alpha).toBe(1);
	});

	it('measures perceptual distance in OKLab', () => {
		expect(deltaEOK(parseCssColor('oklch(0.5 0.1 30)'), parseCssColor('oklch(0.5 0.1 30)'))).toBe(0);
		expect(deltaEOK(parseCssColor('oklch(0.5 0 0)'), parseCssColor('oklch(0.6 0 0)'))).toBeCloseTo(0.1, 6);
	});
});

describe('errors', () => {
	it('throws on input it does not understand instead of guessing', () => {
		expect(() => parseCssColor('hsl(10 20% 30%)')).toThrow(/unsupported/i);
		expect(() => parseCssColor('oklch(from #f00 l c)')).toThrow();
		expect(() => parseCssColor('oklch(from #000 calc(l + q) c h)')).toThrow(/q/);
	});
});
