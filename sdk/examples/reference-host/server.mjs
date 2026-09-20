/**
 * Reference multi-tenant host for KilasFlow.
 *
 * One process serves two tenants (acme, birch). Each tenant has its own
 * KilasFlow API key and therefore its own client, built by
 * tenantClientFactory: the credential is fixed per client, so a request can
 * never be sent for the wrong customer by swapping a key on a shared client.
 *
 * The browser never sees an API key. It gets a short-lived, workflow-scoped
 * embed token per tenant page, plus per-execution stream tickets — both
 * minted by this backend, which checks every request against its own tenant
 * registry first. An unknown tenant slug mints nothing.
 */

import { createServer } from 'node:http';
import { readFile, writeFile } from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { KilasFlowClient, tenantClientFactory } from '@kilasflow/sdk/server';

const here = dirname(fileURLToPath(import.meta.url));
const port = Number(process.env.PORT ?? 4174);
const origin = process.env.HOST_ORIGIN ?? `http://localhost:${port}`;
const kilasflowUrl = process.env.KILASFLOW_URL ?? 'http://127.0.0.1:8080';
const statePath = join(here, '.reference-state.json');

const forTenant = tenantClientFactory({ baseUrl: kilasflowUrl });

/**
 * The host's own customer registry. The slugs are the only tenant identities
 * this process knows: every per-tenant route looks the slug up here, and an
 * unknown slug answers 404 before any KilasFlow call happens. The API keys
 * come from the environment — explicit host configuration the SDK never
 * looks up itself.
 */
const tenants = {
	acme: {
		label: 'Acme',
		apiKey: process.env.TENANT_A_API_KEY,
		branding: { name: 'Acme Flows', accent: '#0ea5e9' }
	},
	birch: {
		label: 'Birch',
		apiKey: process.env.TENANT_B_API_KEY,
		branding: { name: 'Birch Automations', accent: '#16a34a' }
	}
};

for (const [slug, tenant] of Object.entries(tenants)) {
	if (!tenant.apiKey) throw new Error(`${slug} has no API key: set TENANT_A_API_KEY and TENANT_B_API_KEY`);
	tenant.client = forTenant(tenant.apiKey);
}

const state = await loadState();
for (const slug of Object.keys(tenants)) await ensureTenant(slug);
await saveState();

createServer(async (request, response) => {
	try {
		const url = new URL(request.url ?? '/', origin);

		if (url.pathname === '/') return serveLanding(response);
		if (url.pathname === '/tenant.html') return serveFile(response, join(here, 'tenant.html'), 'text/html; charset=utf-8');
		if (url.pathname.startsWith('/t/')) {
			const slug = url.pathname.slice('/t/'.length);
			if (!tenants[slug]) return notFound(response);
			return serveFile(response, join(here, 'tenant.html'), 'text/html; charset=utf-8');
		}

		// The installed SDK, served to the page. Only the published package's
		// compiled output is reachable — never server sources, never a key.
		if (url.pathname.startsWith('/node_modules/@kilasflow/sdk/dist/')) {
			return serveFile(response, join(here, decodeURIComponent(url.pathname.slice(1))), 'text/javascript; charset=utf-8');
		}

		const match = /^\/api\/([^/]+)\/(.+)$/.exec(url.pathname);
		if (match) {
			const tenant = tenants[match[1]];
			// Minting a session is an authorization decision this host owns:
			// EmbedSessions.Create verifies the workflow exists in the
			// tenant, it cannot know whether the end user in front of the
			// browser is entitled to it. An unknown slug is refused here,
			// before any token exists.
			if (!tenant) return notFound(response);
			// Awaited, not returned: a rejection must land in the catch
			// below as a 500, never as an unhandled rejection that kills
			// the process.
			await serveTenantApi(tenant, match[2], url, request, response);
			return;
		}
	} catch (error) {
		response.writeHead(500, { 'Content-Type': 'application/json' });
		response.end(JSON.stringify({ error: String(error) }));
	}
}).listen(port, () => console.log(`Reference host on ${origin}`));

