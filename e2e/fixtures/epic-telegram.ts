// Epic-acceptance fixtures, part 1: a Telegram Bot API stub (FEAT-5fhj6p).
//
// New file only; the harness in e2e/helpers/* is untouched.
//
// The epic's first proof is a Telegram bot answering through an AI agent, and
// the real Bot API is deliberately not on the critical path of every `make
// test-e2e` run: this stub speaks the four calls the proof exercises —
// getWebhookInfo, setWebhook, deleteWebhook and sendMessage — plus a
// byte-transparent /v1/* proxy to the local Ollama, so one admitted private
// endpoint covers the bot and the model together (the harness boots each
// instance with a single allowed_private_endpoints entry; allow_private_networks
// is never set). The on-demand run against the real Bot API keeps the same
// workflow document with a real baseUrl and a public-URL tunnel; see the
// ticket for that deviation.
//
// What this is not: a model served from Node. Every model byte comes from
// Ollama, including the streaming chunks; this process only forwards them,
// exactly like e2e/fixtures/ai-gateway.ts (whose forwarder is copied here so
// the stub stays a single server).
import { createHmac } from 'node:crypto';
import { once } from 'node:events';
import {
	createServer,
	request as proxyRequest,
	type IncomingMessage,
	type Server,
	type ServerResponse
} from 'node:http';
import { expect } from '@playwright/test';
import { gate } from './gates';

export const OLLAMA_BASE_URL = process.env.KILASFLOW_TEST_OLLAMA_BASE_URL?.trim() || 'http://127.0.0.1:11434/v1';
export const OLLAMA_MODEL = process.env.KILASFLOW_TEST_OLLAMA_MODEL?.trim() || 'gemma4:12b-mlx';

// The bot token the proof uses. It never leaves loopback: the credential's
// baseUrl points at this stub, so setWebhook registers a dummy public URL
// and the test delivers updates to the real local route itself.
export const EPIC_BOT_TOKEN = '774411:epic-e2e-token';

// Where setWebhook pretends Telegram should deliver. `.invalid` never
// resolves (RFC 2606): no packet in this proof reaches the internet, and the
// lifecycle's only hard requirement — an https:// public URL — is satisfied
// without a tunnel. The on-demand real-credential run replaces this with the
// tunnel workflow from the README.
export const EPIC_PUBLIC_URL = 'https://epic-external.invalid';

function trimSlash(value: string): string {
	return value.replace(/\/+$/, '');
}

export async function probeOllama(): Promise<OllamaProbe> {
	const base = trimSlash(OLLAMA_BASE_URL);
	let catalogue: Response | undefined;
	let failure = '';
	try {
		catalogue = await fetch(`${base}/models`, { signal: AbortSignal.timeout(20_000) });
	} catch (error: unknown) {
		failure = error instanceof Error ? error.message : String(error);
	}
	if (catalogue === undefined) {
		return {
			ready: false,
			reason: gate(
				'model',
				`${OLLAMA_BASE_URL} answered nothing (${failure}). ` +
					`Start it with \`ollama serve\`, then \`ollama pull ${OLLAMA_MODEL}\`.`
			)
		};
	}
	if (!catalogue.ok) {
		return {
			ready: false,
			reason: gate(
				'model',
				`${OLLAMA_BASE_URL} answered ${catalogue.status} for /v1/models ` +
					`(is \`ollama serve\` running? wants \`ollama pull ${OLLAMA_MODEL}\` next).`
			)
		};
	}
	const payload = (await catalogue.json()) as { data?: Array<{ id?: string }> };
	const offered = (payload.data ?? []).map((entry) => entry.id).filter((id): id is string => typeof id === 'string');
	if (!offered.includes(OLLAMA_MODEL)) {
		return {
			ready: false,
			reason: gate(
				'model',
				`${OLLAMA_BASE_URL} does not serve ${JSON.stringify(OLLAMA_MODEL)} — run \`ollama pull ${OLLAMA_MODEL}\`. ` +
					`It offers: ${offered.join(', ') || '(nothing)'}.`
			)
		};
	}
	const warmed = await fetch(`${base}/chat/completions`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify({
			model: OLLAMA_MODEL,
			temperature: 0,
			stream: false,
			max_tokens: 1,
			messages: [{ role: 'user', content: 'hi' }]
		}),
		signal: AbortSignal.timeout(300_000)
	}).catch(() => undefined);
	if (warmed === undefined || !warmed.ok) {
		return {
			ready: false,
			reason: gate(
				'model',
				`${OLLAMA_BASE_URL} would not answer a completion for ${JSON.stringify(OLLAMA_MODEL)}.`
			)
		};
	}
	return { ready: true, reason: '' };
}

