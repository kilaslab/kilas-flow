import { describe, expect, it } from 'vitest';

import { ApiError } from '$lib/api/http';

import { fitPaddingBeside, hasStatus, shownSaveError } from './save-feedback';

describe('shownSaveError', () => {
	it('stays silent on a conflict, which its own banner already explains without revision ids', () => {
		const raw = '409 — This workflow changed since you loaded it (expected revision wfv_01a0cbd9, latest is wfv_01a0cbda). Reload and save again.';
		expect(shownSaveError(raw, true)).toBeNull();
	});

	it('passes any other failure through', () => {
		expect(shownSaveError('422 — invalid', false)).toBe('422 — invalid');
		expect(shownSaveError(null, false)).toBeNull();
	});
});

describe('hasStatus', () => {
	it('recognises the API error a missing workflow raises', () => {
		expect(hasStatus(new ApiError(404, 'workflow not found'), 404)).toBe(true);
		expect(hasStatus(new ApiError(500, 'boom'), 404)).toBe(false);
	});

	it('is false for anything that is not an HTTP failure', () => {
		expect(hasStatus(new Error('offline'), 404)).toBe(false);
		expect(hasStatus(null, 404)).toBe(false);
		expect(hasStatus(undefined, 404)).toBe(false);
	});
});

describe('fitPaddingBeside', () => {
	const canvas = { left: 208, right: 1440 };

	it('pads evenly when no panel covers the canvas', () => {
		expect(fitPaddingBeside(canvas, null)).toEqual({ top: '24px', right: '24px', bottom: '24px', left: '24px' });
	});

	it('keeps the fitted revision out from under the history panel', () => {
		// A 416 px panel docked to the window's right edge covers the canvas from 1024 on.
		expect(fitPaddingBeside(canvas, 1024).right).toBe('440px');
	});

	it('ignores a panel that does not reach the canvas', () => {
		expect(fitPaddingBeside(canvas, 1500).right).toBe('24px');
	});

	it('never leaves the fitted area narrower than a sliver of the canvas', () => {
		const right = Number.parseFloat(fitPaddingBeside({ left: 0, right: 500 }, 40).right);
		expect(500 - right).toBeGreaterThanOrEqual(120);
	});
});
