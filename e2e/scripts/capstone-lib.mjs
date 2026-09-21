// Capstone machinery (FEAT-5fhj6p): pure process-table logic, no dependencies.
//
// One implementation, two callers: the TypeScript epic proofs import it
// (e2e/fixtures/epic-proofs.ts -> EpicHost.assertNoNode) and the on-demand
// capstone's report CLI will import it too, so a Node.js process hiding beside
// a server cannot be judged differently by the two suites.
//
// The epic's constraint is "no Node.js process anywhere in or beside the
// server", and the previous check failed to hold that line: it matched one
// exact `comm` string (`comm === 'node'`) and looped over DIRECT children
// only, although its comment claimed a subtree check. A `sh -c node ...`
// sidecar — Node running as a grandchild — slipped through every proof.
//
// What this module gets right instead:
// - `comm` is platform- and launch-dependent: on macOS `ps -o comm` prints the
//   basename, elsewhere it can be a full path or a truncated name. Names are
//   therefore compared as WHOLE basenames, never as substrings (`node` matches,
//   `node_exporter` must not), and a full path is reduced to its basename.
// - The process table is walked as a FULL subtree with the roots included, so a
//   process started through a shell wrapper is still found.
// - `args` is consulted when the harness has it (`env node` runs Node as the
//   second token), which keeps the check honest on platforms whose `comm` is a
//   wrapper's name.
//
// JSDoc-only types: this file is plain ESM JavaScript so the report CLI can run
// it without a build step.

/** Executable basenames that run JavaScript: Node proper plus the package
 * managers and runtimes that execute JS on the server side. */
export const NODE_LIKE = new Set(['node', 'nodejs', 'bun', 'deno', 'npm', 'npx', 'pnpm', 'yarn', 'tsx', 'ts-node']);

/**
 * The last path segment of an executable name, so `/usr/local/bin/node` and
 * `node` compare equal. Windows-style `node.exe` is reduced to `node`.
 * @param {string | undefined} token
 * @returns {string}
 */
export function baseName(token) {
	if (typeof token !== 'string') return '';
	const trimmed = token.trim();
	if (trimmed === '') return '';
	const segments = trimmed.split(/[\\/]/);
	const last = segments[segments.length - 1] ?? '';
	return last.replace(/\.exe$/i, '');
}

/**
 * @typedef {object} ProcessRow
 * @property {number} pid
 * @property {number} ppid
 * @property {string} comm
 * @property {string[]} [args]
 */

/**
 * Is this a Node-like process? The comparison is over whole basenames of
 * `comm` and of every argv token the caller supplies.
 *
 * The whole token list matters, not just the first two, because `comm` is not
 * reliably the program: in a container on Docker Desktop a `node` process
 * reports `MainThread` — the name of its main thread — and the program appears
 * later in the argv (`{MainThread} node …`). Matching only argv[0] missed it.
 * A token is compared as a whole basename too, so `sh -c "node server.js"`
 * (one token containing a space, i.e. a command string) is not evidence while
 * `env node` is.
 * @param {{ comm?: string, args?: string[] | string }} proc
 * @returns {boolean}
 */
export function isNodeLike(proc) {
	if (!proc || typeof proc !== 'object') return false;
	const comm = baseName(proc.comm);
	const args = typeof proc.args === 'string' ? proc.args.trim().split(/\s+/) : Array.isArray(proc.args) ? proc.args : [];
	if (NODE_LIKE.has(comm)) return true;
	return args.some((token) => NODE_LIKE.has(baseName(token)));
}

/**
 * Parses `ps -eo pid,ppid,comm` output into rows. The header line and any blank
 * line are skipped; `comm` may contain spaces, so everything after the second
 * column belongs to it.
 * @param {string} text
 * @returns {ProcessRow[]}
 */
