// Library-import fixtures (FEAT-wdnc03). New file only: the harness in
// e2e/helpers/* and e2e/fixtures.ts is untouched, and the gg85se (waha-migration)
// and nch9dg (datastore) fixtures are reused by composition — imported, never
// rewritten.
//
// Sample strategy: one workflow per family (webhook-triggered, scheduled,
// data-shaping, flow-control, HTTP integration, AI agent) plus a blocked
// capsule. Each is a pinned export under library-import-exports/*.json,
// authored by hand from the documented n8n shapes (see each file's
// _provenance marker). When N8N_EMAIL/N8N_PASSWORD name a live local n8n, the
// live layer below exports the same families from that instance and the suite
// compares them against the pins; otherwise every live test skips by name with
// the exact export commands, never passing silently.
//
// Secrets: N8N_URL defaults to http://localhost:5678; N8N_EMAIL/N8N_PASSWORD
// are env-only and fail closed. No credential value ever appears in a file.
import { readFile } from 'node:fs/promises';

export interface ImportIssue {
	severity: string;
	nodeName?: string;
	nodeId?: string;
	field?: string;
	type?: string;
	reason: string;
}

export interface ImportedLibraryWorkflow {
	workflow: { id: string };
	unsupported: ImportIssue[];
	webhooks: Array<{ nodeId: string; method: string; path: string; url: string }>;
}

export type GapClass = 'unsupported-node' | 'expression-gap' | 'connection-gap' | 'credential-gap' | 'other';

export type SampleProvenance = 'authored-shape' | 'live-export' | 'corpus-pinned';

export interface LibrarySample {
	/** Pinned export basename under library-import-exports/ (no extension). */
	id: string;
	/** Workflow family this sample stands for. */
	family: string;
	/** Live-export when creds name an n8n; authored-shape otherwise. */
	provenance: Exclude<SampleProvenance, 'corpus-pinned'>;
	/** False for the samples that must stay blocked (ai-agent, capsule). */
	runnable: boolean;
}

export const LIBRARY_SAMPLES: LibrarySample[] = [
	{ id: 'webhook-echo', family: 'webhook-triggered', provenance: 'authored-shape', runnable: true },
	{ id: 'scheduled-tick', family: 'scheduled', provenance: 'authored-shape', runnable: true },
	{ id: 'data-shaping', family: 'data-shaping', provenance: 'authored-shape', runnable: true },
	{ id: 'flow-control', family: 'flow-control', provenance: 'authored-shape', runnable: true },
	{ id: 'http-stub', family: 'http-integration', provenance: 'authored-shape', runnable: true },
	{ id: 'ai-agent', family: 'ai-agent', provenance: 'authored-shape', runnable: false },
	{ id: 'unsupported-node', family: 'blocked-capsule', provenance: 'authored-shape', runnable: false }
];

/** Maps one nbqye0 import diagnostic onto the wdnc03 failure classes. */
export function classifyIssue(issue: ImportIssue): GapClass {
	const reason = (issue.reason ?? '').toLowerCase();
	const field = (issue.field ?? '').toLowerCase();
	if (field === 'credentials' || reason.includes('credential')) return 'credential-gap';
	if (reason.includes('has no equivalent of the n8n node') || reason.includes('unsupported placeholder')) {
		return 'unsupported-node';
	}
	if (reason.includes('connection')) return 'connection-gap';
	if (reason.includes('expression')) return 'expression-gap';
	return 'other';
}

export type GapCounts = Record<GapClass, number>;

export function countGaps(issues: ImportIssue[]): GapCounts {
	const counts: GapCounts = {
		'unsupported-node': 0,
		'expression-gap': 0,
		'connection-gap': 0,
		'credential-gap': 0,
		other: 0
	};
	for (const issue of issues) counts[classifyIssue(issue)] += 1;
	return counts;
}

