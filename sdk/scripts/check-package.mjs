/**
 * The SDK, checked the way a consumer receives it.
 *
 * `make sdk-check` typechecks the source under `moduleResolution: bundler`,
 * which is the setting the author works in and the one that cannot see the
 * failure this script exists for: an ESM package whose relative re-export
 * dropped its `.js` extension typechecks under `bundler` and fails for every
 * consumer on `node16`/`nodenext` (TS2835). So this script packs the tarball,
 * installs it into a scratch project outside the repository, and typechecks and
 * runs it there, through the `exports` map, with the TypeScript version the
 * package pins.
 *
 * Every phase runs even when an earlier one failed, and all failures are
 * printed before exiting 1: with the file list and the typecheck both asserted,
 * a tarball missing a declaration fails twice, in two different ways, which is
 * the evidence that both layers bite independently. The one exception is the
 * install, which the later phases genuinely need — those are reported as not
 * run rather than allowed to cascade.
 *
 * Flags:
 *   --tarball <path>   check this tarball instead of packing one (mutation proofs)
 *   --registry [spec]  install from the registry instead of the tarball;
 *                      default spec @kilasflow/sdk@<manifest version>
 *                      (KILASFLOW_SDK_SPEC is read when the flag is absent)
 *   --keep             keep the scratch project for inspection
 */
import { existsSync } from 'node:fs';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

import { packSdk, run } from './lib/pack.mjs';
import { packProblems } from './lib/release.mjs';

const sdkDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repoDir = resolve(sdkDir, '..');
const require = createRequire(import.meta.url);

const usage = 'usage: node scripts/check-package.mjs [--tarball <path>] [--registry [spec]] [--keep]';

function parseArgs(argv) {
	const options = { tarball: null, registry: null, keep: false };
	for (let index = 0; index < argv.length; index += 1) {
		const arg = argv[index];
		if (arg === '--keep') {
			options.keep = true;
		} else if (arg === '--tarball') {
			const value = argv[index + 1];
			if (value === undefined || value.startsWith('--')) throw new Error(`--tarball needs a path\n${usage}`);
			options.tarball = value;
			index += 1;
		} else if (arg === '--registry') {
			const value = argv[index + 1];
			if (value === undefined || value.startsWith('--')) {
				options.registry = '';
			} else {
				options.registry = value;
				index += 1;
			}
		} else {
			throw new Error(`unknown argument: ${arg}\n${usage}`);
		}
	}
	if (options.registry === null && process.env.KILASFLOW_SDK_SPEC !== undefined) {
		options.registry = process.env.KILASFLOW_SDK_SPEC;
	}
	return options;
}

/** The consumer the README describes: the three subpaths and no deep import. */
const consumerSource = [
	'// A consumer, exactly as the README describes one.',
	"import { API_VERSION, KilasFlowClient, SDK_VERSION } from '@kilasflow/sdk';",
	'import {',
	'	KilasFlowClient as ServerClient,',
	'	KilasFlowError,',
	'	datastoreFilter,',
	'	tenantClientFactory',
	"} from '@kilasflow/sdk/server';",
	"import { mountWorkflowEditor, subscribeExecutionEvents } from '@kilasflow/sdk/browser';",
	'',
	'const v: string = SDK_VERSION;',
	'const api: string = API_VERSION;',
	'const workflows: Promise<unknown> = new KilasFlowClient({',
	"	baseUrl: 'https://flows.example',",
	"	apiKey: 'kfa1_x_y'",
	'}).listWorkflows();',
	'const perTenant: (tenantApiKey: string) => ServerClient = tenantClientFactory({',
	"	baseUrl: 'https://flows.example'",
	'});',
	"const filter = datastoreFilter('and', []);",
	"const failure: KilasFlowError = new KilasFlowError(404, 'not found');",
	'const mount: typeof mountWorkflowEditor = mountWorkflowEditor;',
	'const subscribe: typeof subscribeExecutionEvents = subscribeExecutionEvents;',
	'',
	'// @ts-expect-error the exports map must block deep imports',
	"import type {} from '@kilasflow/sdk/dist/server.js';",
	'',
	'export { api, failure, filter, mount, perTenant, subscribe, v, workflows };',
	''
].join('\n');

