import { describe, expect, it } from 'vitest';

import { requestShortcuts, shortcutsBridge } from './shortcuts-bridge.svelte';

describe('shortcutsBridge', () => {
	it('starts with no editor and counts each request', () => {
		expect(shortcutsBridge.available).toBe(false);
		const before = shortcutsBridge.requests;
		requestShortcuts();
		requestShortcuts();
		expect(shortcutsBridge.requests).toBe(before + 2);
	});
});
