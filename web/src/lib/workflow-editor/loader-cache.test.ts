import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { OptionsLoader } from '$lib/api/generated/models';

import { LOAD_DEBOUNCE_MS, loadSoon, loaderSignature } from './loader-cache';

const loader: OptionsLoader = {
	source: 'internal',
	name: 'pack.telegram.operations',
	dependsOn: ['resource']
};

describe('loaderSignature', () => {
	it('keys on the parameters the loader declared, and nothing else', () => {
		// The regression this exists for: the effect tracked the whole node, so a
		// keystroke in a field the loader does not read sent a fresh request.
		const before = loaderSignature(loader, { resource: 'message', text: 'hello' }, 'cred-1');
		const afterText = loaderSignature(loader, { resource: 'message', text: 'hello world' }, 'cred-1');
		expect(afterText).toBe(before);

		const afterResource = loaderSignature(loader, { resource: 'chat', text: 'hello' }, 'cred-1');
		expect(afterResource).not.toBe(before);

		expect(loaderSignature(loader, { resource: 'message' }, 'cred-2')).not.toBe(before);
		expect(loaderSignature(undefined, { resource: 'message' })).toBe('');
	});

	it('separates two loaders that share a dependency', () => {
		const list = loaderSignature(loader, { resource: 'message' });
		const byId = loaderSignature({ ...loader, name: 'pack.telegram.operationById' }, { resource: 'message' });
		expect(list).not.toBe(byId);
	});
});

describe('loadSoon', () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it('does not call the loader for a key the effect abandoned before it settled', async () => {
		const load = vi.fn(async () => 'first');
		const applied: string[] = [];

		const cleanup = loadSoon({ key: 'a', load, apply: (result) => applied.push(result), fail: () => 'failed' });
		await vi.advanceTimersByTimeAsync(LOAD_DEBOUNCE_MS / 2);
		cleanup();
		await vi.advanceTimersByTimeAsync(LOAD_DEBOUNCE_MS * 2);

		expect(load).not.toHaveBeenCalled();
		expect(applied).toEqual([]);
	});

	it('waits for the debounce window before calling the loader', async () => {
		const load = vi.fn(async () => 'first');
		const applied: string[] = [];

		loadSoon({ key: 'a', load, apply: (result) => applied.push(result), fail: () => 'failed' });
		await vi.advanceTimersByTimeAsync(LOAD_DEBOUNCE_MS - 1);
		expect(load).not.toHaveBeenCalled();

		await vi.advanceTimersByTimeAsync(1);
		expect(load).toHaveBeenCalledTimes(1);
		expect(applied).toEqual(['first']);
	});

	it('never applies an answer for a key the user has moved off', async () => {
		let resolveFirst: (value: string) => void = () => {};
		const load = vi.fn(() => new Promise<string>((resolve) => (resolveFirst = resolve)));
		const applied: string[] = [];

		const cleanup = loadSoon({ key: 'a', load, apply: (result) => applied.push(result), fail: () => 'failed' });
		await vi.advanceTimersByTimeAsync(LOAD_DEBOUNCE_MS);
		cleanup();
		resolveFirst('late');
		await vi.advanceTimersByTimeAsync(0);

		expect(applied).toEqual([]);
	});

	it('reports a refused loader as a reason instead of an unhandled rejection', async () => {
		const applied: { options: never[]; reason: string }[] = [];
		const cleanup = loadSoon({
			key: 'a',
			load: async () => {
				throw new Error('telegram: upstream unreachable');
			},
			apply: (result) => applied.push(result),
			fail: (reason) => ({ options: [], reason }),
			cache: new Map()
		});
		await vi.advanceTimersByTimeAsync(LOAD_DEBOUNCE_MS);
		cleanup();

		expect(applied).toEqual([{ options: [], reason: 'telegram: upstream unreachable' }]);
	});

	it('answers from the cache without calling the server again, and does not cache a failure', async () => {
		const cache = new Map<string, string>();
		const load = vi.fn(async () => 'list');
		const applied: string[] = [];

		loadSoon({ key: 'k', load, apply: (result) => applied.push(result), fail: () => 'failed', cache });
		await vi.advanceTimersByTimeAsync(LOAD_DEBOUNCE_MS);
		loadSoon({ key: 'k', load, apply: (result) => applied.push(result), fail: () => 'failed', cache });

		expect(load).toHaveBeenCalledTimes(1);
		expect(applied).toEqual(['list', 'list']);

		const failing = vi.fn(async () => {
			throw new Error('nope');
		});
		const failureCache = new Map<string, string>();
		loadSoon({ key: 'k', load: failing, apply: () => {}, fail: () => 'failed', cache: failureCache });
		await vi.advanceTimersByTimeAsync(LOAD_DEBOUNCE_MS);
		expect(failureCache.size).toBe(0);
	});
});
