import { expect } from '@playwright/test';
import { gate } from './gates';

// Live-n8n comparison fixture for FEAT-1jqjtd.
//
// What this file owns: the secret contract, the executable-node matrix with
// its tiers, the documented KilasFlow-vs-n8n divergences, and the pure output
// comparison the live tests use. What it does not own: product code (nothing
// outside e2e/), committed secrets (there are none — see below), or the
// harness (fixtures.ts / helpers/ stay untouched).
//
// Secrets: the reference n8n instance is addressed through N8N_URL (default
// http://localhost:5678) and signed into with N8N_EMAIL / N8N_PASSWORD, all
// read from the environment and nowhere else. When the credentials are
// absent — the state of every machine that has not been given them — every
// live test skips by name with the reason below instead of failing, and the
// suite stays green with the skip count reported. To run the live layer:
//
//   export N8N_URL="http://localhost:5678"
//   export N8N_EMAIL="operator@example.com"
//   export N8N_PASSWORD="the-operator-password"
//
// The guard `allow_private_networks` is never set anywhere in this suite;
// outbound HTTP from KilasFlow reaches only the loopback stub admitted through
// the instance's outbound policy (see helpers/server.ts).

// Address of the reference n8n instance. A loopback default, never a secret.
export const N8N_URL = process.env.N8N_URL ?? 'http://localhost:5678';

export interface N8nLiveConfig {
	url: string;
	email: string;
	password: string;
}

// Reads the live credentials from the environment only. Returns null when
// they are absent so callers skip instead of failing: an unprovisioned
// machine is not a broken suite.
export function n8nLiveConfig(): N8nLiveConfig | null {
	const email = process.env.N8N_EMAIL;
	const password = process.env.N8N_PASSWORD;
	if (!email || !password) return null;
	return { url: N8N_URL, email, password };
}

export function isN8nLiveConfigured(): boolean {
	return n8nLiveConfig() !== null;
}

// The single skip reason every live test reports. It names the missing
// secret set and the exact exports that provision it, so a skipped run tells
// the operator how to un-skip it.
export const N8N_LIVE_SKIP_REASON =
	gate(
		'n8nLive',
		'N8N_EMAIL/N8N_PASSWORD are not set ' +
	`(N8N_URL is ${N8N_URL}); provision with ` +
			'`export N8N_URL="http://localhost:5678" N8N_EMAIL="operator@example.com" N8N_PASSWORD="the-operator-password"` ' +
			'and rerun to execute this case against the reference instance'
	);

export function n8nLiveSkipReason(node: string): string {
	return `${node}: ${N8N_LIVE_SKIP_REASON}`;
}

// The executable-node matrix, driven from the live catalogue response in the
// ratchet test (which fails on any catalogue entry with no tier here).
// Tiers, stated so a green result is not read as more than it is:
//   stub-executed    — run to terminal success against the loopback stub
//                      through the instance's outbound policy.
//   editor-validated — saved as a draft, then the run is refused with the
//                      named user-facing validation message (import capsules
//                      must refuse; third-party nodes name their missing
//                      credential/service). The editor half is proven by the
//                      picker tests in n8n-compare.spec.ts.
//   live-service     — executed against the real third-party service. Empty
//                      in this suite: the only live service this ticket
//                      compares against is the reference n8n itself, and that
//                      layer is env-gated per case below.
export const STUB_EXECUTED = [
	'kilasflow.manual@1',
	'kilasflow.set@1',
	'kilasflow.if@1',
	'kilasflow.merge@1',
	'kilasflow.switch@1',
	'kilasflow.filter@1',
	'kilasflow.limit@1',
	'kilasflow.noOp@1',
	'kilasflow.httpRequest@1',
	'kilasflow.webhook@1',
	'kilasflow.respondToWebhook@1',
	'kilasflow.schedule@1',
	'kilasflow.sqlite@1',
	'kilasflow.code@1',
	'kilasflow.calculator@1',
	'kilasflow.stickyNote@1',
	'kilasflow.loop@1',
	'kilasflow.telegramTrigger@1',
	'kilasflow.aggregate@1',
	'kilasflow.splitOut@1',
	'kilasflow.sort@1',
	'kilasflow.summarize@1',
	'kilasflow.removeDuplicates@1',
	'kilasflow.dateTime@1',
	'kilasflow.wait@1',
	'kilasflow.executeWorkflow@1',
	'kilasflow.executeWorkflowTrigger@1',
	'kilasflow.datastore@1',
	// The error pair and the hosted form are exercised end-to-end by
	// fixtures/error-form-nodes.ts, which n8n-compare.spec.ts calls below.
	'kilasflow.errorTrigger@1',
	'kilasflow.stopAndError@1',
	'kilasflow.formTrigger@1',
	'pack.telegram@1',
	'pack.waha@202409',
	'pack.waha@202502',
	'pack.wahaTrigger@202409',
	'pack.wahaTrigger@202502'
];

export const EDITOR_VALIDATED = [
	'kilasflow.foreignCode@1',
	'kilasflow.unsupported@1',
	'kilasflow.unsupported@2',
	'kilasflow.unsupported@4',
	'kilasflow.unsupported@8',
	'kilasflow.agent@1',
	'kilasflow.chainLlm@1',
	'kilasflow.chatModel@1',
	'kilasflow.lmChatOpenAi@1',
	'kilasflow.lmChatOpenRouter@1',
	'kilasflow.memoryBuffer@1',
	'kilasflow.httpTool@1',
	'kilasflow.calculatorTool@1',
	'kilasflow.workflowTool@1',
	'kilasflow.outputParser@1',
	'kilasflow.mcpClientTool@1',
	'kilasflow.embeddings@1',
	'kilasflow.vectorStore@1',
	'kilasflow.datastoreTool@1',
	'kilasflow.postgres@1',
	'kilasflow.postgres@2',
	'kilasflow.mysql@1',
	'kilasflow.mysql@2'
];

