/**
 * Producing the tarball a consumer receives.
 *
 * The one place the SDK is packed, so the checks that read the file list and
 * the checks that install it cannot disagree about which bytes were tested.
 */
import { execFile } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

/**
 * Run a command and reject with both streams attached. A failing `tsc`
 * explains itself on stdout and a failing `npm` on stderr, so keeping only one
 * of them loses the reason the check failed.
 */
export async function run(command, args, options = {}) {
	try {
		return await execFileAsync(command, args, {
			cwd: options.cwd,
			env: options.env,
			encoding: 'utf8',
			maxBuffer: 64 * 1024 * 1024,
			stdio: options.stdio
		});
	} catch (error) {
		const detail = [error.stdout, error.stderr]
			.filter((stream) => typeof stream === 'string' && stream.trim() !== '')
			.join('\n');
		if (detail !== '') {
			// The child's own explanation, without a multi-kilobyte echo of a
			// `node -e` command ahead of it.
			const command = [error.cmd, ...(error.args ?? [])].join(' ');
			const shown = command.length > 160 ? `${command.slice(0, 160)}…` : command;
			error.message = `${shown}\nexit ${error.code ?? '?'}\n${detail}`;
		}
		throw error;
	}
}

/**
 * `npm pack --json` into `dest`, which is created first: `--pack-destination`
 * fails with ENOENT on a directory that does not exist yet. stderr stays
 * inherited because `--json` prints pure JSON on stdout only when nothing else
 * is written there.
 *
 * The destination defaults to a fresh temp directory at the call site; this
 * function only packs.
 */
export async function packSdk({ sdkDir, dest }) {
	await mkdir(dest, { recursive: true });
	const { stdout } = await run('npm', ['pack', '--json', '--pack-destination', dest], {
		cwd: sdkDir,
		stdio: ['ignore', 'pipe', 'inherit']
	});

	let parsed;
	try {
		parsed = JSON.parse(stdout ?? '');
	} catch {
		throw new Error(`npm pack --json did not print JSON:\n${stdout}`);
	}

	const [entry] = Array.isArray(parsed) ? parsed : [];
	if (!entry?.filename || !Array.isArray(entry.files)) {
		throw new Error(`npm pack --json printed an unexpected shape:\n${stdout}`);
	}

	return {
		tarball: isAbsolute(entry.filename) ? entry.filename : join(dest, entry.filename),
		files: entry.files.map((file) => file.path),
		name: entry.name,
		version: entry.version
	};
}
