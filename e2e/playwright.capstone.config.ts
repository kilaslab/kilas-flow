import { randomBytes } from 'node:crypto';

import { defineConfig } from '@playwright/test';

// The epic acceptance capstone (FEAT-5fhj6p): the four proofs of EPIC-m42s3g
// against a docker IMAGE, the real Telegram Bot API, a real WAHA server and the
// npm registry. It is deliberately a separate config from playwright.config.ts,
// which owns the per-PR hermetic suite: this one needs docker, credentials and
// third-party availability, so it must never gate a merge and must never enter
// the per-PR skip budget. `make test-e2e` cannot pick it up because the default
// config's testDir is './tests' and this suite lives in './capstone'.
//
// One run id is minted here and exported into the environment, because the
// run's containers and network are labelled with it and globalTeardown runs in
// the same process — deriving it separately in the workers would leave the
// teardown looking for a run that never existed.
process.env.KILASFLOW_CAPSTONE_RUN_ID ??= `r${randomBytes(4).toString('hex')}`;

export default defineConfig({
	testDir: './capstone',
	// No globalSetup: nothing here builds bin/kilasflow. The artefact under
	// test is the image, and the first test pulls it and proves its identity.
	globalTeardown: './capstone/global-teardown.ts',
	// The JSON report is cross-checked by scripts/capstone-report.mjs: a test
	// that crashed before writing its record shows up there.
	reporter: [['line'], ['json', { outputFile: 'capstone-results/playwright.json' }]],
	// One container proof at a time: a run must be attributable, and the WAHA
	// and Telegram sides are single-session resources in stage 3.
	fullyParallel: false,
	workers: 1,
	// The verdict is the report's (exit 0/1/2 through exitCodeFor), not this
	// runner's, so a flake must not be retried into a different story.
	retries: 0,
	timeout: 15 * 60_000,
	expect: { timeout: 30_000 },
	outputDir: './capstone-results/artifacts',
	use: {
		// Traces record the browser only, which never sees a bot token; the
		// server-side secrets stay in the records, where redact() strips them.
		trace: 'retain-on-failure',
		screenshot: 'only-on-failure',
		video: 'retain-on-failure'
	},
	projects: [{ name: 'chromium' }]
});
