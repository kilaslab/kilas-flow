/**
 * The release rules, as pure functions.
 *
 * Everything here is a decision about whether a release may happen, written as
 * a function of its inputs rather than as shell: the tag parser, the publish
 * context check, the changelog vocabulary, the tarball allowlist and the
 * manifest checks. `scripts/release.mjs` (the CLI the release workflow runs)
 * and `test/release.test.mjs` both call these, so the rules that gate a publish
 * are the rules that are tested, with no second copy to drift.
 *
 * No I/O: no readFile, no execFile, no process.env. A caller reads the file and
 * hands over the text, which is what makes these testable without a registry,
 * a tag or a workflow run.
 */

/** The one release-tag grammar: `sdk-v<major>.<minor>.<patch>[-prerelease]`. */
const TAG_PATTERN = /^sdk-v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;

/** A `## x.y.z` changelog heading. `## Unreleased` and prose headings are not releases. */
const RELEASE_HEADING = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;

/** The files a tarball may carry besides the compiled `dist/` tree. */
const ROOT_FILES = ['package.json', 'README.md', 'CHANGELOG.md', 'LICENSE'];

/** Lifecycle hooks that would run on a consumer's machine. */
const INSTALL_SCRIPTS = ['install', 'preinstall', 'postinstall', 'prepare'];

const SUBPATHS = ['.', './server', './browser'];

/**
 * The canonical source URL, read out of `scripts/check-coordinates.sh` rather
 * than written a second time here: that script exists because the project's
 * coordinates were once wrong by one letter, and the manifest must not be able
 * to disagree with it.
 */
export function readCanonicalSource(shellText) {
	const match = typeof shellText === 'string' ? shellText.match(/^canonical_source='([^']+)'$/m) : null;
	if (!match) {
		throw new Error('no canonical_source= line found; cannot check the manifest against the canonical source URL');
	}
	return match[1];
}

/**
 * Parse a release tag. Throws — naming the offending ref, because this runs on
 * a tag pushed by hand and the error is the only explanation a maintainer gets.
 */
export function parseSdkTag(ref) {
	const match = typeof ref === 'string' ? ref.match(TAG_PATTERN) : null;
	if (!match) {
		throw new Error(`cannot parse release tag "${ref}": expected sdk-vMAJOR.MINOR.PATCH[-prerelease]`);
	}
	const prerelease = match[4] ?? null;
	return {
		version: prerelease ? `${match[1]}.${match[2]}.${match[3]}-${prerelease}` : `${match[1]}.${match[2]}.${match[3]}`,
		prerelease
	};
}

/**
 * npm refuses a prerelease publish without `--tag` ("You must specify a tag
 * using --tag when publishing a prerelease version"), so the tag the workflow
 * passes is derived from the version rather than left to npm's default.
 */
export function distTagFor(version) {
	return String(version).includes('-') ? 'next' : 'latest';
}

/**
 * Whether the npm CLI is new enough for trusted publishing, which the release
 * workflow relies on for provenance: `npm trust` and OIDC publishing arrive in
 * 11.5.1. Compared field by field rather than by string order, because "11.10.0"
 * is newer than "11.5.1" and sorts before it lexically.
 */
export function npmSupportsTrustedPublishing(version) {
	const [major = 0, minor = 0, patch = 0] = String(version)
		.trim()
		.replace(/^v/, '')
		.split('.')
		.map((part) => Number.parseInt(part, 10) || 0);
	if (major !== 11) return major > 11;
	if (minor !== 5) return minor > 5;
	return patch >= 1;
}

/**
 * The publish-context rules, as a list of reasons. An empty list means the
 * environment is exactly a push of the tag matching this manifest.
 */
export function publishContextProblems(env, manifest) {
	const problems = [];
	const expected = `sdk-v${manifest.version}`;
	const shown = (name) => (env[name] === undefined ? 'unset' : JSON.stringify(env[name]));

	if (env.GITHUB_ACTIONS !== 'true') {
		problems.push(`not running inside GitHub Actions (GITHUB_ACTIONS is ${shown('GITHUB_ACTIONS')})`);
	}
	if (env.GITHUB_EVENT_NAME !== 'push') {
		problems.push(`not a push event (GITHUB_EVENT_NAME is ${shown('GITHUB_EVENT_NAME')})`);
	}
	if (env.GITHUB_REF_TYPE !== 'tag') {
		problems.push(`not a tag build (GITHUB_REF_TYPE is ${shown('GITHUB_REF_TYPE')})`);
	}
	if (env.GITHUB_REF_NAME !== expected) {
		if (/^v\d/.test(env.GITHUB_REF_NAME ?? '')) {
			problems.push(`the ref ${shown('GITHUB_REF_NAME')} is an image tag; SDK releases publish from ${expected}`);
		} else {
			problems.push(`the ref ${shown('GITHUB_REF_NAME')} is not the release tag ${expected}`);
		}
	}
	return problems;
}

/**
 * The changelog rules: exactly one section for the version being released, at
 * least one bullet in every release section, and every bullet labelled with the
 * vocabulary the API contract defines (additive, fix, breaking). The labels are
 * the ones that decide the next version number, so an unlabelled bullet makes
 * the entry unreadable to anyone deciding whether a bump is safe.
 */