/** `moduleResolution` is paired with the `module` it requires, per variant. */
const typecheckVariants = [
	{ slug: 'bundler', module: 'ESNext', moduleResolution: 'bundler', lib: ['ES2022', 'DOM', 'DOM.Iterable'], skipLibCheck: false },
	{ slug: 'node16', module: 'node16', moduleResolution: 'node16', lib: ['ES2022', 'DOM', 'DOM.Iterable'], skipLibCheck: false },
	{ slug: 'nodenext', module: 'nodenext', moduleResolution: 'nodenext', lib: ['ES2022', 'DOM', 'DOM.Iterable'], skipLibCheck: false },
	{ slug: 'nodenext-nodom', module: 'nodenext', moduleResolution: 'nodenext', lib: ['ES2022'], skipLibCheck: true }
];

/**
 * The runtime check reads the installed manifest off disk rather than through
 * the exports map, because the map deliberately does not export `./package.json`.
 */
const runtimeSource = [
	"import { readFile } from 'node:fs/promises';",
	"const root = await import('@kilasflow/sdk');",
	"const server = await import('@kilasflow/sdk/server');",
	"const browser = await import('@kilasflow/sdk/browser');",
	"const installed = JSON.parse(await readFile('node_modules/@kilasflow/sdk/package.json', 'utf8'));",
	'const expectExports = (from, mod, names) => {',
	'	for (const name of names) {',
	"		if (!(name in mod)) throw new Error(from + ' does not export ' + name);",
	'	}',
	'};',
	"expectExports('@kilasflow/sdk', root, ['SDK_VERSION', 'API_VERSION', 'KilasFlowClient']);",
	"expectExports('@kilasflow/sdk/server', server, ['KilasFlowClient', 'KilasFlowError', 'datastoreFilter', 'tenantClientFactory']);",
	"expectExports('@kilasflow/sdk/browser', browser, ['mountWorkflowEditor', 'subscribeExecutionEvents']);",
	"if (root.SDK_VERSION !== installed.version) {",
	"	throw new Error('dist reports ' + root.SDK_VERSION + ' but the installed manifest says ' + installed.version);",
	'}',
	"console.log('all three subpaths import, SDK_VERSION ' + root.SDK_VERSION);",
	''
].join('\n');

async function packedFiles(tarball) {
	const { stdout } = await run('tar', ['-tzf', tarball]);
	return String(stdout)
		.split('\n')
		.map((line) => line.trim())
		.filter((line) => line !== '' && !line.endsWith('/'));
}