async function serveTenantApi(tenant, action, url, request, response) {
	const json = (status, body) => {
		response.writeHead(status, { 'Content-Type': 'application/json' });
		response.end(JSON.stringify(body));
	};

	// What the page needs before it mounts: its label and accent. The accent
	// is a validated value, never markup — the page sets it as a CSS
	// variable, and the editor renders branding into elements it controls.
	if (action === 'info' && request.method === 'GET') {
		return json(200, { label: tenant.label, accent: tenant.branding.accent });
	}

	// The page asks its own backend for a session. The token it gets back is
	// scoped to this tenant's workflow, this origin, and a few minutes. The
	// origin is a server-side constant, never taken from the request: a
	// token sent to an origin the host did not verify is a token handed to
	// someone else's page.
	if (action === 'embed-session' && request.method === 'POST') {
		const session = await tenant.client.createEmbedSession({
			workflowId: tenant.workflowId,
			scopes: ['workflow:read', 'workflow:write', 'workflow:run'],
			origin,
			branding: tenant.branding
		});
		return json(200, { ...session, baseUrl: kilasflowUrl });
	}

	// EventSource cannot send an Authorization header, so the page asks its
	// backend for a single-use ticket per execution and spends it as
	// ?ticket=. The ticket is minted only after the backend confirms the
	// execution belongs to this tenant's workflow — a ticket for another
	// customer's run would be a cross-tenant read.
	if (action === 'stream-ticket' && request.method === 'GET') {
		const executionId = url.searchParams.get('executionId');
		if (!executionId) return json(400, { error: 'executionId is required' });
		if (!await ownExecution(tenant, executionId)) return notFound(response);
		return json(200, await tenant.client.createStreamTicket(executionId));
	}

	if (action === 'execution' && request.method === 'GET') {
		const executionId = url.searchParams.get('executionId');
		if (!executionId) return json(400, { error: 'executionId is required' });
		const execution = await ownExecution(tenant, executionId);
		if (!execution) return notFound(response);
		return json(200, execution);
	}

	// The page fires the feature, not the plumbing: the webhook secret lives
	// in this process's state file and is sent server-to-server. The browser
	// learns the execution id, never the header value that authorizes
	// deliveries.
	if (action === 'fire' && request.method === 'POST') {
		const email = `user-${Date.now()}@example.com`;
		const delivery = await fetch(kilasflowUrl + tenant.webhookUrl, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json', 'X-Reference-Key': tenant.webhookSecret },
			body: JSON.stringify({ email, plan: 'trial' })
		});
		if (!delivery.ok) return json(502, { error: `webhook answered ${delivery.status}` });
		// The acknowledgement is n8n's own immediate answer and names no run, so
		// the id comes from the tenant's own key instead. A delivery the server
		// filtered was never queued, and says so rather than waiting for a run
		// that does not exist.
		const acknowledgement = await delivery.json();
		if (acknowledgement.filtered) return json(200, { filtered: true });
		const executionId = await newestExecutionId(tenant);
		const execution = await waitForExecution(tenant, executionId);
		await recordSignup(tenant, email);
		return json(200, { executionId, status: execution.status });
	}

	// Host-side datastore read through the tenant's own key: the embed token
	// is default-denied on /datastores/*, so this call is backend-only by
	// construction, not by convention.
	if (action === 'signups' && request.method === 'GET') {
		const rows = await datastore(tenant, 'GET', `/datastores/${tenant.datastoreId}/rows?limit=25`);
		return json(200, rows);
	}

	return notFound(response);
}

/**
 * Reads an execution through the tenant's client, answering null for
 * anything that is not this tenant's workflow. The API already scopes the
 * read — another tenant's execution answers 404 there, never as someone
 * else's row — and the workflow check below is the second lock for a
 * caller whose key could see more than one workflow.
 */
async function ownExecution(tenant, executionId) {
	const execution = await tenant.client.getExecution(executionId).catch(() => null);
	if (!execution || execution.workflowId !== tenant.workflowId) return null;
	return execution;
}

