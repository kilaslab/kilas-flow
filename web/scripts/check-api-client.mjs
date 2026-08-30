import { spawn } from 'node:child_process';
import { readdir, readFile, rm, writeFile, mkdir } from 'node:fs/promises';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const generatedDir = join(webDir, 'src/lib/api/generated');
const before = await snapshot(generatedDir);

try {
	await run(process.execPath, ['scripts/generate-api-client.mjs'], webDir);
	const after = await snapshot(generatedDir);
	if (sameSnapshot(before, after)) process.exit(0);

	await restore(generatedDir, before);
	throw new Error('Generated API client is stale. Run `pnpm generate:api` and commit the result.');
} catch (error) {
	await restore(generatedDir, before);
	throw error;
}

async function snapshot(directory) {
	try {
		const entries = await readdir(directory, { recursive: true, withFileTypes: true });
		const files = await Promise.all(
			entries
				.filter((entry) => entry.isFile())
				.map(async (entry) => {
					const path = join(entry.parentPath, entry.name);
					return [relative(directory, path), await readFile(path)]
				})
		);
		return new Map(files);
	} catch (error) {
		if (error && typeof error === 'object' && error.code === 'ENOENT') return new Map();
		throw error;
	}
}

function sameSnapshot(left, right) {
	if (left.size !== right.size) return false;
	return [...left].every(([path, contents]) => contents.equals(right.get(path)));
}

async function restore(directory, snapshot) {
	await rm(directory, { recursive: true, force: true });
	for (const [path, contents] of snapshot) {
		const target = join(directory, path);
		await mkdir(dirname(target), { recursive: true });
		await writeFile(target, contents);
	}
}

async function run(command, args, cwd) {
	const child = spawn(command, args, { cwd, stdio: 'inherit' });
	const [code] = await new Promise((resolveExit) => child.once('exit', (...result) => resolveExit(result)));
	if (code !== 0) throw new Error(`${command} ${args.join(' ')} failed with exit code ${code}`);
}
