/**
 * Release rules, as tests.
 *
 * The rules that decide whether a tag may publish, which files may ship and
 * whether the manifest describes the right project are pure functions in
 * `scripts/lib/release.mjs`, so they can be exercised here without a registry
 * and without ceremony. JavaScript, not TypeScript, for the same reason as
 * version.test.mjs: the SDK typechecks under `moduleResolution: bundler` with
 * no node types, so a TypeScript test cannot read files.
 *
 * The canonical source URL is read out of scripts/check-coordinates.sh through
 * the same helper the release CLI uses, so there is one place to change it.
 */
import { execFile } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import {
	changelogProblems,
	distTagFor,
	manifestProblems,
	npmSupportsTrustedPublishing,
	packProblems,
	parseSdkTag,
	publishContextProblems,
	readCanonicalSource
} from '../scripts/lib/release.mjs';

const sdkDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const repoDir = join(sdkDir, '..');

async function readJson(path) {
	return JSON.parse(await readFile(path, 'utf8'));
}

const manifest = await readJson(join(sdkDir, 'package.json'));
const canonicalSource = readCanonicalSource(
	await readFile(join(repoDir, 'scripts', 'check-coordinates.sh'), 'utf8')
);
// Read once at the top: a describe body is synchronous.
const realChangelog = await readFile(join(sdkDir, 'CHANGELOG.md'), 'utf8');

describe('sdk release tag', () => {
	it('parses sdk-v0.1.0 into version 0.1.0', () => {
		expect(parseSdkTag('sdk-v0.1.0')).toEqual({ version: '0.1.0', prerelease: null });
	});

	it('marks sdk-v0.2.0-rc.1 as a prerelease with dist-tag next', () => {
		expect(parseSdkTag('sdk-v0.2.0-rc.1')).toEqual({ version: '0.2.0-rc.1', prerelease: 'rc.1' });
		expect(distTagFor('0.2.0-rc.1')).toBe('next');
		expect(distTagFor('0.1.0')).toBe('latest');
	});

	it.each(['v0.1.0', 'sdk-v0.1', 'sdk-v01.2.3', 'sdk-v0.1.0+build', "sdk-v0.1.0'; rm -rf", 'sdk-v', ''])(
		'rejects everything that is not sdk-vMAJOR.MINOR.PATCH[-pre]: %s',
		(ref) => {
			expect(() => parseSdkTag(ref)).toThrow(`cannot parse release tag "${ref}"`);
		}
	);
});

describe('publish context', () => {
	const releaseRef = `sdk-v${manifest.version}`;
	const insideActions = {
		GITHUB_ACTIONS: 'true',
		GITHUB_EVENT_NAME: 'push',
		GITHUB_REF_TYPE: 'tag',
		GITHUB_REF_NAME: releaseRef
	};

	it('accepts exactly a push of the matching tag inside Actions', () => {
		expect(publishContextProblems(insideActions, manifest)).toEqual([]);
		// "Exactly": a longer ref that merely starts with the release tag is not it.
		expect(publishContextProblems({ ...insideActions, GITHUB_REF_NAME: `${releaseRef}-rc.1` }, manifest)).not.toEqual([]);
	});

	it('refuses a laptop (no GITHUB_ACTIONS)', () => {
		const problems = publishContextProblems({}, manifest).join('\n');
		expect(problems).toContain('GITHUB_ACTIONS');
	});

	it('refuses a branch build', () => {
		const problems = publishContextProblems(
			{ ...insideActions, GITHUB_REF_TYPE: 'branch', GITHUB_REF_NAME: 'main' },
			manifest
		).join('\n');
		expect(problems).toContain('not a tag build');
	});

	it('refuses a tag for another version', () => {
		const problems = publishContextProblems({ ...insideActions, GITHUB_REF_NAME: 'sdk-v0.1.1' }, manifest).join('\n');
		expect(problems).toContain(`is not the release tag ${releaseRef}`);
	});

	it('refuses the image tag namespace (v0.1.0)', () => {
		const problems = publishContextProblems({ ...insideActions, GITHUB_REF_NAME: 'v0.1.0' }, manifest).join('\n');
		expect(problems).toContain('is an image tag');
	});

	it.each(['pull_request', 'workflow_dispatch'])('refuses the %s event', (event) => {
		const problems = publishContextProblems({ ...insideActions, GITHUB_EVENT_NAME: event }, manifest).join('\n');
		expect(problems).toContain(`GITHUB_EVENT_NAME`);
		expect(problems).toContain(event);
	});
});