export function parsePsTable(text) {
	const rows = [];
	for (const line of String(text ?? '').split('\n')) {
		const parts = line.trim().split(/\s+/);
		if (parts.length < 3) continue;
		const pid = Number(parts[0]);
		const ppid = Number(parts[1]);
		if (!Number.isInteger(pid) || !Number.isInteger(ppid)) continue;
		rows.push({ pid, ppid, comm: parts.slice(2).join(' ') });
	}
	return rows;
}

/**
 * The full process subtree rooted at `rootPids`, roots included. Walking the
 * whole subtree (not just direct children) is the point: a wrapper shell makes
 * Node a grandchild.
 * @param {ProcessRow[]} rows
 * @param {number[]} rootPids
 * @returns {ProcessRow[]}
 */
export function descendants(rows, rootPids) {
	const wanted = new Set((rootPids ?? []).filter((pid) => Number.isInteger(pid) && pid > 0));
	const found = new Map();
	for (const row of rows ?? []) {
		if (wanted.has(row.pid)) found.set(row.pid, row);
	}
	for (;;) {
		let grew = false;
		for (const row of rows ?? []) {
			if (found.has(row.pid)) continue;
			if (found.has(row.ppid)) {
				found.set(row.pid, row);
				grew = true;
			}
		}
		if (!grew) break;
	}
	return [...found.values()];
}

/**
 * @typedef {object} NoNodeVerdict
 * @property {boolean} checked        false when the server pid could not be resolved
 * @property {string} reason          why `checked` is false ('' when checked)
 * @property {string} scope           what the roots describe, e.g. 'tcp:1234'
 * @property {string} serverComm      the root process's comm as `ps` printed it
 * @property {ProcessRow[]} processes the server's full subtree, roots included
 * @property {ProcessRow[]} nodeProcesses Node-like processes found in that subtree
 */

/**
 * Judges one process table against the no-Node rule.
 *
 * An unresolved root is reported as `checked: false`, never as a clean verdict:
 * every caller asserts on `checked` precisely so an unreadable process table
 * cannot read as "no Node found".
 * @param {{ rows: ProcessRow[], rootPids: number[], scope: string }} input
 * @returns {NoNodeVerdict}
 */
export function noNodeVerdict({ rows, rootPids, scope }) {
	const roots = (rootPids ?? []).filter((pid) => Number.isInteger(pid) && pid > 0);
	if (roots.length === 0) {
		return {
			checked: false,
			reason: `no server pid resolved in ${scope}`,
			scope,
			serverComm: '',
			processes: [],
			nodeProcesses: []
		};
	}
	const processes = descendants(rows ?? [], roots);
	const root = (rows ?? []).find((row) => row.pid === roots[0]);
	return {
		checked: true,
		reason: '',
		scope,
		serverComm: root?.comm ?? '',
		processes,
		nodeProcesses: processes.filter((row) => isNodeLike({ comm: row.comm, args: row.args }))
	};
}

// --- Capstone core (stage 2) ------------------------------------------------
//
// The image host and the report CLI share these: one implementation of the
// container verdict, the failure classification, the redaction and the report
// body, so the two suites cannot disagree about what a run observed.

/**
 * Parses the two `docker top` tables the image host collects per container.
 *
 * Two calls are needed because one `-eo` list cannot hold both: `comm` may
 * contain spaces, so it has to be the last column of its own table, while the
 * full argv lives only in the `args` table.  Both tables print a header and
 * rows; `args` is joined back by pid.
 * @param {string} commText `docker top <c> -eo pid,ppid,comm`
 * @param {string} argsText `docker top <c> -eo pid,args`
 * @returns {ProcessRow[]} rows with `args` filled where the pid appeared
 */
