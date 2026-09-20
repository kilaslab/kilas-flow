/**
 * The release CLI: the one entry point the workflow and the `prepublishOnly`
 * hook run.
 *
 *   node scripts/release.mjs guard        # refuse a publish outside a tag push
 *   node scripts/release.mjs facts <tag>  # print version= and dist_tag= for $GITHUB_OUTPUT
 *   node scripts/release.mjs npm-version  # refuse an npm too old for trusted publishing
 *
 * `guard` exists to catch the foot-gun — a maintainer running `npm publish`
 * from a laptop — not to be a security boundary: `npm publish <tarball>` runs no
 * lifecycle scripts, so it never reaches this. The real controls are the
 * registry-side trusted publisher plus "disallow tokens", and a tag ruleset;
 * both are owner-only and are described in RELEASING.md. What this does give is
 * a second, earlier refusal in the workflow itself, before the publish step.
 *
 * The rules themselves are pure functions in `lib/release.mjs`, so they are the
 * ones the tests exercise; this file only does the reading and the exiting.
 */
import { execFile } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

import {
	changelogProblems,
	distTagFor,
	manifestProblems,
	npmSupportsTrustedPublishing,
	parseSdkTag,
	publishContextProblems,
	readCanonicalSource
} from './lib/release.mjs';

const execFileAsync = promisify(execFile);

const sdkDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const repoDir = join(sdkDir, '..');

// Importing the module would execute it; the version source must stay a read,
// exactly as the Makefile's sdk-version-check reads it.
const SDK_VERSION_PATTERN = /export const SDK_VERSION\s*=\s*["']([^"']+)/;

const WORKFLOW_SENTENCE =
	'publishing happens only from the release workflow on a push of the matching sdk-v tag';

async function readJson(path) {
	return JSON.parse(await readFile(path, 'utf8'));
}

function fail(problems) {
	for (const problem of problems) process.stderr.write(`${problem}\n`);
	process.exit(1);
}

/**
 * `guard`: a laptop publish is refused, a dry run is allowed (npm exports
 * `npm_config_dry_run=true` to the hook during `npm publish --dry-run`, and a
 * dry run touches no registry).
 */
async function guard() {
	if (process.env.npm_config_dry_run === 'true') {
		process.stdout.write('release guard: dry run; the publish context is not enforced for a dry run\n');
		return;
	}
	const manifest = await readJson(join(sdkDir, 'package.json'));
	const problems = publishContextProblems(process.env, manifest);
	if (problems.length > 0) fail([...problems, WORKFLOW_SENTENCE]);
	process.stdout.write(`release guard: publish context is a push of sdk-v${manifest.version}\n`);
}

/**
 * `facts <tag>`: every release rule that can be checked without a registry, in
 * one place, so the workflow fails before it builds or publishes. On success it
 * prints ONLY the two `key=value` lines, because its stdout is appended to
 * $GITHUB_OUTPUT.
 */
async function facts(tag) {
	const [manifest, versionSource, changelog, shippedLicence, rootLicence, coordinates] = await Promise.all([
		readJson(join(sdkDir, 'package.json')),
		readFile(join(sdkDir, 'src', 'version.ts'), 'utf8'),
		readFile(join(sdkDir, 'CHANGELOG.md'), 'utf8'),
		readFile(join(sdkDir, 'LICENSE'), 'utf8'),
		readFile(join(repoDir, 'LICENSE'), 'utf8'),
		readFile(join(repoDir, 'scripts', 'check-coordinates.sh'), 'utf8')
	]);

	const problems = [];
	let version;
	try {
		({ version } = parseSdkTag(tag));
	} catch (error) {
		problems.push(error.message);
	}

	const sourceVersion = (versionSource.match(SDK_VERSION_PATTERN) ?? [])[1];
	if (!sourceVersion) {
		problems.push('no `export const SDK_VERSION = "..."` line in sdk/src/version.ts');
	} else if (sourceVersion !== manifest.version) {
		problems.push(`sdk/src/version.ts SDK_VERSION (${sourceVersion}) != sdk/package.json version (${manifest.version}); bump both together`);
	}
	if (version !== undefined && version !== manifest.version) {
		problems.push(`tag ${tag} names version ${version} but sdk/package.json is ${manifest.version}; bump both together`);
	}
	if (shippedLicence !== rootLicence) {
		problems.push('sdk/LICENSE is not byte-identical to the repository LICENSE');
	}
	problems.push(...changelogProblems(changelog, manifest.version));
	problems.push(...manifestProblems(manifest, readCanonicalSource(coordinates)));

	if (problems.length > 0) fail(problems);
	process.stdout.write(`version=${manifest.version}\ndist_tag=${distTagFor(manifest.version)}\n`);
}

/**
 * `npm-version`: OIDC publishing and `npm trust` need npm 11.5.1; a runner on
 * an older npm would fail late, after every gate, so this fails first.
 */
async function npmVersion() {
	let current;
	try {
		current = (await execFileAsync('npm', ['--version'])).stdout.trim();
	} catch (error) {
		fail([`cannot run npm --version: ${error.message}`]);
	}
	if (!npmSupportsTrustedPublishing(current)) {
		fail([
			`npm ${current} does not support trusted publishing; npm 11.5.1 or later is required for OIDC provenance and \`npm trust\``
		]);
	}
	process.stdout.write(`npm ${current} supports trusted publishing\n`);
}

const [command, argument] = process.argv.slice(2);

switch (command) {
	case 'guard':
		await guard();
		break;
	case 'facts':
		if (!argument) fail(['usage: node scripts/release.mjs facts <sdk-vX.Y.Z tag>']);
		await facts(argument);
		break;
	case 'npm-version':
		await npmVersion();
		break;
	default:
		fail([`usage: node scripts/release.mjs <guard|facts <tag>|npm-version> (got ${JSON.stringify(command ?? null)})`]);
}