// Executed against the real third-party service: nothing in this suite.
// A node lands here only with the service, the test, and the reason named.
export const LIVE_SERVICE: string[] = [];

// Explicit, justified exclusions from every tier. Empty: everything the
// server advertises is either executed, validated, or live-served above.
export const MATRIX_EXCLUDED: Record<string, string> = {
	// No exclusions. When a node needs one, add `"type@version": "why"`.
};

// One live comparison case per node family with an n8n counterpart. The
// `operation` is the same logical operation both engines run; outputs are
// compared with compareN8nOutput, modulo DOCUMENTED_DIVERGENCES.
export interface N8nComparisonCase {
	// Stable name used for the per-node skip title.
	name: string;
	kilasflow: string;
	n8nType: string;
	operation: string;
}

export const N8N_COMPARISONS: N8nComparisonCase[] = [
	{
		name: 'set assigns literal fields',
		kilasflow: 'kilasflow.set@1',
		n8nType: 'n8n-nodes-base.set',
		operation: 'assign {compared: "n8n-parity"} on one item and read it back'
	},
	{
		name: 'if routes on a field predicate',
		kilasflow: 'kilasflow.if@1',
		n8nType: 'n8n-nodes-base.if',
		operation: 'route {compared: "n8n-parity"} to the true branch, the rest to false'
	},
	{
		name: 'http posts to the stub',
		kilasflow: 'kilasflow.httpRequest@1',
		n8nType: 'n8n-nodes-base.httpRequest',
		operation: 'POST {compared: "n8n-parity"} at the loopback stub and read the echoed body'
	},
	{
		name: 'webhook delivers an inbound POST',
		kilasflow: 'kilasflow.webhook@1',
		n8nType: 'n8n-nodes-base.webhook',
		operation: 'deliver {compared: "n8n-parity"} by POST and run the downstream node'
	}
];

export function comparisonCaseNames(): string[] {
	return N8N_COMPARISONS.map((entry) => `${entry.kilasflow} — ${entry.name}`);
}

// Behavioural divergences that are expected, not drift. The comparison
// asserts around these and fails on anything else.
export interface DocumentedDivergence {
	kilasflow: string;
	n8n: string;
	difference: string;
}

export const DOCUMENTED_DIVERGENCES: DocumentedDivergence[] = [
	{
		kilasflow: 'kilasflow.httpRequest@1',
		n8n: 'n8n-nodes-base.httpRequest',
		difference:
			'response envelope: KilasFlow records {statusCode, body} while n8n returns the body at the top level; only the body is compared'
	},
	{
		kilasflow: 'kilasflow.code@1',
		n8n: 'n8n-nodes-base.code',
		difference:
			'runtime language: KilasFlow runs Go snippets, n8n runs JavaScript; the same logical op is expressed per engine and only the resulting data is compared'
	},
	{
		kilasflow: 'kilasflow.set@1',
		n8n: 'n8n-nodes-base.set',
		difference:
			'assignment metadata: n8n Set versions carry per-assignment ids and type hints KilasFlow does not store; only field names and values are compared'
	},
	{
		kilasflow: 'kilasflow.dateTime@1',
		n8n: 'n8n-nodes-base.dateTime',
		difference:
			'formatting: default string rendering and week numbering may differ across engines; instants are compared as epoch milliseconds, never as strings'
	}
];

// Recursively sorts object keys so comparison is order-insensitive. Arrays
// keep their order: item streams are ordered and a reorder is real drift.
function canonicalize(value: unknown): unknown {
	if (Array.isArray(value)) return value.map(canonicalize);
	if (value !== null && typeof value === 'object') {
		const entries = Object.entries(value as Record<string, unknown>)
			.map(([key, entry]) => [key, canonicalize(entry)] as const)
			.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
		return Object.fromEntries(entries);
	}
	return value;
}

// KilasFlow wraps HTTP answers in a {statusCode, body} envelope (see the
// first DOCUMENTED_DIVERGENCES entry); n8n surfaces the body. Unwrap both
// sides to the body before comparing an http case.
function httpComparable(value: unknown): unknown {
	if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
		const record = value as Record<string, unknown>;
		if ('body' in record && 'statusCode' in record) return record.body;
		if ('body' in record && Object.keys(record).length === 1) return record.body;
	}
	return value;
}

// Compares one KilasFlow execution-record output against the n8n counterpart
// for the same logical operation. Returns the list of differences; empty
// means the engines agree modulo the documented divergences above.
export function compareN8nOutput(kind: string, kilasflow: unknown, n8n: unknown): string[] {
	const left = kind === 'http' ? httpComparable(kilasflow) : kilasflow;
	const right = kind === 'http' ? httpComparable(n8n) : n8n;
	const a = JSON.stringify(canonicalize(left));
	const b = JSON.stringify(canonicalize(right));
	if (a === b) return [];
	return [`outputs differ for ${kind}: kilasflow=${a} n8n=${b}`];
}

// Asserts the gate itself: without credentials the live layer must skip with
// the named reason (proven by the skipped cases in the report); with
// credentials the config carries the loopback default unless N8N_URL says
// otherwise. Called by the always-running gate test.
export function expectLiveGateState(configured: boolean): void {
	if (!configured) {
		expect(N8N_LIVE_SKIP_REASON).toContain('N8N_EMAIL/N8N_PASSWORD');
		expect(N8N_LIVE_SKIP_REASON).toContain('export N8N_URL=');
		return;
	}
	const config = n8nLiveConfig();
	expect(config, 'live config is present once the gate passes').not.toBeNull();
	expect(config!.url.length).toBeGreaterThan(0);
}