describe('changelog', () => {
	it('the real CHANGELOG has exactly one entry for the manifest version', () => {
		expect(changelogProblems(realChangelog, manifest.version)).toEqual([]);
	});

	it('every bullet of every entry states Additive, Fix or Breaking', () => {
		const twoEntries = [
			'## 2.0.0',
			'',
			'- **Breaking**: drops Node 18',
			'',
			'## 1.1.0',
			'',
			'- **Fix**: correct the retry count',
			'',
			'## Unreleased',
			'',
			'prose is not a bullet',
			''
		].join('\n');
		expect(changelogProblems(twoEntries, '2.0.0')).toEqual([]);
		// The rule is checked on every release section, not only the tagged one.
		const oldEntryUnlabelled = twoEntries.replace('- **Fix**: correct the retry count', '- correct the retry count');
		expect(changelogProblems(oldEntryUnlabelled, '2.0.0').join('\n')).toContain('is not labelled');
		expect(changelogProblems(realChangelog, manifest.version)).toEqual([]);
	});

	it.each([
		['missing heading', ['## 0.2.0', '', '- **Additive**: later work', ''].join('\n'), 'exactly one'],
		[
			'duplicate heading',
			['## 0.1.0', '', '- **Additive**: a', '', '## 0.1.0', '', '- **Additive**: b', ''].join('\n'),
			'found 2'
		],
		['empty entry', ['## 0.1.0', '', '## 0.2.0', '', '- **Additive**: later', ''].join('\n'), 'has no bullets'],
		['unlabeled bullet', ['## 0.1.0', '', '- the first release', ''].join('\n'), 'is not labelled'],
		['**Changed** label', ['## 0.1.0', '', '- **Changed**: everything', ''].join('\n'), '**Changed**']
	])('rejects a changelog with a %s', (name, text, reason) => {
		expect(changelogProblems(text, '0.1.0').join('\n')).toContain(reason);
	});
});

/**
 * README.md and CHANGELOG.md are baked into the immutable tarball, so they ship
 * forever: a sentence about whether the package is on the registry is wrong on
 * one side of the first publish either way. Publication status lives in the
 * repo-level files only (sdk/RELEASING.md lists them), never in the package.
 */
describe('shipped prose', () => {
	const publicationClaims = /not on npm|not (yet )?published|isn.t on npm|answers 404|no release has been cut/i;

	it.each(['README.md', 'CHANGELOG.md'])('%s makes no claim about publication state', async (file) => {
		const prose = await readFile(join(sdkDir, file), 'utf8');
		const match = prose.match(publicationClaims);
		expect(match?.[0], `${file} says ${JSON.stringify(match?.[0])} about the registry`).toBeUndefined();
	});
});