export function changelogProblems(text, version) {
	const problems = [];
	const sections = [];
	let current = null;

	for (const line of String(text).split(/\r?\n/)) {
		const heading = line.match(/^##\s+(.+?)\s*$/);
		if (heading) {
			current = { title: heading[1], bullets: [] };
			sections.push(current);
			continue;
		}
		if (current && line.startsWith('- ')) {
			current.bullets.push(line);
		}
	}

	const named = sections.filter((section) => section.title === version);
	if (named.length !== 1) {
		problems.push(`expected exactly one ## ${version} heading, found ${named.length}`);
	}

	for (const section of sections.filter((entry) => RELEASE_HEADING.test(entry.title))) {
		if (section.bullets.length === 0) {
			problems.push(`changelog entry ${section.title} has no bullets`);
		}
		for (const bullet of section.bullets) {
			if (!/^- \*\*(Additive|Fix|Breaking)\*\*: /.test(bullet)) {
				problems.push(`changelog bullet is not labelled Additive, Fix or Breaking: ${bullet}`);
			}
		}
	}

	return problems;
}

/** Every `types`/`import` target the exports map points at, repo-relative. */
function exportsTargets(manifest) {
	const targets = new Set();
	for (const entry of Object.values(manifest.exports ?? {})) {
		if (typeof entry !== 'object' || entry === null) continue;
		for (const value of [entry.types, entry.import]) {
			if (typeof value === 'string') targets.add(value.replace(/^\.\//, ''));
		}
	}
	return [...targets];
}

/** `npm pack` reports paths under `package/`; strip it, and any trailing slash. */
function normalisePackedPath(path) {
	return String(path).replace(/^package\//, '').replace(/\/+$/, '');
}

/**
 * Whether one path may appear in the tarball: the four root files, or a
 * compiled `.js`/`.d.ts` under `dist/`. Maps, tests, dot-segments (`.tmp`,
 * `.tsbuildinfo`) and anything unbuilt are all leaks.
 */
function isPackable(path) {
	if (ROOT_FILES.includes(path)) return true;
	if (!path.startsWith('dist/')) return false;
	if (path.endsWith('.map') || path.includes('.test.')) return false;
	if (path.split('/').some((segment) => segment.startsWith('.'))) return false;
	return /^dist\/.+\.(?:js|d\.ts)$/.test(path);
}

/**
 * The tarball rules: nothing unexpected, and nothing missing — in particular
 * every target the exports map names, because a missing `dist/server.d.ts`
 * fails only for consumers on `moduleResolution: node16`, which is not the
 * setting the author tests on.
 */
export function packProblems(paths, manifest) {
	const present = new Set(paths.map(normalisePackedPath));
	const problems = [];

	for (const path of [...ROOT_FILES, ...exportsTargets(manifest)]) {
		if (!present.has(path)) problems.push(`missing from tarball: ${path}`);
	}
	for (const path of present) {
		if (!isPackable(path)) problems.push(`unexpected file in tarball: ${path}`);
	}

	return problems;
}

/**
 * The manifest rules a published package is judged by: the coordinates must
 * name this project (provenance requires `repository.url` to match the source
 * repository exactly), the licence must be the repository's, the entry points
 * must be the three subpaths with declarations, and nothing may run on a
 * consumer's machine at install time.
 */
export function manifestProblems(manifest, canonicalSource) {
	const problems = [];
	const repository = manifest.repository;
	const shown = (value) => JSON.stringify(value ?? null);

	if (repository?.type !== 'git') {
		problems.push(`repository.type must be git, found ${shown(repository?.type)}`);
	}
	if (repository?.url !== `git+${canonicalSource}.git`) {
		problems.push(`repository.url must be git+${canonicalSource}.git, found ${shown(repository?.url)}`);
	}
	if (repository?.directory !== 'sdk') {
		problems.push(`repository.directory must be sdk, found ${shown(repository?.directory)}`);
	}
	if (manifest.homepage !== `${canonicalSource}#readme`) {
		problems.push(`homepage must be ${canonicalSource}#readme, found ${shown(manifest.homepage)}`);
	}
	if (manifest.bugs !== `${canonicalSource}/issues`) {
		problems.push(`bugs must be ${canonicalSource}/issues, found ${shown(manifest.bugs)}`);
	}
	if (manifest.license !== 'Apache-2.0') {
		problems.push(`license must be Apache-2.0 to match the repository LICENSE, found ${shown(manifest.license)}`);
	}
	if (manifest.private === true) {
		problems.push('private must not be true: this package is published');
	}
	if (manifest.publishConfig?.access !== 'public') {
		problems.push(`publishConfig.access must be public, found ${shown(manifest.publishConfig?.access)}`);
	}

	const keys = Object.keys(manifest.exports ?? {});
	const missing = SUBPATHS.filter((subpath) => !keys.includes(subpath));
	const extra = keys.filter((key) => !SUBPATHS.includes(key));
	if (missing.length > 0) problems.push(`exports is missing ${missing.join(', ')}`);
	if (extra.length > 0) problems.push(`exports has unexpected subpaths: ${extra.join(', ')}`);
	for (const subpath of SUBPATHS) {
		const entry = manifest.exports?.[subpath];
		if (typeof entry?.types !== 'string' || !/^\.\/dist\/.+\.d\.ts$/.test(entry.types)) {
			problems.push(`exports["${subpath}"].types must name a declaration file under ./dist/, found ${shown(entry?.types)}`);
		}
		if (typeof entry?.import !== 'string' || !/^\.\/dist\/.+\.js$/.test(entry.import)) {
			problems.push(`exports["${subpath}"].import must name a module under ./dist/, found ${shown(entry?.import)}`);
		}
	}

	for (const name of INSTALL_SCRIPTS) {
		if (typeof manifest.scripts?.[name] === 'string') {
			problems.push(`scripts.${name} runs on every install and must not ship: ${manifest.scripts[name]}`);
		}
	}

	return problems;
}