export function parseDockerTop(commText, argsText) {
	const rows = [];
	for (const line of String(commText ?? '').split('\n')) {
		const parts = line.trim().split(/\s+/);
		if (parts.length < 3) continue;
		const pid = Number(parts[0]);
		const ppid = Number(parts[1]);
		if (!Number.isInteger(pid) || !Number.isInteger(ppid)) continue;
		rows.push({ pid, ppid, comm: parts.slice(2).join(' ') });
	}
	const argsByPid = new Map();
	for (const line of String(argsText ?? '').split('\n')) {
		const match = line.trim().match(/^(\d+)\s+(.*)$/);
		if (!match) continue;
		argsByPid.set(Number(match[1]), match[2].trim());
	}
	for (const row of rows) {
		const args = argsByPid.get(row.pid);
		if (args !== undefined) row.args = args;
	}
	return rows;
}

/**
 * @typedef {object} ContainerStackVerdict
 * @property {boolean} ok       true when every container in the stack is clean
 * @property {string} reason    '' when ok, otherwise the first defect, naming container and pid
 * @property {Array<{container: string, pid: number, ppid: number, comm: string}>} nodeProcesses
 * @property {Array<{container: string, pid: number, comm: string}>} roots
 */

/**
 * Judges a whole labelled stack of containers against the epic's no-Node rule.
 *
 * "In or beside the server" is a stack question in image mode: the server, a
 * second tenant and the datastore each run in a container, and a Node sidecar
 * would be a fourth.  Only containers the run labelled count, so an unrelated
 * Node container elsewhere on the host is never blamed.
 *
 * Two defects are reported.  A Node-like process inside any labelled container
 * fails.  Additionally, every container declared to be KilasFlow must have the
 * server binary as its lowest pid: a container whose PID 1 is `node` is not the
 * image under test, whatever else is running in it.
 * @param {Array<{name: string, rows: ProcessRow[], isKilasFlow?: boolean}>} containers
 * @returns {ContainerStackVerdict}
 */
export function containerStackVerdict(containers) {
	const stack = Array.isArray(containers) ? containers : [];
	const nodeProcesses = [];
	const roots = [];
	const failures = [];
	for (const container of stack) {
		const name = container?.name ?? '<unnamed>';
		const rows = Array.isArray(container?.rows) ? container.rows : [];
		if (rows.length === 0) {
			failures.push(`${name}: docker top reported no processes`);
			continue;
		}
		const root = rows.reduce((lowest, row) => (row.pid < lowest.pid ? row : lowest), rows[0]);
		roots.push({ container: name, pid: root.pid, comm: root.comm });
		if (container?.isKilasFlow === true && !baseName(root.comm).includes('kilasflow')) {
			failures.push(`${name}: lowest pid ${root.pid} runs "${root.comm}", not the kilasflow binary`);
		}
		for (const row of rows) {
			if (!isNodeLike({ comm: row.comm, args: row.args })) continue;
			nodeProcesses.push({ container: name, pid: row.pid, ppid: row.ppid, comm: row.comm });
			failures.push(`${name}: Node-like process at pid ${row.pid} (${row.comm})`);
		}
	}
	return {
		ok: failures.length === 0,
		reason: failures[0] ?? '',
		nodeProcesses,
		roots
	};
}

/** The causes an `unavailable` record may name. */
export const UNAVAILABLE_CAUSES = Object.freeze([
	'network',
	'timeout',
	'http-5xx',
	'rate-limited',
	'credential-rejected',
	'tunnel',
	'registry',
	'reprobe'
]);

/**
 * The transport-cause reading of an error message, with no health context.
 *
 * Deliberately narrow: an HTTP 401/403 from a service we reached is NOT here,
 * because a refused delivery is evidence about our request, not about the
 * service being down.  Only causes a re-probe can also confirm live here.
 * @param {string} message
 * @returns {string | null}
 */
function transportCause(message) {
	const text = String(message ?? '');
	if (/ECONNRESET|ECONNREFUSED|ENETUNREACH|EHOSTUNREACH|EAI_AGAIN|ENOTFOUND|socket hang up|connection reset|network is unreachable|fetch failed/i.test(text)) {
		return 'network';
	}
	if (/ETIMEDOUT|ESOCKETTIMEDOUT|timed? ?out|deadline exceeded/i.test(text)) return 'timeout';
	if (/\b50[0-4]\b|status 5\d\d|HTTP 5\d\d/i.test(text)) return 'http-5xx';
	if (/rate.?limit|too many requests|\b429\b/i.test(text)) return 'rate-limited';
	if (/cloudflared|tunnel/i.test(text)) return 'tunnel';
	return null;
}

