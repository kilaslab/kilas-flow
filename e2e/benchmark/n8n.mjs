/**
 * The n8n comparison half for FEAT-8mymac (stage 2).
 *
 * Two things live here: `N8nClient`, a small REST client over n8n's own UI API
 * (`/rest/*`, verified live against 2.33.7), and the engine adapter run.mjs
 * drives with the same schedule it drives KilasFlow with.
 *
 * Where the instance comes from is run.mjs's decision — a managed throwaway
 * container (n8n-container.mjs) or an operator's own instance through the
 * env-gated external mode. This file only talks to a base URL.
 *
 * Secrets. `N8nClient` signs in with an owner email and password held in
 * process memory and a session cookie it keeps in a jar; it never logs a
 * request body and never echoes a credential. Errors surface n8n's own error
 * body (which never contains the request body) so a failure names the reason
 * rather than the payload.
 *
 * API shapes were verified against the running 2.33.7 instance, not assumed:
 *   - GET  /rest/settings (unauthenticated) carries userManagement, and
 *     (authenticated) carries versionCli;
 *   - POST /rest/owner/setup {email, firstName, lastName, password};
 *   - POST /rest/login {emailOrLdapLoginId, password} sets the n8n-auth cookie;
 *   - POST /rest/workflows returns data.id and data.versionId;
 *   - POST /rest/workflows/:id/activate {versionId} sets data.active true;
 *   - GET  /rest/executions?workflowId=…&limit=… returns data.results, each a
 *     record whose terminal shape carries startedAt and stoppedAt;
 *   - a workflow must be archived before it can be deleted;
 *   - POST /rest/credentials {name, type, data}.
 */

import { randomUUID } from 'node:crypto';

import { deliverTimed } from './method.mjs';
import { payloadFor } from './workflows.mjs';

export const N8N_URL = process.env.N8N_URL ?? 'http://localhost:5678';

/** The name of the credential the agent row uses, on both engines' books. */
export const CREDENTIAL_NAME = 'Bench Ollama';

/** Terminal n8n execution statuses, as reported by /rest/executions. */
const TERMINAL_STATUS = new Set(['success', 'failed', 'crashed', 'error', 'canceled', 'cancelled']);

const EXECUTION_PAGE = 100;
const EXECUTION_MAX_PAGES = 20;