/** Loads a pinned export, asserting its provenance marker and stripping it. */
export async function loadLibraryExport(id: string): Promise<Record<string, unknown>> {
	const url = new URL(`./library-import-exports/${id}.json`, import.meta.url);
	const parsed = JSON.parse(await readFile(url, 'utf-8')) as Record<string, unknown>;
	const provenance = (parsed._provenance as { origin?: string } | undefined)?.origin;
	if (provenance !== 'authored-shape' && provenance !== 'live-export') {
		throw new Error(
			`library export ${id}.json carries no provenance marker (want "authored-shape" or "live-export"): refusing to import an unmarked fixture`
		);
	}
	const { _provenance: _dropped, ...workflow } = parsed;
	return workflow;
}

// --- Live n8n layer (env-gated, fail-closed) --------------------------------

export interface N8nLiveConfig {
	url: string;
	email: string;
	password: string;
}

/** N8N_URL defaults to the local instance; the login itself is env-only. */
export function n8nLiveConfig(): N8nLiveConfig {
	return {
		url: (process.env.N8N_URL ?? 'http://localhost:5678').replace(/\/+$/, ''),
		email: process.env.N8N_EMAIL ?? '',
		password: process.env.N8N_PASSWORD ?? ''
	};
}

/** Null when the live layer can run; otherwise the exact skip/export message. */
export function liveSkipReason(): string | null {
	const config = n8nLiveConfig();
	if (config.email === '' || config.password === '') {
		return (
			`live n8n export skipped: N8N_EMAIL/N8N_PASSWORD are unset. ` +
			`To run: export N8N_EMAIL='<operator login>' N8N_PASSWORD='<operator password>' ` +
			`[N8N_URL='${config.url}']; N8N_URL defaults to http://localhost:5678. ` +
			`Secrets stay in env, never in files.`
		);
	}
	return null;
}

async function liveLogin(config: N8nLiveConfig): Promise<string> {
	const response = await fetch(`${config.url}/rest/login`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify({ email: config.email, password: config.password })
	});
	if (response.status !== 200) {
		throw new Error(
			`live n8n signin failed: POST ${config.url}/rest/login answered ${response.status} (body: ${await response.text()}); ` +
				`check N8N_URL/N8N_EMAIL/N8N_PASSWORD and retry`
		);
	}
	const cookies = response.headers.getSetCookie?.() ?? [];
	const session = cookies.map((cookie) => cookie.split(';')[0]).join('; ');
	if (session === '') throw new Error('live n8n signin answered 200 with no session cookie; refusing to proceed unauthenticated');
	return session;
}

/** Exports one workflow by name from the live instance (creds-gated callers only). */
export async function exportLiveWorkflow(name: string): Promise<Record<string, unknown>> {
	const config = n8nLiveConfig();
	const session = await liveLogin(config);
	const listed = await fetch(`${config.url}/rest/workflows`, { headers: { cookie: session } });
	if (listed.status !== 200) {
		throw new Error(`live n8n workflow list answered ${listed.status} (body: ${await listed.text()})`);
	}
	const workflows = ((await listed.json()) as { data?: Array<{ id?: string; name?: string }> }).data ?? [];
	const match = workflows.find((workflow) => workflow.name === name);
	if (!match?.id) {
		throw new Error(
			`live n8n has no workflow named ${JSON.stringify(name)} (offers: ${workflows.map((workflow) => workflow.name).join(', ') || '(none)'}); ` +
				`create it from the library sample first`
		);
	}
	const exported = await fetch(`${config.url}/rest/workflows/${encodeURIComponent(match.id)}`, {
		headers: { cookie: session }
	});
	if (exported.status !== 200) {
		throw new Error(`live n8n export of ${JSON.stringify(name)} answered ${exported.status}`);
	}
	return (await exported.json()) as Record<string, unknown>;
}

/** n8n node types in a workflow document, for live-vs-pin shape comparison. */
export function n8nNodeTypes(workflow: Record<string, unknown>): string[] {
	const nodes = (workflow.nodes as Array<{ type?: string }> | undefined) ?? [];
	return nodes.map((node) => node.type ?? '(missing)').sort();
}