describe('packed file list', () => {
	// What `npm pack` reports, including the `package/` directory prefix.
	const intended = [
		'package.json',
		'README.md',
		'CHANGELOG.md',
		'LICENSE',
		'dist/index.js',
		'dist/index.d.ts',
		'dist/server.js',
		'dist/server.d.ts',
		'dist/browser.js',
		'dist/browser.d.ts',
		'dist/http.js',
		'dist/http.d.ts',
		'dist/version.js',
		'dist/version.d.ts',
		'dist/generated/models.js',
		'dist/generated/models.d.ts'
	].map((path) => `package/${path}`);

	const without = (path) => intended.filter((entry) => entry !== `package/${path}`);

	it('accepts the intended tarball', () => {
		expect(packProblems(intended, manifest)).toEqual([]);
	});

	it('names every leak', () => {
		const leaks = [
			'src/server.ts',
			'test/x.test.ts',
			'.tmp/openapi.json',
			'tsconfig.json',
			'pnpm-lock.yaml',
			'examples/host-page/server.mjs',
			'dist/server.js.map',
			'dist/x.ts',
			'dist/.tsbuildinfo'
		];
		const problems = packProblems(
			[...intended, ...leaks.map((path) => `package/${path}`)],
			manifest
		);
		for (const leak of leaks) {
			expect(problems).toContain(`unexpected file in tarball: ${leak}`);
		}
	});

	it('requires package.json, README.md, CHANGELOG.md, LICENSE and every exports target', () => {
		for (const path of ['package.json', 'README.md', 'CHANGELOG.md', 'LICENSE']) {
			expect(packProblems(without(path), manifest)).toContain(`missing from tarball: ${path}`);
		}
		for (const subpath of ['.', './server', './browser']) {
			for (const target of [manifest.exports[subpath].types, manifest.exports[subpath].import]) {
				const path = target.replace(/^\.\//, '');
				expect(packProblems(without(path), manifest)).toContain(`missing from tarball: ${path}`);
			}
		}
	});
});

describe('manifest', () => {
	it('repository, homepage and bugs sit under the canonical source', () => {
		expect(canonicalSource).toMatch(/^https:\/\/github\.com\/[^/]+\/[^/]+$/);
		expect(manifestProblems(manifest, canonicalSource)).toEqual([]);
		const wrong = (patch) => manifestProblems({ ...manifest, ...patch }, canonicalSource).join('\n');
		expect(wrong({ homepage: 'https://example.com/#readme' })).toContain('homepage');
		expect(wrong({ bugs: 'https://example.com/issues' })).toContain('bugs');
		expect(
			wrong({ repository: { type: 'git', url: 'git+https://example.com/x.git', directory: 'sdk' } })
		).toContain('repository');
		expect(wrong({ repository: { type: 'git', url: `git+${canonicalSource}.git`, directory: '.' } })).toContain(
			'directory'
		);
	});

	it('declares Apache-2.0 and is not private', () => {
		expect(manifest.license).toBe('Apache-2.0');
		expect(manifest.private).not.toBe(true);
		expect(manifestProblems({ ...manifest, license: 'MIT' }, canonicalSource).join('\n')).toContain('license');
		expect(manifestProblems({ ...manifest, private: true }, canonicalSource).join('\n')).toContain('private');
	});

	it('publishes as public', () => {
		expect(manifest.publishConfig?.access).toBe('public');
		expect(
			manifestProblems({ ...manifest, publishConfig: { provenance: true } }, canonicalSource).join('\n')
		).toContain('publishConfig');
	});

	it('exposes exactly the three subpaths, each with types and import under ./dist/', () => {
		expect(Object.keys(manifest.exports).sort()).toEqual(['.', './browser', './server']);
		for (const subpath of ['.', './server', './browser']) {
			expect(manifest.exports[subpath].types).toMatch(/^\.\/dist\/.+\.d\.ts$/);
			expect(manifest.exports[subpath].import).toMatch(/^\.\/dist\/.+\.js$/);
		}
		const withExports = (exports) => manifestProblems({ ...manifest, exports }, canonicalSource).join('\n');
		expect(
			withExports({ ...manifest.exports, './extra': { types: './dist/extra.d.ts', import: './dist/extra.js' } })
		).toContain('exports');
		expect(withExports({ ...manifest.exports, '.': { import: './dist/index.js' } })).toContain('types');
		expect(withExports({ ...manifest.exports, './server': { types: './dist/server.d.ts' } })).toContain('import');
	});

	it('runs no install-time script', () => {
		for (const name of ['install', 'preinstall', 'postinstall', 'prepare']) {
			expect(manifest.scripts?.[name]).toBeUndefined();
			expect(
				manifestProblems({ ...manifest, scripts: { ...manifest.scripts, [name]: 'echo nope' } }, canonicalSource).join('\n')
			).toContain(name);
		}
		// The two lifecycle hooks a release does need stay allowed.
		expect(
			manifestProblems(
				{ ...manifest, scripts: { ...manifest.scripts, prepack: 'npm run build', prepublishOnly: 'node scripts/release.mjs guard' } },
				canonicalSource
			)
		).toEqual([]);
	});

	it('ships a LICENSE byte-identical to the repository LICENSE', async () => {
		const [shipped, root] = await Promise.all([
			readFile(join(sdkDir, 'LICENSE'), 'utf8'),
			readFile(join(repoDir, 'LICENSE'), 'utf8')
		]);
		expect(shipped).toBe(root);
	});

	it('the host-page example pins exactly the version being released', async () => {
		const example = await readJson(join(sdkDir, 'examples', 'host-page', 'package.json'));
		expect(example.dependencies['@kilasflow/sdk']).toBe(manifest.version);
	});
});

/**
 * `npm --version` is only trusted-publisher-capable from 11.5.1. The release
 * workflow runs the CLI's `npm-version` command before anything else, so this
 * pure predicate is what decides whether the runner can publish at all.
 */
describe('npmSupportsTrustedPublishing', () => {
	it.each([
		['11.5.1', true],
		['11.5.0', false],
		['10.9.2', false],
		['11.13.0', true],
		['12.0.0', true]
	])('npm %s -> %s', (version, supported) => {
		expect(npmSupportsTrustedPublishing(version)).toBe(supported);
	});
});

/**
 * The release CLI is what the workflow and the `prepublishOnly` hook run, so it
 * is exercised as a subprocess: the exit code and the stream the reasons land on
 * are the whole contract. `process.execPath` is spawned directly so the child's
 * environment can be set exactly — an "empty environment" test that inherited
 * PATH would not be empty.
 */
describe('release CLI', () => {
	const cli = join(sdkDir, 'scripts', 'release.mjs');

	function runCli(args, env) {
		return new Promise((resolve) => {
			execFile(process.execPath, [cli, ...args], { cwd: sdkDir, env }, (error, stdout, stderr) => {
				resolve({ code: error ? (error.code ?? 1) : 0, stdout, stderr });
			});
		});
	}

	const releaseEnv = {
		GITHUB_ACTIONS: 'true',
		GITHUB_EVENT_NAME: 'push',
		GITHUB_REF_TYPE: 'tag',
		GITHUB_REF_NAME: `sdk-v${manifest.version}`
	};

	it('guard passes for a push of the matching tag inside Actions', async () => {
		const result = await runCli(['guard'], releaseEnv);
		expect(result.code).toBe(0);
	});

	it('guard refuses an empty environment and names the workflow', async () => {
		const result = await runCli(['guard'], {});
		expect(result.code).toBe(1);
		expect(result.stderr).toContain('GITHUB_ACTIONS');
		expect(result.stderr).toContain('only from the release workflow');
	});

	it.each([
		['GITHUB_ACTIONS', { ...releaseEnv, GITHUB_ACTIONS: 'false' }, 'GITHUB_ACTIONS'],
		['GITHUB_EVENT_NAME', { ...releaseEnv, GITHUB_EVENT_NAME: 'workflow_dispatch' }, 'GITHUB_EVENT_NAME'],
		['GITHUB_REF_TYPE', { ...releaseEnv, GITHUB_REF_TYPE: 'branch' }, 'GITHUB_REF_TYPE'],
		['GITHUB_REF_NAME', { ...releaseEnv, GITHUB_REF_NAME: 'sdk-v9.9.9' }, 'sdk-v9.9.9']
	])('guard refuses %s alone being wrong', (name, env, fragment) => {
		return runCli(['guard'], env).then((result) => {
			expect(result.code).toBe(1);
			expect(result.stderr).toContain(fragment);
		});
	});

	it('guard waives the context for a dry run', async () => {
		const result = await runCli(['guard'], { npm_config_dry_run: 'true' });
		expect(result.code).toBe(0);
		expect(result.stdout).toContain('not enforced');
		expect(result.stdout).toMatch(/dry run/i);
	});

	it('facts prints version= and dist_tag= for the manifest version', async () => {
		const result = await runCli(['facts', `sdk-v${manifest.version}`], {});
		expect(result.code).toBe(0);
		expect(result.stdout).toBe(`version=${manifest.version}\ndist_tag=latest\n`);
	});

	it('facts exits 1 for a tag that is not this version', async () => {
		const result = await runCli(['facts', 'sdk-v9.9.9'], {});
		expect(result.code).toBe(1);
		expect(result.stderr).toContain('9.9.9');
	});

	it('the prepublishOnly hook is the guard', () => {
		expect(manifest.scripts.prepublishOnly).toBe('node scripts/release.mjs guard');
	});
});
