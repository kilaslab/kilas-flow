/**
 * Manifest-versus-source agreement.
 *
 * This file is JavaScript on purpose, like operation-coverage.test.mjs: the
 * SDK typechecks under `moduleResolution: bundler` with no node types, so a
 * TypeScript test cannot read files. `make sdk-version-check` enforces the
 * same in CI; this gate enforces it wherever `pnpm test` runs, without
 * ceremony, so a release can never publish one number while the code reports
 * another.
 */
import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

const sdkDir = join(dirname(fileURLToPath(import.meta.url)), '..');

async function manifest() {
	return JSON.parse(await readFile(join(sdkDir, 'package.json'), 'utf8'));
}

async function sdkVersion() {
	const source = await readFile(join(sdkDir, 'src', 'version.ts'), 'utf8');
	// A plain read, not an import: this gate must stay a comparison of two
	// files, the way `make sdk-version-check` states it.
	const match = source.match(/export const SDK_VERSION\s*=\s*["']([^"']+)/);
	if (!match) throw new Error('SDK_VERSION not found in src/version.ts');
	return match[1];
}

describe('version agreement', () => {
	it('SDK_VERSION mirrors the manifest version', async () => {
		expect(await sdkVersion()).toBe((await manifest()).version);
	});

	it('the manifest licence matches the repository LICENSE', async () => {
		expect((await manifest()).license).toBe('Apache-2.0');
	});
});
