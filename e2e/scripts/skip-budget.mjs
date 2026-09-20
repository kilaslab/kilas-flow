// Fails a CI run whose Playwright suite skipped more than the budget allows.
//
// The problem this exists for: the e2e suite is environment-gated, and in a
// summarised run "26 skipped" reads exactly like "71 passed". Every proof the
// gates cover — PostgreSQL, the WAHA corpus, a local model, a live n8n — was
// therefore unverified while the job was green.
//
// What it checks, in order of importance:
//   1. Every skipped test names a dependency on the allowlist in
//      e2e/skip-budget.json. A new gate, or a gate whose reason changed, fails
//      the run rather than passing quietly.
//   2. No skipped test names something CI is supposed to provide (the
//      `forbidden` list). A PostgreSQL skip in CI means the service, the DSN or
//      the connection is broken, which is a failure, not a coverage gap.
//   3. The total does not exceed the recorded number. A test that starts
//      skipping without saying so trips this.
//
// Usage: node scripts/skip-budget.mjs [report.json]
import { readFile } from 'node:fs/promises';
import { dirname, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(here, '..', '..');
const budgetPath = resolve(here, '..', 'skip-budget.json');
const reportPath = process.argv[2] ?? resolve(here, '..', 'test-results', 'report.json');

async function readJson(path, what) {
	try {
		return JSON.parse(await readFile(path, 'utf8'));
	} catch (error) {
		console.error(`skip-budget: cannot read the ${what} at ${path}: ${error.message}`);
		process.exit(1);
	}
}

const budget = await readJson(budgetPath, 'budget');
const report = await readJson(reportPath, 'Playwright JSON report');

// walks the report's suite tree, since a describe nests arbitrarily deep.
function collect(suite, file, skipped) {
	const suiteFile = suite.file ?? file;
	for (const spec of suite.specs ?? []) {
		const results = spec.tests ?? [];
		for (const test of results) {
			const wasSkipped = test.status === 'skipped' || (test.results ?? []).some((r) => r.status === 'skipped');
			if (!wasSkipped) continue;
			const reason = (test.annotations ?? [])
				.filter((annotation) => annotation.type === 'skip')
				.map((annotation) => annotation.description ?? '')
				.join(' ')
				.trim();
			skipped.push({ file: relative(repositoryRoot, suiteFile ?? spec.file ?? ''), title: spec.title, reason });
		}
	}
	for (const child of suite.suites ?? []) collect(child, suiteFile, skipped);
}

const skipped = [];
for (const suite of report.suites ?? []) collect(suite, undefined, skipped);

const violations = [];
for (const test of skipped) {
	const forbidden = (budget.forbidden ?? []).find((entry) => test.reason.includes(entry.prefix));
	if (forbidden) {
		violations.push(
			`${test.file} › ${test.title}\n      skipped with a forbidden reason: ${forbidden.prefix}\n      ${forbidden.why}`
		);
		continue;
	}
	if (test.reason === '') {
		violations.push(`${test.file} › ${test.title}\n      skipped with no reason at all`);
		continue;
	}
	if (!(budget.allowedPrefixes ?? []).some((prefix) => test.reason.startsWith(prefix))) {
		violations.push(
			`${test.file} › ${test.title}\n      skipped for a reason that is not on the allowlist: ${test.reason}`
		);
	}
}

if (skipped.length > budget.maxSkipped) {
	violations.push(
		`the run skipped ${skipped.length} tests, past the recorded budget of ${budget.maxSkipped} ` +
			`(recorded ${budget.recorded?.before ?? '?'} before the PostgreSQL service was added to this job)`
	);
}

for (const test of skipped) {
	console.log(`skip-budget: skipped  ${test.file} › ${test.title}`);
	if (test.reason !== '') console.log(`             ${test.reason}`);
}

if (violations.length > 0) {
	console.error(`\nskip-budget: ${violations.length} violation(s)`);
	for (const violation of violations) console.error(`  - ${violation}`);
	console.error(
		`\nIf a skip is legitimate, add its reason prefix to e2e/skip-budget.json ` +
			`(e2e/fixtures/gates.ts holds the prefixes the specs use).`
	);
	process.exit(1);
}

console.log(
	`\nskip-budget: ${skipped.length}/${budget.maxSkipped} skipped, every one of them explained ` +
		`(${budget.allowedPrefixes.join(' | ')})`
);
