import { readFile } from 'node:fs/promises';

import { describe, expect, it } from 'vitest';

describe('generated API client', () => {
	it('uses the shared ApiError type as its default TanStack Query error', async () => {
		const client = await readFile(new URL('./generated/system/system.ts', import.meta.url), 'utf8');

		expect(client).toContain("import { apiFetch } from '../../http';");
		expect(client).toContain("import type { ErrorType } from '../../http';");
		expect(client).toContain('TError = ErrorType<ErrorModel>');
	});
});
