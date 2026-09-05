import { rm } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { dumpOpenAPISpec, run } from '../../scripts/openapi-spec.mjs';

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const generatedSpecPath = join(webDir, '.tmp', 'openapi.json');

try {
	await dumpOpenAPISpec(generatedSpecPath);
	await run('pnpm', ['exec', 'orval', '--config', 'orval.config.ts'], webDir);
} finally {
	await rm(dirname(generatedSpecPath), { recursive: true, force: true });
}