export interface BotApiHit {
	method: string;
	token: string;
	body: string;
}

export interface SendMessageHit {
	chatId: unknown;
	text: unknown;
	raw: string;
}

export interface TelegramStub {
	// The single loopback origin the test instance admits through its one
	// allowed_private_endpoints entry.
	origin: string;
	// The `host:port` the instance's outbound policy names.
	endpoint: string;
	// Where the chat model node points: the real Ollama catalogue and
	// completions, byte-transparent through this server.
	modelBaseURL: string;
	hits: BotApiHit[];
	setWebhookBodies: string[];
	deleteWebhookCalls: number;
	getWebhookInfoCalls: number;
	sentMessages: SendMessageHit[];
	close: () => Promise<void>;
}

// Derives the per-registration secret_token exactly the way
// nodes.TelegramSecret does: HMAC-SHA256 over
// "kilasflow-telegram-webhook:"+route, keyed by the bot token,
// base64url without padding. The proof asserts the stub received this
// value at setWebhook and then presents it back as the delivery's
// X-Telegram-Bot-Api-Secret-Token header.
export function telegramSecret(botToken: string, route: string): string {
	return createHmac('sha256', botToken).update(`kilasflow-telegram-webhook:${route}`, 'utf-8').digest('base64url');
}

export async function startTelegramStub(ollamaBaseURL: string): Promise<TelegramStub> {
	const ollama = new URL(trimSlash(ollamaBaseURL));
	const ollamaHost = ollama.hostname;
	const ollamaPort = Number(ollama.port || (ollama.protocol === 'https:' ? 443 : 80));
	const hits: BotApiHit[] = [];
	const setWebhookBodies: string[] = [];
	const sentMessages: SendMessageHit[] = [];
	let deleteWebhookCalls = 0;
	let getWebhookInfoCalls = 0;
	let registeredUrl = '';
	let messageId = 7000;

	const json = (response: ServerResponse, status: number, body: unknown): void => {
		response.writeHead(status, { 'content-type': 'application/json' });
		response.end(JSON.stringify(body));
	};

	const server: Server = createServer((incoming: IncomingMessage, outgoing: ServerResponse) => {
		const chunks: Buffer[] = [];
		incoming.on('data', (chunk: Buffer) => chunks.push(chunk));
		incoming.on('end', () => {
			const body = Buffer.concat(chunks);
			const url = new URL(incoming.url ?? '/', 'http://127.0.0.1');
			if (url.pathname === '/v1/models' || url.pathname.startsWith('/v1/')) {
				forwardToOllama(incoming, outgoing, ollamaHost, ollamaPort, url, body);
				return;
			}
			if (!url.pathname.startsWith('/bot')) {
				json(outgoing, 404, { ok: false, error: `unknown telegram stub path ${url.pathname}` });
				return;
			}
			const rest = url.pathname.slice('/bot'.length);
			const slash = rest.lastIndexOf('/');
			if (slash === -1) {
				json(outgoing, 404, { ok: false, error_code: 404, description: 'Not Found: method not found' });
				return;
			}
			const token = rest.slice(0, slash);
			const method = rest.slice(slash + 1);
			const text = body.toString('utf-8');
			hits.push({ method, token, body: text });
			let parsed: Record<string, unknown> = {};
			try {
				parsed = text ? (JSON.parse(text) as Record<string, unknown>) : {};
			} catch {
				parsed = {};
			}
			switch (method) {
				case 'getWebhookInfo':
					getWebhookInfoCalls += 1;
					json(outgoing, 200, {
						ok: true,
						result: { url: registeredUrl, has_custom_certificate: false, pending_update_count: 0 }
					});
					return;
				case 'setWebhook':
					setWebhookBodies.push(text);
					registeredUrl = typeof parsed.url === 'string' ? parsed.url : '';
					json(outgoing, 200, { ok: true, result: true, description: 'Webhook was set' });
					return;
				case 'deleteWebhook':
					deleteWebhookCalls += 1;
					registeredUrl = '';
					json(outgoing, 200, { ok: true, result: true, description: 'Webhook is deleted' });
					return;
				case 'sendMessage': {
					messageId += 1;
					const chatId = (parsed as Record<string, unknown>)['chat_id'] ?? (parsed as Record<string, unknown>)['chatId'];
					const messageText = (parsed as Record<string, unknown>)['text'];
					sentMessages.push({ chatId, text: messageText, raw: text });
					json(outgoing, 200, {
						ok: true,
						result: {
							message_id: messageId,
							chat: { id: chatId, type: 'private' },
							date: Math.floor(Date.now() / 1000),
							text: messageText
						}
					});
					return;
				}
				case 'getUpdates':
					json(outgoing, 200, { ok: true, result: [] });
					return;
				default:
					json(outgoing, 404, { ok: false, error_code: 404, description: `Not Found: unknown method ${method}` });
					return;
			}
		});
	});
	server.listen(0, '127.0.0.1');
	await once(server, 'listening');
	const address = server.address();
	if (!address || typeof address === 'string') throw new Error('Unable to start the Telegram stub');
	const origin = `http://127.0.0.1:${address.port}`;
	let closed = false;
	return {
		origin,
		endpoint: `127.0.0.1:${address.port}`,
		modelBaseURL: `${origin}/v1`,
		hits,
		setWebhookBodies,
		get deleteWebhookCalls() {
			return deleteWebhookCalls;
		},
		get getWebhookInfoCalls() {
			return getWebhookInfoCalls;
		},
		sentMessages,
		close: async () => {
			if (closed) return;
			closed = true;
			server.close();
			await once(server, 'close').catch(() => undefined);
		}
	};
}

