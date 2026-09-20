/**
 * The release workflow, as tests.
 *
 * `release.yml` is the only place a real `npm publish` runs, and its prose
 * comments name the very tokens the rules are about (`id-token: write`,
 * `npm publish --dry-run`, `--provenance`, `${{ github.ref_name }}`). Matching
 * that text would pass or fail for the wrong reason, so this file parses the
 * YAML with the `yaml` package and asserts over the parsed structure.
 *
 * The rules enforced here are the ones a reviewer cannot hold in their head
 * across two jobs: an sdk tag must not start the image publisher, the publish
 * step must be the only one holding a tag and a token, and the workflow must
 * run the same gates `make sdk-release-check` runs, in the same order — so the
 * laptop command and the pipeline cannot drift apart.
 *
 * `ci.yml` is parsed too, for the half of that same recipe its sdk job runs and
 * the one target it leaves to the drift job, because "CI and the recipe agree"
 * is otherwise a claim in a ticket that nothing checks.
 */
import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';
import { parse } from 'yaml';

const sdkDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const repoDir = join(sdkDir, '..');

const workflow = parse(await readFile(join(repoDir, '.github', 'workflows', 'release.yml'), 'utf8'));
const ciWorkflow = parse(await readFile(join(repoDir, '.github', 'workflows', 'ci.yml'), 'utf8'));
const makefile = await readFile(join(repoDir, 'Makefile'), 'utf8');

// YAML 1.1 would read the `on:` key as a boolean; the `yaml` package's default
// core schema keeps it a string, but resolve either spelling so the test states
// its intent rather than the parser's version.
const triggers = (workflow.on ?? workflow[true])?.push?.tags ?? [];
const jobs = workflow.jobs;

// Every `run:` string in the file, with the job and step it belongs to, so a
// test can ask "is there a publish anywhere" rather than "is there one in the
// step I already know about".
function runSteps() {
	const steps = [];
	for (const [job, jobDefinition] of Object.entries(jobs)) {
		for (const step of jobDefinition.steps ?? []) {
			if (typeof step.run === 'string') steps.push({ job, step, run: step.run });
		}
	}
	return steps;
}

// The tab-indented recipe lines of a Make target, which is what `make` executes.
function recipeLines(target) {
	const lines = makefile.split('\n');
	const start = lines.findIndex((line) => line.startsWith(`${target}:`));
	if (start === -1) throw new Error(`no ${target} target in the Makefile`);
	const recipe = [];
	for (let i = start + 1; i < lines.length; i += 1) {
		if (!lines[i].startsWith('\t')) break;
		recipe.push(lines[i]);
	}
	return recipe;
}

// The `$(MAKE) <target>` names a recipe runs: the list a workflow has to agree
// with, shared by the release and CI parity tests below.
function recipeTargets(target) {
	return recipeLines(target)
		.map((line) => (line.match(/\$\(MAKE\)\s+(\S+)/) ?? [])[1])
		.filter(Boolean);
}

describe('release workflow triggers', () => {
	it('triggers on exactly v* and sdk-v* tags', () => {
		expect([...triggers].sort()).toEqual(['sdk-v*', 'v*']);
	});
});

describe('release workflow jobs', () => {
	it('the image job only runs for v* tags', () => {
		expect(jobs.image.if).toBe("startsWith(github.ref, 'refs/tags/v')");
	});

	it('the sdk job only runs for sdk-v* tags', () => {
		expect(jobs.sdk.if).toBe("startsWith(github.ref, 'refs/tags/sdk-v')");
	});

	it('the sdk job holds id-token: write and never contents: write or packages: write', () => {
		const permissions = jobs.sdk.permissions;
		expect(permissions['id-token']).toBe('write');
		expect(permissions.contents).toBe('read');
		expect(permissions.packages).toBeUndefined();
	});

	it('every third-party action is pinned to a 40-hex SHA', () => {
		const uses = [];
		for (const jobDefinition of Object.values(jobs)) {
			for (const step of jobDefinition.steps ?? []) {
				if (typeof step.uses === 'string') uses.push(step.uses);
			}
		}
		// Non-vacuous: there really are third-party actions to pin.
		expect(uses.some((use) => !use.startsWith('./'))).toBe(true);
		for (const use of uses) {
			if (use.startsWith('./')) continue;
			expect(use).toMatch(/@[0-9a-f]{40}$/);
		}
	});
});