function sleep(ms) {
	return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * The external-mode configuration: an operator's own instance.
 *
 * Both variables must be set; a URL without a credential is not usable and is
 * reported as an honest skip rather than a failed sign-in.
 */
export function externalN8nConfig(env = process.env) {
	const url = (env.N8N_URL ?? '').trim();
	const email = (env.N8N_EMAIL ?? '').trim();
	const password = env.N8N_PASSWORD ?? '';
	if (url === '' || email === '' || password === '') return null;
	return { url, email, password, stubOrigin: (env.N8N_STUB_ORIGIN ?? '').trim() || null };
}

/** A session-cookie REST client over n8n's UI API. */
export class N8nClient {
	constructor({ url, browserId = 'kilasflow-bench' }) {
		this.url = String(url).replace(/\/+$/, '');
		this.browserId = browserId;
		this.jar = new Map();
	}

	cookieHeader() {
		return [...this.jar.entries()].map(([key, value]) => `${key}=${value}`).join('; ');
	}

	hasSession() {
		return this.jar.size > 0;
	}

	absorbCookies(response) {
		const cookies = response.headers.getSetCookie?.() ?? [];
		for (const cookie of cookies) {
			const [pair] = cookie.split(';');
			const index = pair.indexOf('=');
			if (index > 0) this.jar.set(pair.slice(0, index).trim(), pair.slice(index + 1).trim());
		}
	}

	/** One request. Throws with n8n's own error body, never the request body. */
	async request(method, path, body, extraHeaders = {}) {
		const headers = { 'content-type': 'application/json', 'browser-id': this.browserId, ...extraHeaders };
		if (this.jar.size > 0) headers.cookie = this.cookieHeader();
		const response = await fetch(`${this.url}${path}`, {
			method,
			headers,
			body: body === undefined ? undefined : JSON.stringify(body),
			redirect: 'manual',
		});
		this.absorbCookies(response);
		const text = await response.text();
		let json = null;
		try {
			json = text ? JSON.parse(text) : null;
		} catch {
			// A non-JSON body is reported as text below, never as a fabricated object.
		}
		return { status: response.status, ok: response.ok, json, text };
	}

	async expectOk(method, path, { body, statuses = [200], extraHeaders } = {}) {
		const result = await this.request(method, path, body, extraHeaders);
		if (!statuses.includes(result.status)) {
			throw new Error(`${method} ${path}: n8n answered ${result.status} (${result.text.slice(0, 500) || 'no body'})`);
		}
		return result;
	}

	/** Unauthenticated bootstrap state; also proves the server is up. */
	async settings() {
		return (await this.expectOk('GET', '/rest/settings')).json;
	}

	/** The version string from the running instance (authenticated). */
	async version() {
		const settings = await this.settings();
		return settings?.data?.versionCli ?? settings?.versionCli ?? null;
	}

	async ownerSetup({ email, password }) {
		await this.expectOk('POST', '/rest/owner/setup', {
			body: { email, firstName: 'Bench', lastName: 'Owner', password },
		});
	}

	async login({ email, password }) {
		await this.expectOk('POST', '/rest/login', { body: { emailOrLdapLoginId: email, password } });
		if (!this.hasSession()) throw new Error('login succeeded but n8n set no session cookie');
	}

	async createWorkflow(document) {
		const result = await this.expectOk('POST', '/rest/workflows', { body: document, statuses: [200, 201] });
		const data = result.json?.data ?? result.json;
		if (!data?.id) throw new Error(`POST /rest/workflows returned no workflow id (${result.text.slice(0, 300)})`);
		return { id: data.id, versionId: data.versionId ?? null, active: data.active === true };
	}

	/**
	 * Activate a workflow.
	 *
	 * Some builds ask for a `push-ref` header to accept an activation; if the
	 * server says so, the call is retried once with a fresh random ref. The
	 * active flag is asserted by the caller so a silent refusal cannot pass.
	 */
	async activate(id, versionId) {
		const body = versionId ? { versionId } : {};
		const first = await this.request('POST', `/rest/workflows/${id}/activate`, body);
		if (first.status >= 400 && /push-ref/i.test(first.text)) {
			return this.expectOk('POST', `/rest/workflows/${id}/activate`, { body, extraHeaders: { 'push-ref': randomUUID() } });
		}
		if (!first.ok) throw new Error(`POST /rest/workflows/${id}/activate: n8n answered ${first.status} (${first.text.slice(0, 300) || 'no body'})`);
		return first;
	}

	async deactivate(id) {
		return this.expectOk('POST', `/rest/workflows/${id}/deactivate`);
	}

	/** n8n refuses to delete a workflow until it has been archived. */
	async archive(id) {
		return this.expectOk('POST', `/rest/workflows/${id}/archive`, { body: {} });
	}

	async deleteWorkflow(id) {
		return this.expectOk('DELETE', `/rest/workflows/${id}`);
	}

	/**
	 * Every execution record for a workflow.
	 *
	 * Newest first, `limit` per page, following `lastId` while a page comes back
	 * full and unseen ids keep appearing. n8n 2.33.7 returns `data.results` with
	 * no cursor token, so `lastId` (the id of the page's oldest record) is the
	 * pagination key; the loop is bounded and de-duplicates by id, so a build
	 * that ignores `lastId` stops after one page instead of looping.
	 */
	async listExecutions(workflowId) {
		const byId = new Map();
		let lastId = null;
		for (let page = 0; page < EXECUTION_MAX_PAGES; page++) {
			const query = `/rest/executions?workflowId=${encodeURIComponent(workflowId)}&limit=${EXECUTION_PAGE}${lastId ? `&lastId=${encodeURIComponent(lastId)}` : ''}`;
			const result = await this.expectOk('GET', query);
			const rows = result.json?.data?.results ?? result.json?.data ?? [];
			if (!Array.isArray(rows) || rows.length === 0) break;
			const before = byId.size;
			for (const row of rows) if (row?.id) byId.set(String(row.id), row);
			if (byId.size === before || rows.length < EXECUTION_PAGE) break;
			lastId = rows[rows.length - 1]?.id ?? null;
			if (!lastId) break;
		}
		return [...byId.values()];
	}

	async createCredential({ name, type, data }) {
		const result = await this.expectOk('POST', '/rest/credentials', { body: { name, type, data }, statuses: [200, 201] });
		const created = result.json?.data ?? result.json;
		if (!created?.id) throw new Error(`POST /rest/credentials returned no id (${result.text.slice(0, 300)})`);
		return { id: created.id, name: created.name ?? name, type: created.type ?? type };
	}
}

/** Is this record terminal (stoppedAt set or a terminal status)? */
function isTerminal(record) {
	return Boolean(record?.stoppedAt) || TERMINAL_STATUS.has(record?.status);
}

/**
 * The n8n engine adapter.
 *
 * `host` is the running instance: `{url, containerName?, origin, modelBaseUrl}`.
 * `url` is where the harness delivers webhooks (the published loopback port);
 * `origin` is how the container itself reaches the bench server
 * (host.docker.internal), used for the stub bases in the fixtures and for the
 * agent's Ollama credential. `modelBaseUrl` routes n8n's model calls through
 * the same bench gateway KilasFlow uses, so per-run model calls are countable.
 */
export function createN8nAdapter({ client, host, view }) {
	let credential = null;

	async function ensureCredential(key) {
		if (credential) return credential;
		credential = await client.createCredential({
			name: CREDENTIAL_NAME,
			type: key,
			data: { baseUrl: host.modelBaseUrl },
		});
		return credential;
	}

	return {
		name: 'n8n',
		view,
		async prepare(bench, context) {
			const document = structuredClone(bench.n8n(context));
			let credentialId = null;
			if (bench.credential) {
				const created = await ensureCredential(bench.credential.key);
				credentialId = created.id;
				const node = document.nodes.find((entry) => entry.name === bench.credential.nodeId || entry.id === bench.credential.nodeId);
				if (!node) throw new Error(`${bench.key}: the credential marker names a node that is not in the document`);
				node.credentials = { [bench.credential.key]: { id: created.id, name: created.name } };
			}
			const workflow = await client.createWorkflow(document);
			const activation = await client.activate(workflow.id, workflow.versionId);
			const active = (activation.json?.data ?? activation.json)?.active === true;
			if (!active) {
				throw new Error(`${bench.key}: n8n refused to activate the workflow (${activation.text.slice(0, 300) || 'no body'})`);
			}
			return {
				workflowId: workflow.id,
				versionId: workflow.versionId,
				url: `${host.url}/webhook/${bench.webhookPath}`,
				credentialId,
			};
		},
		deliver(prepared, bench, index, context) {
			return deliverTimed(prepared.url, payloadFor(bench, index, context));
		},
		/** Untimed: poll until every delivery so far has a terminal execution. */
		async settle(prepared, expected, timeoutMs = 180_000) {
			const deadline = Date.now() + timeoutMs;
			for (;;) {
				const records = await client.listExecutions(prepared.workflowId);
				const terminal = records.filter(isTerminal);
				if (terminal.length >= expected) return;
				if (Date.now() > deadline) {
					throw new Error(`settle: n8n workflow ${prepared.workflowId} reached ${terminal.length}/${expected} terminal executions in ${timeoutMs}ms`);
				}
				await sleep(50);
			}
		},
		/** Records in the shape pairRecords expects (stoppedAt → finishedAt). */
		async records(prepared) {
			const records = await client.listExecutions(prepared.workflowId);
			return records
				.filter(isTerminal)
				.map((record) => ({
					id: record.id,
					status: record.status,
					startedAt: record.startedAt,
					finishedAt: record.stoppedAt,
				}));
		},
		async rss() {
			return host.rss ? host.rss() : null;
		},
		async teardown(prepared) {
			if (!prepared?.workflowId) return;
			await client.deactivate(prepared.workflowId).catch(() => undefined);
			await client.archive(prepared.workflowId).catch(() => undefined);
			await client.deleteWorkflow(prepared.workflowId).catch(() => undefined);
		},
	};
}