// Forwards one model request to Ollama untouched: same path and query, same
// method, same body — the copy of e2e/fixtures/ai-gateway.ts's forwarder this
// single-server stub needs so the model turn stays byte-transparent.
function forwardToOllama(
	incoming: IncomingMessage,
	outgoing: ServerResponse,
	ollamaHost: string,
	ollamaPort: number,
	url: URL,
	body: Buffer
): void {
	const upstream = proxyRequest(
		{
			host: ollamaHost,
			port: ollamaPort,
			method: incoming.method,
			path: `${url.pathname}${url.search}`,
			headers: { ...incoming.headers, host: `${ollamaHost}:${ollamaPort}` }
		},
		(upstreamResponse) => {
			outgoing.writeHead(upstreamResponse.statusCode ?? 502, upstreamResponse.headers);
			upstreamResponse.pipe(outgoing);
		}
	);
	upstream.on('error', () => {
		if (!outgoing.headersSent) outgoing.writeHead(502, { 'content-type': 'application/json' });
		outgoing.end(JSON.stringify({ ok: false, error: 'telegram stub could not reach ollama' }));
	});
	if (body.length > 0) upstream.write(body);
	upstream.end();
}

export interface TelegramUpdate {
	update_id: number;
	message: {
		message_id: number;
		from: { id: number; first_name: string };
		chat: { id: number; type: string };
		date: number;
		text: string;
	};
}

export function epicUpdate(text: string, chatId = 774411): TelegramUpdate {
	return {
		update_id: 9001,
		message: {
			message_id: 1,
			from: { id: 555001, first_name: 'Epic' },
			chat: { id: chatId, type: 'private' },
			date: Math.floor(Date.now() / 1000),
			text
		}
	};
}

// Delivers one update to a minted route exactly as the Bot API would: a JSON
// body plus the per-registration secret header. A missing secret argument
// sends no header at all, which the trigger must refuse.
export async function deliverTelegramUpdate(
	baseURL: string,
	routeUrl: string,
	update: TelegramUpdate,
	secret?: string
): Promise<{ status: number; text: string }> {
	const response = await fetch(`${baseURL}${routeUrl}`, {
		method: 'POST',
		headers: {
			'content-type': 'application/json',
			...(secret === undefined ? {} : { 'X-Telegram-Bot-Api-Secret-Token': secret })
		},
		body: JSON.stringify(update)
	});
	return { status: response.status, text: await response.text() };
}