/**
 * Decides whether a thrown error is a regression (`failed`) or an outage
 * (`unavailable`).
 *
 * The re-probe is the authority.  A third party that flaps mid-run produces an
 * error indistinguishable from a broken assertion, so a health probe run right
 * after the failure decides: unhealthy reclassifies as `unavailable` (cause
 * `reprobe`, `reclassified: true`, the original message preserved), healthy
 * leaves it `failed`.  With no re-probe supplied, only a transport cause —
 * which no assertion of ours produces — counts as `unavailable`.
 * @param {{ error: unknown, reprobe?: { healthy?: boolean, cause?: string, detail?: string } }} input
 * @returns {{ outcome: 'failed' | 'unavailable', cause?: string, reason: string, reclassified?: boolean, evidence: { original: string, reprobe?: string } }}
 */
export function classifyFailure({ error, reprobe } = {}) {
	const message = error instanceof Error ? error.message : String(error ?? 'unknown error');
	if (reprobe) {
		if (reprobe.healthy === false) {
			return {
				outcome: 'unavailable',
				cause: reprobe.cause ?? 'reprobe',
				reason: message,
				reclassified: true,
				evidence: { original: message, ...(reprobe.detail ? { reprobe: reprobe.detail } : {}) }
			};
		}
		return { outcome: 'failed', reason: message, reclassified: false, evidence: { original: message } };
	}
	const cause = transportCause(message);
	if (cause) return { outcome: 'unavailable', cause, reason: message, evidence: { original: message } };
	return { outcome: 'failed', reason: message, evidence: { original: message } };
}

/**
 * Classifies a `docker pull` failure.
 *
 * `manifest unknown`, `not found` and `denied` all mean the artefact is not
 * there (or not ours): the run's premise failed, so it is `failed` and exits 1.
 * A registry or network outage is `unavailable` and exits 2.
 * @param {string} text
 * @returns {{ outcome: 'failed' | 'unavailable', cause: string, reason: string }}
 */
export function classifyPullError(text) {
	const message = String(text ?? '');
	if (/manifest unknown|no such manifest|not found|denied|unauthorized|repository does not exist|name unknown/i.test(message)) {
		return { outcome: 'failed', cause: 'artefact-missing', reason: message };
	}
	const cause = transportCause(message) ?? 'registry';
	return { outcome: 'unavailable', cause, reason: message };
}

/**
 * Classifies an npm install failure.  E404/ETARGET mean the version is not
 * published; the network codes are outages.
 * @param {string} text
 * @returns {{ outcome: 'failed' | 'unavailable', cause: string, reason: string }}
 */
export function classifyNpmError(text) {
	const message = String(text ?? '');
	if (/E404|ETARGET|notarget|No matching version|404 Not Found/i.test(message)) {
		return { outcome: 'failed', cause: 'artefact-missing', reason: message };
	}
	if (/ETIMEDOUT|ESOCKETTIMEDOUT/i.test(message)) return { outcome: 'unavailable', cause: 'timeout', reason: message };
	if (/ECONNRESET|EAI_AGAIN|ECONNREFUSED|ENOTFOUND|socket hang up/i.test(message)) {
		return { outcome: 'unavailable', cause: 'network', reason: message };
	}
	if (/E5\d\d|503|502|Service Unavailable|Bad Gateway/i.test(message)) {
		return { outcome: 'unavailable', cause: 'http-5xx', reason: message };
	}
	return { outcome: 'failed', cause: 'unknown', reason: message };
}