describe('release workflow publish step', () => {
	// Every `npm publish` that is not a dry run, with the job it sits in.
	function publishers() {
		return runSteps().filter(({ run }) => /npm publish/.test(run) && !/--dry-run/.test(run));
	}

	it('there is exactly one non-dry-run npm publish and it lives in the sdk job', () => {
		const found = publishers();
		expect(found).toHaveLength(1);
		expect(found[0].job).toBe('sdk');
	});

	it('the publish passes --provenance and a --tag taken from the release facts step', () => {
		const publish = publishers()[0].step;
		expect(publish.run).toContain('--provenance');
		expect(publish.run).toMatch(/--tag\s+"\$DIST_TAG"/);
		expect(publish.env.DIST_TAG).toBe('${{ steps.release.outputs.dist_tag }}');
	});

	it('NPM_TOKEN appears only in the env of the publish step', () => {
		const carrying = [];
		for (const jobDefinition of Object.values(jobs)) {
			for (const step of jobDefinition.steps ?? []) {
				if (JSON.stringify(step).includes('NPM_TOKEN')) carrying.push(step);
			}
		}
		expect(carrying).toHaveLength(1);
		expect(carrying[0].env.NPM_TOKEN).toBe('${{ secrets.NPM_TOKEN }}');
	});

	it('the tag reaches shell steps through env, never interpolated into a run line', () => {
		for (const { job, run } of runSteps()) {
			expect(run, `${job} interpolates github.ref_name into a run line`).not.toContain('github.ref_name');
		}
		// Non-vacuous: the tag is passed, just through env.
		expect(JSON.stringify(jobs)).toContain('github.ref_name');
	});
});

describe('release workflow sdk job steps', () => {
	const sdkSteps = jobs.sdk.steps ?? [];
	const installIndex = sdkSteps.findIndex((step) => /pnpm install/.test(step.run ?? ''));

	// The steps that gate the release, publish it, or read the release facts —
	// the only ones that need the dependencies. Asserted in both directions
	// below: nothing before the install is one of these, and everything after
	// it is.
	const isGateStep = (run) =>
		/^make \S+$/.test(run) || /^node sdk\/scripts\/release\.mjs \S/.test(run) || /npm publish/.test(run);

	it('installs dependencies before anything else runs', () => {
		expect(installIndex).toBeGreaterThan(-1);
		// The install is only "before anything else" if no gate, release CLI or
		// publish step precedes it. The sibling below slices from after this
		// index, so without this assertion the install could move to the end of
		// the job — putting every gate, and the publish whose `prepack` runs
		// `tsc`, behind an install that no longer precedes them — and both
		// tests would still pass.
		const before = sdkSteps
			.slice(0, installIndex)
			.map((step) => (step.run ?? '').trim())
			.filter((run) => isGateStep(run));
		expect(before, 'steps that run before the SDK dependencies are installed').toEqual([]);
	});

	it('after the install step, every step is a make target, the release CLI or the publish', () => {
		expect(installIndex).toBeGreaterThan(-1);
		for (const step of sdkSteps.slice(installIndex + 1)) {
			const run = (step.run ?? '').trim();
			expect(isGateStep(run), `unexpected sdk-job step after install: ${JSON.stringify(step)}`).toBe(true);
		}
	});

	it('runs exactly the make targets that sdk-release-check names, in the same order', () => {
		const expected = recipeTargets('sdk-release-check');
		// Non-vacuous: the Makefile really does name targets.
		expect(expected.length).toBeGreaterThan(3);
		const actual = sdkSteps
			.map((step) => (step.run ?? '').trim())
			.filter((run) => run.startsWith('make '))
			.map((run) => run.replace(/^make\s+/, ''));
		expect(actual).toEqual(expected);
	});
});

/**
 * The same recipe, as CI runs it. `ci.yml` splits the gate on purpose: its sdk
 * job runs every target except `generate-types-check` — the one that needs a
 * binary built from this tree — which the drift job runs with the Go toolchain
 * it already has. Both halves are pinned so the split stays deliberate and no
 * target can be wired into one workflow only.
 */
describe('ci workflow sdk job', () => {
	const sdkJobRuns = (ciWorkflow.jobs.sdk.steps ?? []).map((step) => (step.run ?? '').trim());
	const allCiRuns = Object.values(ciWorkflow.jobs).flatMap((job) =>
		(job.steps ?? []).map((step) => (step.run ?? '').trim())
	);

	it('runs the sdk-release-check recipe minus generate-types-check, in the same order', () => {
		const expected = recipeTargets('sdk-release-check').filter((target) => target !== 'generate-types-check');
		// Non-vacuous: the recipe names targets, and the filter removed one.
		expect(expected.length).toBeGreaterThan(3);
		expect(recipeTargets('sdk-release-check')).toContain('generate-types-check');
		const actual = sdkJobRuns.filter((run) => run.startsWith('make ')).map((run) => run.replace(/^make\s+/, ''));
		expect(actual).toEqual(expected);
	});

	it('runs every recipe target somewhere, so the one the sdk job skips is still gated', () => {
		const absent = recipeTargets('sdk-release-check').filter((target) => !allCiRuns.includes(`make ${target}`));
		expect(absent, `recipe targets no CI job runs: ${absent.join(', ')}`).toEqual([]);
	});
});
