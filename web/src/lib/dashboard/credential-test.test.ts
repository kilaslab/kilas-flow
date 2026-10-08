import { describe, expect, it } from 'vitest';

import { canTest, testVerdict } from './credential-test';

describe('what a credential test answered', () => {
	it('reads a probe that passed as connected', () => {
		expect(testVerdict({ ok: true, detail: 'the service answered 200' })).toEqual({
			kind: 'ok',
			detail: 'the service answered 200'
		});
	});

	it('reads a probe that ran and failed as failed', () => {
		expect(testVerdict({ ok: false, detail: 'the service answered 401' })).toEqual({
			kind: 'failed',
			detail: 'the service answered 401'
		});
	});

	// BUG-epy2se: the server says "untestable" for a type with no probe, and
	// showing that as a red failure told people a working credential was broken.
	it('reads a type with no probe as untestable, not failed', () => {
		expect(
			testVerdict({ ok: false, untestable: true, detail: 'this credential type has no test defined' })
		).toEqual({ kind: 'untestable', detail: 'this credential type has no test defined' });
	});

	it('treats a missing detail as empty', () => {
		expect(testVerdict({ ok: false })).toEqual({ kind: 'failed', detail: '' });
	});
});

describe('whether the editor offers Test', () => {
	it('offers it for a type the server can probe', () => {
		expect(canTest({ testable: true })).toBe(true);
	});

	it('leaves it off for a type the server cannot probe', () => {
		expect(canTest({ testable: false })).toBe(false);
	});

	it('offers nothing before a type is chosen', () => {
		expect(canTest(null)).toBe(false);
	});
});