const TERMINAL = new Set(['succeeded', 'failed', 'cancelled']);

async function waitForExecution(tenant, executionId) {
	const deadline = Date.now() + 30_000;
	for (;;) {
		const execution = await ownExecution(tenant, executionId);
		if (!execution) throw new Error(`execution ${executionId} is not this tenant's`);
		if (TERMINAL.has(execution.status)) return execution;
		if (Date.now() > deadline) throw new Error(`execution ${executionId} did not finish in 30s`);
		await new Promise((resolve) => setTimeout(resolve, 500));
	}
}

/**
 * The id of the execution a delivery just queued.
 *
 * The trigger answers in n8n's `onReceived` mode, whose body is n8n's own
 * `{"message":"Workflow was started"}` — an imported workflow's caller expects
 * exactly that, and no part of it names a run. So the id is read back through
 * the tenant's own key instead: the execution list is newest-first, the server
 * queues the execution before it writes the acknowledgement, and this sample
 * fires one delivery at a time per tenant.
 */
async function newestExecutionId(tenant) {
	const page = await tenant.client.listExecutions({ workflowId: tenant.workflowId, limit: 1 });
	const newest = page.items?.[0];
	if (!newest) throw new Error(`the delivery queued no execution for ${tenant.label}`);
	return newest.id;
}

/**
 * Provisions everything one tenant needs, reusing what a previous boot left
 * in the state file. Import is used for provisioning rather than create,
 * for one reason: it is the only operation that reports the minted webhook
 * address back. Activation keeps reporting nothing, so without the import
 * response the host would own a workflow and have no URL to send anything
 * to. Activating never changes the reported address.
 */
async function ensureTenant(slug) {
	const tenant = tenants[slug];
	const cached = state[slug] ?? {};

	if (cached.workflowId) {
		try {
			await tenant.client.getWorkflow(cached.workflowId);
			const credentials = await tenant.client.listCredentials();
			if (credentials.some((credential) => credential.id === cached.credentialId)) {
				Object.assign(tenant, cached);
				return;
			}
			// The webhook credential is gone; fall through and provision again.
		} catch {
			// The workflow is gone; fall through and provision again.
		}
	}
	const name = `Reference ${tenant.label}`;
	// One tenant cannot claim the same endpoint label twice: an active
	// workflow holding `reference-<slug>` would refuse this one's
	// activation. A previous boot's workflow that this state file lost
	// track of is exactly that, so retire stale namesakes first.
	for (const workflow of await tenant.client.listWorkflows()) {
		if (workflow.name === name && workflow.id !== cached.workflowId) {
			await tenant.client.deactivateWorkflow(workflow.id).catch(() => {});
			await tenant.client.deleteWorkflow(workflow.id).catch(() => {});
		}
	}
	const imported = await tenant.client.importWorkflow({
		format: 'n8n',
		name,
		workflow: {
			name,
			nodes: [
				{
					id: 'hook', name: 'Signup webhook', type: 'n8n-nodes-base.webhook', typeVersion: 2,
					position: [80, 80],
					parameters: { path: `reference-${slug}`, httpMethod: 'POST', responseMode: 'onReceived' }
				}
			],
			connections: {}
		}
	});
	const workflowId = imported.workflow.id;
	const webhookUrl = imported.webhooks?.[0]?.url;
	if (!webhookUrl) throw new Error(`import for ${slug} reported no webhook address`);

	const webhookSecret = randomBytes(24).toString('hex');
	const credential = await tenant.client.createCredential({
		name: `Reference ${tenant.label} webhook`,
		type: 'httpHeaderAuth',
		fields: { name: 'X-Reference-Key', value: webhookSecret }
	});

	// The stored credential authenticates deliveries to the trigger: the node
	// carries the credential reference and the header-auth mode, and the
	// webhook boundary verifies the header server-side. A delivery without
	const document = (await tenant.client.getWorkflow(workflowId)).latestVersion.document;
	const webhook = (document.nodes ?? []).find((node) => node.type === 'kilasflow.webhook');
	if (!webhook) throw new Error(`imported workflow for ${slug} has no webhook trigger`);
	webhook.parameters = { ...webhook.parameters, authentication: 'headerAuth' };
	webhook.credentials = { httpHeaderAuth: credential.id };
	await tenant.client.updateWorkflow(workflowId, {
		schemaVersion: document.schemaVersion,
		name: document.name,
		nodes: document.nodes,
		connections: document.connections,
		settings: document.settings
	});
	await tenant.client.activateWorkflow(workflowId);

	const datastoreId = await ensureDatastore(tenant, slug);

	Object.assign(tenant, {
		workflowId,
		webhookUrl,
		credentialId: credential.id,
		webhookSecret,
		datastoreId
	});
	state[slug] = {
		workflowId: tenant.workflowId,
		webhookUrl: tenant.webhookUrl,
		credentialId: tenant.credentialId,
		webhookSecret: tenant.webhookSecret,
		datastoreId: tenant.datastoreId
	};
}

