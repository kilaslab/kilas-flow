import { describe, expect, it, vi } from 'vitest';

import { createAuthMode, safeNext } from './auth-mode';

const principal = { tenantId: 'acme', kind: 'session', email: 'ops@example.com' };

function health(authEnabled: boolean) {
	return vi.fn(async () => ({ status: 200, data: { status: 'ok', version: 'dev', authEnabled } }));
}

describe('createAuthMode', () => {
	it('reads the mode from the health probe once per page load', async () => {
		const probe = health(false);
		const mode = createAuthMode({ health: probe, me: vi.fn() });
		expect(await mode.authEnabled()).toBe(false);
		expect(await mode.authEnabled()).toBe(false);
		expect(probe).toHaveBeenCalledTimes(1);
	});

	it('never asks /auth/me who is signed in when authentication is off', async () => {
		const me = vi.fn(async () => ({ status: 200, data: principal }));
		const mode = createAuthMode({ health: health(false), me });
		expect(await mode.principal()).toBeNull();
		expect(me).not.toHaveBeenCalled();
	});

	it('asks /auth/me when authentication is on and returns the principal', async () => {
		const me = vi.fn(async () => ({ status: 200, data: principal }));
		const mode = createAuthMode({ health: health(true), me });
		expect(await mode.principal()).toEqual(principal);
		expect(me).toHaveBeenCalledTimes(1);
	});

	it('answers no principal when /auth/me refuses a signed-out caller', async () => {
		const me = vi.fn(async () => {
			throw new Error('401');
		});
		const mode = createAuthMode({ health: health(true), me });
		expect(await mode.principal()).toBeNull();
	});

	it('assumes authentication is on when the probe fails, and probes again next time', async () => {
		const probe = vi
			.fn()
			.mockRejectedValueOnce(new Error('offline'))
			.mockResolvedValueOnce({ status: 200, data: { status: 'ok', version: 'dev', authEnabled: false } });
		const mode = createAuthMode({ health: probe, me: vi.fn() });
		expect(await mode.authEnabled()).toBe(true);
		expect(await mode.authEnabled()).toBe(false);
		expect(probe).toHaveBeenCalledTimes(2);
	});
});

describe('safeNext', () => {
	it('keeps an in-app path', () => {
		expect(safeNext('/datastores?x=1')).toBe('/datastores?x=1');
	});

	it('falls back to the workflows list for anything that could leave the app', () => {
		expect(safeNext(null)).toBe('/app/workflows');
		expect(safeNext('')).toBe('/app/workflows');
		expect(safeNext('//evil.example')).toBe('/app/workflows');
		expect(safeNext('https://evil.example')).toBe('/app/workflows');
	});
});
