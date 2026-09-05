import { rm } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { dumpOpenAPISpec, run } from '../../scripts/openapi-spec.mjs';

const sdkDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const specPath = join(sdkDir, '.tmp', 'openapi.json');

try {
	await dumpOpenAPISpec(specPath);
	await run('pnpm', ['exec', 'orval', '--config', 'orval.config.ts'], sdkDir);
} finally {
	await rm(dirname(specPath), { recursive: true, force: true });
}