/**
 * Removes anything a report must never carry: credentials the suite holds, the
 * bearer tokens a request echoed back, and the webhook route (a capability in
 * its own right — anyone who reads it can post to that workflow).
 *
 * The webhook rewrite keeps the shape (`/webhook/<route>`) so a reader can
 * still see what was called, and the secret list is applied longest-first so a
 * token that contains another cannot leak a suffix.
 * @param {string} text
 * @param {Iterable<string>} [secrets]
 * @returns {string}
 */
export function redact(text, secrets = []) {
	let safe = String(text ?? '');
	const list = [...(secrets ?? [])].filter((secret) => typeof secret === 'string' && secret.length > 0);
	list.sort((a, b) => b.length - a.length);
	for (const secret of list) safe = safe.split(secret).join('[redacted]');
	safe = safe.replace(/\/webhook\/[0-9a-f]{32}\b/gi, '/webhook/<route>');
	safe = safe.replace(/\bBearer\s+[A-Za-z0-9._~+/=-]+/gi, 'Bearer [redacted]');
	return safe;
}

/**
 * The process exit code for a set of records.
 *
 * 0 when every record passed or skipped; 1 when any failed OR an expected
 * record is missing (a test that crashed before writing one is a failure, not
 * silence); 2 when nothing failed and at least one proof was unavailable.
 * @param {Array<{id: string, outcome: string}>} records
 * @param {string[]} [expectedIds]
 * @returns {0 | 1 | 2}
 */
export function exitCodeFor(records, expectedIds = []) {
	const list = Array.isArray(records) ? records : [];
	const byId = new Set(list.map((record) => record?.id));
	for (const id of expectedIds) {
		if (!byId.has(id)) return 1;
	}
	if (list.some((record) => record?.outcome === 'failed' || record?.outcome === undefined)) return 1;
	if (list.some((record) => record?.outcome === 'unavailable')) return 2;
	return 0;
}

/**
 * Renders the report a human reads: the verdict, the artefacts under test, then
 * one row per proof carrying its reason, recovery command and gaps, then the
 * corpus and no-Node summaries.
 * @param {Record<string, any>} report
 * @returns {string}
 */
