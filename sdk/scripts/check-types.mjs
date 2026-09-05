/**
 * Fails when the committed generated types differ from the server's current
 * OpenAPI document, so a contract change cannot land without the SDK's types
 * following it.
 */
import { readFile, rm, cp, mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { dumpOpenAPISpec, run } from '../../scripts/openapi-spec.mjs';

const sdkDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const generated = join(sdkDir, 'src', 'generated', 'models.ts');
const specPath = join(sdkDir, '.tmp', 'openapi.json');

const backup = join(await mkdtemp(join(tmpdir(), 'kilasflow-sdk-types-')), 'models.ts');
await cp(generated, backup);

try {
	await dumpOpenAPISpec(specPath);
	await run('pnpm', ['exec', 'orval', '--config', 'orval.config.ts'], sdkDir);

	const [before, after] = await Promise.all([readFile(backup, 'utf8'), readFile(generated, 'utf8')]);
	if (before !== after) {
		await cp(backup, generated);
		throw new Error('SDK types are out of date. Run `pnpm generate:types` and commit the result.');
	}
} finally {
	await rm(dirname(specPath), { recursive: true, force: true });
	await rm(dirname(backup), { recursive: true, force: true });
}
