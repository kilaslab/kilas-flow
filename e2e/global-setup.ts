import { execFile } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

const execFileAsync = promisify(execFile);

// Builds the binary and the SPA once per suite run, so every test boots the
// same artifact. `make build-all` builds the SPA into internal/web/dist BEFORE
// compiling the Go binary: the SPA is go:embed'd, so a binary built first
// would serve the "SPA not built" placeholder and every editor assertion would
// fail looking like a frontend bug.
async function globalSetup(): Promise<void> {
	const repoDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
	await execFileAsync('make', ['build-all'], {
		cwd: repoDir,
		timeout: 20 * 60 * 1000,
		maxBuffer: 64 * 1024 * 1024
	});
	const binary = join(repoDir, 'bin', 'kilasflow');
	if (!existsSync(binary)) {
		throw new Error(`global-setup: ${binary} is missing after make build-all`);
	}
}

export default globalSetup;