export function renderMarkdown(report) {
	const lines = [];
	const proofs = Array.isArray(report?.proofs) ? report.proofs : [];
	lines.push('# KilasFlow epic capstone');
	lines.push('');
	lines.push(`Run \`${report?.runId ?? 'unknown'}\` at ${report?.generatedAt ?? 'unknown'}.`);
	lines.push('');
	lines.push('| proof | outcome | detail |');
	lines.push('| --- | --- | --- |');
	for (const proof of proofs) {
		const detail = proof.reason ? String(proof.reason).replace(/\|/g, '\\|').replace(/\n/g, ' ') : '';
		lines.push(`| ${proof.title ?? proof.id} | ${proof.outcome} | ${detail} |`);
	}
	lines.push('');
	if (proofs.some((proof) => proof.recovery)) {
		lines.push('## How to run what was skipped');
		lines.push('');
		for (const proof of proofs) {
			if (!proof.recovery) continue;
			lines.push(`- **${proof.title ?? proof.id}** — ${proof.reason ?? 'not configured'}`);
			// Indented rather than backticked: a recovery command may itself
			// contain backticks (the make target does).
			lines.push(`  \`\`\`sh`);
			lines.push(`  ${proof.recovery}`);
			lines.push(`  \`\`\``);
		}
		lines.push('');
	}
	const artefacts = report?.artefacts ?? {};
	const image = artefacts.image ?? {};
	const sdk = artefacts.sdk ?? {};
	lines.push('## Artefacts under test');
	lines.push('');
	lines.push(`- image \`${image.ref ?? 'unknown'}\`${image.id ? ` (${image.id})` : ''}`);
	if (image.version) lines.push(`- image version \`${image.version}\`${image.revision ? `, revision \`${image.revision}\`` : ''}`);
	if (image.arch) lines.push(`- architecture \`${image.arch}\``);
	if (image.entrypoint) lines.push(`- entrypoint \`${JSON.stringify(image.entrypoint)}\``);
	lines.push(`- health version \`${artefacts.health?.version ?? 'unknown'}\``);
	lines.push(
		`- sdk ${sdk.source ?? 'unknown'} \`${sdk.spec ?? ''}\` resolved \`${sdk.resolvedVersion ?? 'unknown'}\`` +
			`${sdk.integrity ? ` integrity \`${sdk.integrity}\`` : ''}`
	);
	lines.push('');
	const noNode = report?.noNode ?? {};
	const noNodeScopes = Array.isArray(noNode.scopes) ? noNode.scopes : [];
	lines.push('## No Node.js process in or beside the server');
	lines.push('');
	lines.push(
		`${noNode.checked ? 'checked' : 'NOT CHECKED'}: ` +
			`${noNode.nodeProcesses?.length ? `${noNode.nodeProcesses.length} Node-like process(es)` : 'no Node-like process'} ` +
			`across ${noNode.checks ?? 0} process table(s)${noNodeScopes.length > 0 ? ` (${noNodeScopes.join(', ')})` : ''}` +
			`${noNode.reason ? ` — ${noNode.reason}` : ''}`
	);
	lines.push('');
	if (report?.corpus) {
		const corpus = report.corpus;
		lines.push('## Corpus fidelity');
		lines.push('');
		lines.push(
			`coverage ${corpus.coverage?.measured ?? 0}/${corpus.coverage?.baselineTotal ?? 0}` +
				`${corpus.partial ? ' (partial)' : ''}, tiers ` +
				`imported ${corpus.tiers?.imported ?? 0}, activatable ${corpus.tiers?.activatable ?? 0}, ` +
				`runnable ${corpus.tiers?.runnable ?? 0}, blocked ${corpus.tiers?.blocked ?? 0}`
		);
		if (Array.isArray(corpus.drift) && corpus.drift.length > 0) {
			lines.push('');
			lines.push('| fixture | was | now |');
			lines.push('| --- | --- | --- |');
			for (const row of corpus.drift) lines.push(`| ${row.name} | ${row.was} | ${row.now} |`);
		}
		if (Array.isArray(corpus.approximate) && corpus.approximate.length > 0) {
			lines.push('');
			lines.push(`Approximate (not tuned to match the baseline): ${corpus.approximate.map((row) => `${row.name} (${row.reason})`).join(', ')}`);
		}
		lines.push('');
	}
	for (const proof of proofs) {
		if (!Array.isArray(proof.gaps) || proof.gaps.length === 0) continue;
		lines.push(`## Gaps — ${proof.title ?? proof.id}`);
		lines.push('');
		for (const gap of proof.gaps) lines.push(`- ${gap}`);
		lines.push('');
	}
	lines.push(`**Verdict: ${report?.verdict?.outcome ?? 'unknown'}** (exit ${report?.verdict?.exitCode ?? '?'})`);
	return `${lines.join('\n')}\n`;
}

/**
 * The corpus fixture name and source, mirroring the Go loader
 * (internal/interop/n8n/corpus/corpus.go loadDirectory): the name is the
 * corpus-relative path minus `.json`, the source is its first segment, and a
 * forced source prefixes the name (how an authored fixture becomes
 * `kilasflow/<file>`).
 * @param {string} relativePath
 * @param {string} [forceSource]
 * @returns {{ name: string, source: string }}
 */
export function corpusName(relativePath, forceSource) {
	const slashed = String(relativePath ?? '').replace(/\\/g, '/').replace(/^\.\//, '');
	const withoutExtension = slashed.endsWith('.json') ? slashed.slice(0, -'.json'.length) : slashed;
	if (forceSource) return { name: `${forceSource}/${withoutExtension}`, source: forceSource };
	const segments = withoutExtension.split('/');
	return { name: withoutExtension, source: segments[0] ?? '' };
}