/**
 * One datastore per tenant, reached with that tenant's key. The catalogue
 * lookup clauses on tenant and id together, so naming another tenant's
 * datastore reads as unknown and answers 404 — isolation the reference app
 * relies on rather than reimplements.
 *
 * The SDK has no datastore methods yet, so this helper speaks the documented
 * REST endpoints with the same tenant key the SDK client holds. It will read
 * as SDK calls once they exist; the endpoints, not the spelling, are the
 * contract.
 */
async function ensureDatastore(tenant, slug) {
	const name = `reference-${slug}`;
	const existing = await datastore(tenant, 'GET', '/datastores');
	const found = existing.items?.find((store) => store.name === name);
	if (found) return found.id;
	const created = await datastore(tenant, 'POST', '/datastores', {
		name,
		columns: [
			{ name: 'email', type: 'string' },
			{ name: 'plan', type: 'string' }
		]
	});
	return created.id;
}

/** Writes the fired signup into the tenant's datastore and reads it back. */
async function recordSignup(tenant, email) {
	await datastore(tenant, 'POST', `/datastores/${tenant.datastoreId}/rows`, {
		values: { email, plan: 'trial' }
	});
	return datastore(
		tenant, 'GET',
		`/datastores/${tenant.datastoreId}/rows?limit=5&columnName=email&condition=eq&value=${encodeURIComponent(email)}`
	);
}

async function datastore(tenant, method, path, body) {
	const response = await fetch(kilasflowUrl + '/api/v1' + path, {
		method,
		headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${tenant.apiKey}` },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (!response.ok) throw new Error(`datastore ${method} ${path} answered ${response.status}`);
	if (response.status === 204) return null;
	return response.json();
}

async function loadState() {
	try {
		return JSON.parse(await readFile(statePath, 'utf8'));
	} catch {
		return {};
	}
}

async function saveState() {
	await writeFile(statePath, JSON.stringify(state, null, 2) + '\n');
}

function serveLanding(response) {
	response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
	response.end(`<!doctype html>
<html lang="en"><head><meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>KilasFlow reference host</title></head>
<body style="font: 14px system-ui, sans-serif; padding: 24px">
<h1>KilasFlow reference host</h1>
<p>Two tenants, one process, each with its own key, workflow, credential, webhook and datastore:</p>
<ul>
<li><a href="/t/acme">Acme</a> — customer onboarding flow</li>
<li><a href="/t/birch">Birch</a> — customer onboarding flow</li>
</ul>
</body></html>`);
}

async function serveFile(response, file, contentType) {
	try {
		response.writeHead(200, { 'Content-Type': contentType });
		response.end(await readFile(file));
	} catch {
		notFound(response);
	}
}

function notFound(response) {
	response.writeHead(404, { 'Content-Type': 'application/json' });
	response.end(JSON.stringify({ error: 'Not found' }));
}
