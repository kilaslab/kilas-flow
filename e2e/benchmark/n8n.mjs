/**
 * The n8n comparison side for FEAT-8mymac.
 *
 * Secrets: the reference instance is addressed through N8N_URL (default
 * http://localhost:5678, a loopback address, never a secret) and signed into
 * with N8N_EMAIL / N8N_PASSWORD, read from the environment and nowhere else.
 * Nothing here hardcodes a credential. allow_private_networks is never set;
 * the n8n workflows under test call the same loopback bench server, whose
 * origin the operator admits in their own n8n configuration.
 *
 * When the credentials are absent — the state of every machine that has not
 * been given them — run.mjs records an honest skip with the exact exports
 * that provision the comparison, and the KilasFlow half still runs. No
 * number is ever fabricated for this side.
 *
 * Credentialed path (first credentialed run validates it end to end):
 * sign in through the UI REST login (POST /rest/login, verified shape
 * against the local instance: {emailOrLdapLoginId, password}), create each
 * benchmark workflow from the checked-in n8n JSON in workflows.mjs,
 * activate it, time N webhook deliveries, then deactivate and delete it.
 * Webhook delivery is the uniform trigger because n8n's public API has no
 * ad-hoc execution endpoint; every benchmark n8n fixture is webhook-first
 * for exactly this reason.
 */

export const N8N_URL = process.env.N8N_URL ?? 'http://localhost:5678';

export function n8nLiveConfig() {
	const email = (process.env.N8N_EMAIL ?? '').trim();
	const password = (process.env.N8N_PASSWORD ?? '').trim();
	if (!email || !password) return null;
	return { url: N8N_URL.replace(/\/+$/, ''), email, password };
}

export function isN8nLiveConfigured() {
	return n8nLiveConfig() !== null;
}

export const N8N_LIVE_SKIP_REASON =
	'live n8n comparison skipped: N8N_EMAIL/N8N_PASSWORD are not set ' +
	`(N8N_URL is ${N8N_URL}); provision with ` +
	'`export N8N_URL="http://localhost:5678" N8N_EMAIL="operator@example.com" N8N_PASSWORD="the-operator-password"` ' +
	'and rerun to execute this half against the reference instance';

async function rest(config, cookie, method, path, body) {
	const response = await fetch(`${config.url}${path}`, {
		method,
		headers: { 'content-type': 'application/json', cookie },
		body: body === undefined ? undefined : JSON.stringify(body),
	});
	const text = await response.text();
	let parsed = null;
	try {
		parsed = text ? JSON.parse(text) : null;
	} catch {
		parsed = { raw: text.slice(0, 500) };
	}
	return { status: response.status, body: parsed, cookie: response.headers.get('set-cookie') };
}

/** Best-effort n8n version probe; unauthenticated halves stay honest. */
export async function probeN8nVersion(url) {
	const candidates = ['/healthz', '/rest/settings'];
	const out = {};
	for (const path of candidates) {
		try {
			const res = await fetch(`${url}${path}`, { signal: AbortSignal.timeout(5000) });
			out[path] = { status: res.status, body: (await res.text()).slice(0, 300) };
		} catch (error) {
			out[path] = { error: String(error).slice(0, 200) };
		}
	}
	return out;
}

function quantile(xs, q) {
	const sorted = [...xs].sort((a, b) => a - b);
	return sorted[Math.min(sorted.length - 1, Math.ceil(q * sorted.length) - 1)];
}

export function n8nStats(samples) {
	const xs = samples.map((s) => s.clientMs);
	const mean = xs.reduce((a, b) => a + b, 0) / xs.length;
	const variance = xs.reduce((a, b) => a + (b - mean) ** 2, 0) / xs.length;
	const round2 = (v) => Math.round(v * 100) / 100;
	return {
		n: xs.length,
		min: round2(xs[0] !== undefined ? Math.min(...xs) : 0),
		p50: round2(quantile(xs, 0.5)),
		p95: round2(quantile(xs, 0.95)),
		max: round2(Math.max(...xs)),
		mean: round2(mean),
		stddev: round2(Math.sqrt(variance)),
	};
}

/**
 * Credentialed n8n measurement. Implemented against the UI REST API the
 * editor itself uses; the first credentialed run validates it end to end
 * (it cannot run in this environment — no creds — so it is honest
 * scaffolding, not a verified path, and run.mjs says so in the report).
 */
export async function runN8nBenchmark({ benchmarks, runs, warmup, benchOrigin, onProgress }) {
	const config = n8nLiveConfig();
	if (!config) throw new Error('runN8nBenchmark called without N8N_EMAIL/N8N_PASSWORD');

	// Sign in. Shape verified against the local instance without creds:
	// POST /rest/login with a non-object body answers 400 naming
	// emailOrLdapLoginId, so that field plus password is the contract.
	const login = await rest(config, '', 'POST', '/rest/login', {
		emailOrLdapLoginId: config.email,
		password: config.password,
	});
	if (login.status !== 200 || !login.cookie) {
		throw new Error(`n8n login failed: status ${login.status} (body: ${JSON.stringify(login.body).slice(0, 300)})`);
	}
	const cookie = login.cookie.split(';')[0];

	const results = [];
	for (const bench of benchmarks) {
		if (bench.key === 'agent-tool-loop') {
			// The agent fixture needs an operator-wired model on the n8n
			// side; timing a differently-wired agent would not compare.
			results.push({ key: bench.key, status: 'needs-operator-model', runs: [], note: bench.divergence });
			continue;
		}
		const document = bench.n8n({ origin: benchOrigin });
		const created = await rest(config, cookie, 'POST', '/rest/workflows', document);
		if (created.status !== 200 && created.status !== 201) {
			throw new Error(`n8n create ${bench.key} failed: status ${created.status} (${JSON.stringify(created.body).slice(0, 300)})`);
		}
		const workflowId = created.body?.id ?? created.body?.data?.id;
		if (!workflowId) throw new Error(`n8n create ${bench.key} returned no id`);
		const hookPath = document.nodes.find((n) => n.type === 'n8n-nodes-base.webhook')?.parameters?.path;
		try {
			const activated = await rest(config, cookie, 'POST', `/rest/workflows/${workflowId}/activate`, {});
			if (activated.status !== 200 && activated.status !== 201) {
				throw new Error(`n8n activate ${bench.key} failed: status ${activated.status}`);
			}
			const hookURL = `${config.url}/webhook/${hookPath}`;
			const deliver = async () => {
				const t0 = performance.now();
				const res = await fetch(hookURL, {
					method: 'POST',
					headers: { 'content-type': 'application/json' },
					body: JSON.stringify({ bench: bench.key }),
				});
				await res.text();
				const clientMs = performance.now() - t0;
				if (!res.ok) throw new Error(`n8n delivery ${bench.key}: status ${res.status}`);
				return { clientMs, serverMs: null };
			};
			for (let i = 0; i < warmup; i++) await deliver();
			const samples = [];
			for (let i = 0; i < runs; i++) {
				samples.push(await deliver());
				onProgress?.(bench.key, i + 1, runs);
			}
			results.push({ key: bench.key, status: 'measured', runs: samples, hookPath });
		} finally {
			await rest(config, cookie, 'POST', `/rest/workflows/${workflowId}/deactivate`, {}).catch(() => undefined);
			await rest(config, cookie, 'DELETE', `/rest/workflows/${workflowId}`, undefined).catch(() => undefined);
		}
	}
	return { status: 'measured', results };
}