// Observable terminal status only — nothing here sleeps for a fixed time. A
// cold model turn can exceed the harness's minute-long poll, hence the longer
// budget (the probe already warmed the model, so this is headroom, not a wait).
export async function waitForTerminalEpic(baseURL: string, executionId: string, timeoutMs = 300_000): Promise<any> {
	const terminal = new Set(['succeeded', 'failed', 'cancelled']);
	const started = Date.now();
	for (;;) {
		const response = await fetch(`${baseURL}/api/v1/executions/${executionId}`);
		if (!response.ok) throw new Error(`GET /executions/${executionId}: status ${response.status}`);
		const record = (await response.json()) as { status: string };
		if (terminal.has(record.status)) return record;
		if (Date.now() - started > timeoutMs) {
			throw new Error(`execution ${executionId} reached no terminal status within ${timeoutMs}ms`);
		}
		await new Promise((resolve) => setTimeout(resolve, 2_000));
	}
}

export interface ChannelEvent {
	type: string;
	nodeId: string;
	data: string;
}

// Reads the whole execution channel, including the agent's nested ai.*
// events, which arrive as data-only SSE blocks — no `event:` line, with the
// type inside the JSON payload. Same shape as the ai-agent suite's reader:
// retained history first, return at the terminal event.
export async function readFullChannelEpic(baseURL: string, executionId: string): Promise<ChannelEvent[]> {
	const response = await fetch(`${baseURL}/api/v1/executions/${executionId}/events`, {
		headers: { accept: 'text/event-stream' }
	});
	if (!response.ok || !response.body) {
		throw new Error(`GET /executions/${executionId}/events: status ${response.status}`);
	}
	const found: ChannelEvent[] = [];
	const reader = response.body.getReader();
	const decoder = new TextDecoder();
	let buffer = '';
	const deadline = Date.now() + 60_000;
	try {
		for (;;) {
			if (Date.now() > deadline) throw new Error(`SSE for execution ${executionId} produced no terminal event in time`);
			const { done, value } = await reader.read();
			if (value) buffer += decoder.decode(value, { stream: true });
			let boundary = buffer.indexOf('\n\n');
			while (boundary !== -1) {
				const block = buffer.slice(0, boundary);
				buffer = buffer.slice(boundary + 2);
				let type = '';
				let nodeId = '';
				const data: string[] = [];
				for (const line of block.split('\n')) {
					if (line.startsWith('event:')) type = line.slice(6).trim();
					else if (line.startsWith('data:')) data.push(line.slice(5).trim());
				}
				const payload = data.join('\n');
				if (!type && payload.startsWith('{')) {
					try {
						const parsed = JSON.parse(payload) as { type?: string; nodeId?: string };
						if (typeof parsed.type === 'string') type = parsed.type;
						if (typeof parsed.nodeId === 'string') nodeId = parsed.nodeId;
					} catch {
						// Not JSON — nothing to classify.
					}
				}
				if (type) {
					found.push({ type, nodeId, data: payload });
					if (type === 'execution.completed' || type === 'execution.failed' || type === 'execution.cancelled') {
						return found;
					}
				}
				boundary = buffer.indexOf('\n\n');
			}
			if (done) throw new Error(`SSE for execution ${executionId} ended without a terminal event`);
		}
	} finally {
		reader.releaseLock();
		await response.body.cancel().catch(() => undefined);
	}
}

export function nodeRunEpic(record: any, nodeId: string): any {
	const run = (record.nodeRuns as any[]).find((entry) => entry.nodeId === nodeId);
	expect(run, `node ${nodeId} has a recorded run`).toBeDefined();
	return run;
}

// The recorded output is one stream per output port; returns one item's json.
export function itemJsonEpic(record: any, nodeId: string, port = 0, index = 0): any {
	const run = nodeRunEpic(record, nodeId);
	expect(run.status).toBe('succeeded');
	return run.output[port][index].json;
}
