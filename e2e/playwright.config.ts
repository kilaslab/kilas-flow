import { defineConfig } from '@playwright/test';

// Browser end-to-end suite. It tests the assembled product — the Go binary
// with the real SPA embedded — not the frontend package, which is why this
// project lives at the repository root instead of inside web/.
//
// Isolation: every test boots its own server with its own port and data
// directory (see fixtures.ts), so parallel workers cannot interfere through
// the database or the scheduler. The binary and the SPA are built once per
// suite run by global-setup.ts via `make build-all`.
export default defineConfig({
	testDir: './tests',
	globalSetup: './global-setup.ts',
	// The JSON report is what makes a skipped test visible rather than implied:
	// scripts/skip-budget.mjs reads it after the run and fails when a skip is
	// unexplained or the total grew. `line` stays for the run's own output.
	reporter: [['line'], ['json', { outputFile: 'test-results/report.json' }]],
	// One test boots a server, seeds over the API and runs a workflow: slower
	// than a unit test, faster than a container probe.
	timeout: 120_000,
	expect: {
		// Observable state (execution status, rendered row, SSE event) is
		// awaited with assertions below; nothing here sleeps for a fixed time.
		timeout: 15_000
	},
	fullyParallel: true,
	workers: process.env.CI ? 2 : undefined,
	retries: process.env.CI ? 2 : 0,
	outputDir: './test-results',
	use: {
		trace: 'retain-on-failure',
		screenshot: 'only-on-failure',
		video: 'retain-on-failure'
	},
	projects: [{ name: 'chromium' }]
});
