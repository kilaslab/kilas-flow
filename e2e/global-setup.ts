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
//
// KILASFLOW_DESIGN_HARNESS=1 keeps the /design/states harness route in this
// build (the visual suite screenshots it); release and Docker builds never set
// it, so their bundles drop the route entirely. Both suites share the one
// build. Locally the SPA is rebuilt without the harness afterwards (below).
//
// KILASFLOW_E2E_PREBUILT=1 reuses an existing bin/kilasflow instead of
// rebuilding: the CI visual step runs right after the functional suite built
// it, and local iteration should not pay for a full build per run.
async function globalSetup(): Promise<void> {
	const repoDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
	const binary = join(repoDir, 'bin', 'kilasflow');
	if (process.env.KILASFLOW_E2E_PREBUILT === '1' && existsSync(binary)) return;
	await execFileAsync('make', ['build-all'], {
		cwd: repoDir,
		env: { ...process.env, KILASFLOW_DESIGN_HARNESS: '1' },
		timeout: 20 * 60 * 1000,
		maxBuffer: 64 * 1024 * 1024
	});
	if (!existsSync(binary)) {
		throw new Error(`global-setup: ${binary} is missing after make build-all`);
	}
	// The binary has embedded its harness SPA; put a release SPA back in
	// web/build and internal/web/dist so a later Go-only `make build` on this
	// checkout does not ship /design/states. CI builds nothing after the suite.
	if (!process.env.CI) {
		const releaseEnv: NodeJS.ProcessEnv = { ...process.env };
		delete releaseEnv.KILASFLOW_DESIGN_HARNESS;
		await execFileAsync('make', ['build-web'], {
			cwd: repoDir,
			env: releaseEnv,
			timeout: 20 * 60 * 1000,
			maxBuffer: 64 * 1024 * 1024
		});
	}
}

export default globalSetup;
