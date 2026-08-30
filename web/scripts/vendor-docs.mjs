/**
 * Copies the Scalar API reference bundle into static/vendor/.
 *
 * The /docs page is served by the Go binary and must work with no network:
 * kilasflow ships as a self-contained binary and gets embedded into other
 * companies' products, where a third-party CDN in the request path is both an
 * availability risk and a privacy one. Huma's built-in docs renderer loads
 * Scalar from unpkg, so kilasflow serves its own page instead.
 *
 * The bundle is generated, not committed - static/vendor/ is gitignored and
 * this script runs on install and before every build.
 */

import { copyFile, mkdir, stat } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');

// @scalar/api-reference is a direct dependency, so pnpm links it here.
// The package does not export ./package.json, so resolve the file directly.
const source = resolve(
	webDir,
	'node_modules/@scalar/api-reference/dist/browser/standalone.js'
);

const target = resolve(webDir, 'static/vendor/scalar.js');

try {
	await stat(source);
} catch {
	console.error(
		`vendor-docs: ${source} not found.\n` +
			'Run `pnpm install` first, or check that @scalar/api-reference is still ' +
			'published with dist/browser/standalone.js.'
	);
	process.exit(1);
}

await mkdir(dirname(target), { recursive: true });
await copyFile(source, target);

const { size } = await stat(target);
console.log(`vendored scalar.js (${(size / 1024 / 1024).toFixed(1)} MB) -> static/vendor/`);