async function main() {
	const options = parseArgs(process.argv.slice(2));
	const manifest = JSON.parse(await readFile(join(sdkDir, 'package.json'), 'utf8'));

	// The pinned TypeScript, resolved the way Node resolves it, so this checks
	// with exactly the compiler the package declares.
	let tscPath;
	try {
		tscPath = require.resolve('typescript/lib/tsc.js');
	} catch {
		tscPath = null;
	}
	if (tscPath === null || !existsSync(tscPath)) {
		console.error('sdk/node_modules/typescript is missing. Run `pnpm install` in sdk/ first.');
		return 1;
	}

	const scratch = await mkdtemp(join(tmpdir(), 'kilasflow-sdk-package-'));
	if (scratch.startsWith(repoDir + sep)) {
		await rm(scratch, { recursive: true, force: true });
		console.error(`the scratch project must live outside the repository, got ${scratch}`);
		return 1;
	}

	const failures = [];
	const ok = (name) => console.log(`ok   ${name}`);
	const fail = (name, error) => {
		const detail = error instanceof Error ? error.message : String(error);
		failures.push(`${name}: ${detail}`);
		console.error(`FAIL ${name}\n${detail}`);
	};
	let blocker = null;

	/** Every phase runs; only a missing prerequisite skips the ones that need it. */
	const step = async (name, body) => {
		if (blocker !== null) {
			failures.push(`${name}: not run, ${blocker}`);
			console.error(`SKIP ${name}: ${blocker}`);
			return false;
		}
		try {
			await body();
			ok(name);
			return true;
		} catch (error) {
			fail(name, error);
			return false;
		}
	};

	try {
		console.log(`scratch: ${scratch}`);
		console.log(
			options.tarball === null
				? options.registry === null
					? 'packing sdk/'
					: `checking the registry package ${options.registry === '' ? `@kilasflow/sdk@${manifest.version}` : options.registry}`
				: `checking the tarball ${resolve(options.tarball)}`
		);

		let tarball = null;
		let files = null;
		let spec = null;
		if (options.tarball !== null) {
			tarball = resolve(options.tarball);
			if (!existsSync(tarball)) {
				console.error(`no such tarball: ${tarball}`);
				return 1;
			}
			files = await packedFiles(tarball);
		} else if (options.registry === null) {
			const packed = await packSdk({ sdkDir, dest: join(scratch, 'pack') });
			tarball = packed.tarball;
			files = packed.files;
			console.log(`packed ${packed.name}@${packed.version}`);
		} else {
			spec = options.registry === '' ? `@kilasflow/sdk@${manifest.version}` : options.registry;
		}

		if (files !== null) {
			await step('tarball file list', async () => {
				const sorted = files.map((path) => path.replace(/^package\//, '')).sort();
				console.log(`files (${sorted.length}):\n${sorted.map((path) => `  ${path}`).join('\n')}`);
				const problems = packProblems(files, manifest);
				if (problems.length > 0) throw new Error(problems.join('\n'));
			});
		}

		await step('scratch project', async () => {
			await writeFile(
				join(scratch, 'package.json'),
				`${JSON.stringify(
					{
						name: 'kilasflow-sdk-consumer',
						version: '0.0.0',
						private: true,
						type: 'module',
						description: 'Scratch project that installs the packed SDK and uses it.'
					},
					null,
					2
				)}\n`
			);
			await writeFile(join(scratch, 'use.ts'), consumerSource);
		});

		const installed = await step('install into the scratch project', async () => {
			const args = ['install', '--no-audit', '--no-fund'];
			// --offline proves the package needs nothing else on the wire; the
			// registry path is the one that must reach it.
			if (tarball !== null) args.push('--offline', tarball);
			else args.push(spec);
			const { stdout } = await run('npm', args, { cwd: scratch });
			console.log(String(stdout).trim());
		});
		if (!installed) blocker = 'the scratch install failed';

		for (const variant of typecheckVariants) {
			await step(`typecheck (${variant.slug})`, async () => {
				const config = {
					compilerOptions: {
						target: 'ES2022',
						module: variant.module,
						moduleResolution: variant.moduleResolution,
						lib: variant.lib,
						strict: true,
						skipLibCheck: variant.skipLibCheck,
						noEmit: true,
						types: []
					},
					files: ['use.ts']
				};
				const configPath = join(scratch, `tsconfig.${variant.slug}.json`);
				await writeFile(configPath, `${JSON.stringify(config, null, 2)}\n`);
				await run(process.execPath, [tscPath, '-p', configPath], { cwd: scratch });
				console.log(
					`  module ${variant.module}, moduleResolution ${variant.moduleResolution}, lib ${variant.lib.join(',')}, skipLibCheck ${variant.skipLibCheck}`
				);
			});
		}

		await step('runtime import of all three subpaths', async () => {
			const { stdout } = await run(process.execPath, ['--input-type=module', '-e', runtimeSource], { cwd: scratch });
			console.log(String(stdout).trim());
		});
	} catch (error) {
		fail('check-package', error);
	} finally {
		if (options.keep) {
			console.log(`kept ${scratch}`);
		} else {
			await rm(scratch, { recursive: true, force: true });
		}
	}

	if (failures.length > 0) {
		console.error(`\n${failures.length} failure(s):\n${failures.map((failure) => `  ${failure}`).join('\n')}`);
		return 1;
	}
	return 0;
}

main()
	.then((code) => {
		process.exitCode = code;
	})
	.catch((error) => {
		console.error(error);
		process.exitCode = 1;
	});
