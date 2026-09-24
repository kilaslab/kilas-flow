import * as m from '$lib/paraglide/messages.js';
import type { Connection, Definition, ExecutionNodeRunResource, Node } from '$lib/api/generated/models';
import { resolveDefinition } from './document';

/**
 * `skipped` names a node that did not run, as against one that ran and failed —
 * the inspector must not show those the same way.
 *
 * It arrives from two places and both mean the same thing to a reader. The
 * server sends it for a node the runner pruned because no incoming item channel
 * delivered anything, which is the untaken arm of a branch. The client falls
 * back to it for a node with no run record at all, which is a node the
 * execution never got as far as.
 */
export type NodeRunStatus = string | 'skipped';

/** Item shape carried on a node-run output port. */
type OutputItem = { json?: unknown; binary?: Record<string, BinaryReference> };

/**
 * What an item carries in place of a payload.
 *
 * The bytes live in the server's binary store, keyed by tenant, execution and
 * this id. Only this metadata ever reaches the browser, which is why the
 * inspector can show an attachment at all without the editor ever being handed
 * a multi-megabyte response it would have to decide what to do with.
 */
export type BinaryReference = {
	id: string;
	fileName?: string;
	mediaType?: string;
	size?: number;
};

/** One attachment, with enough context to say which item it came off. */
export type Attachment = {
	/** Index of the output port slot the item sat on. */
	port: number;
	/** Index of the item within that slot. */
	item: number;
	/** The binary property name the node attached it under. */
	property: string;
	reference: BinaryReference;
};

/**
 * Every binary reference a node run produced, flattened for display.
 *
 * The inspector shows name, type and size and nothing else: a payload never
 * leaves the store, so there is nothing here to render inline even if it wanted
 * to, and an editor that tried would be reaching for bytes the API does not
 * serve.
 */
export function binaryAttachments(output: unknown): Attachment[] {
	if (!Array.isArray(output)) return [];

	const attachments: Attachment[] = [];
	output.forEach((slot, port) => {
		if (!Array.isArray(slot)) return;
		(slot as OutputItem[]).forEach((item, index) => {
			const binary = item?.binary;
			if (!binary || typeof binary !== 'object') return;
			for (const [property, reference] of Object.entries(binary)) {
				if (!reference || typeof reference !== 'object' || typeof reference.id !== 'string') continue;
				attachments.push({ port, item: index, property, reference });
			}
		});
	});
	return attachments;
}

/** The decimal unit ladder, one catalog message per rung, smallest first. */
const SIZE_UNITS: ((inputs: { value: string | number }) => string)[] = [
	m.executions_size_kilobytes,
	m.executions_size_megabytes,
	m.executions_size_gigabytes,
	m.executions_size_terabytes
];

/**
 * A byte count a person can read.
 *
 * Decimal units, because that is what every file manager and every download
 * dialog a user has seen reports, and an attachment is a file to them.
 */
export function formatBytes(size: number | undefined): string {
	if (size === undefined || !Number.isFinite(size) || size < 0) return m.executions_size_unknown();
	if (size < 1000) return m.executions_size_bytes({ value: size });
	let value = size / 1000;
	let unit = 0;
	while (value >= 1000 && unit < SIZE_UNITS.length - 1) {
		value /= 1000;
		unit += 1;
	}
	return SIZE_UNITS[unit]({ value: value < 10 ? value.toFixed(1) : Math.round(value) });
}

/**
 * Reduces a node-run trace to the final attempt per node.
 *
 * Retries append attempts rather than replacing them, so the canvas must show
 * the outcome that actually decided the run, not the first try.
 */
export function latestNodeRuns(
	nodeRuns: ExecutionNodeRunResource[] | null | undefined
): Map<string, ExecutionNodeRunResource> {
	const latest = new Map<string, ExecutionNodeRunResource>();
	for (const run of nodeRuns ?? []) {
		const current = latest.get(run.nodeId);
		if (!current || run.attempt > current.attempt || (run.attempt === current.attempt && run.sequence > current.sequence)) {
			latest.set(run.nodeId, run);
		}
	}
	return latest;
}

export function nodeRunStatus(nodeID: string, runs: Map<string, ExecutionNodeRunResource>): NodeRunStatus {
	return runs.get(nodeID)?.status ?? 'skipped';
}

/**
 * The one node type that can print to a console — `nodes/jscode.go`'s
 * `JSCodeNodeType`. The Console tab is offered for this type only.
 */
export const CODE_NODE_TYPE = 'kilasflow.jsCode';

/** One line a Code node's `console.log`/`info`/`warn`/`error`/`debug` printed. */
export type ConsoleLine = { level: string; text: string; at?: string };

/** What a Code node's run printed, parsed off `NodeRun.Console` or one live `code.console` event's `data`. */
export type ConsoleDetail = { lines: ConsoleLine[]; truncated: boolean };

/**
 * Reads a node's console output from the server's untyped `unknown`.
 *
 * The API types `NodeRun.console` and a `code.console` event's `data` as
 * unknown — both carry the same `engine.ConsoleDetail` shape, so this one
 * parser reads both. A line with no text is dropped, and a missing level
 * reads as `log`, matching the Go side's own zero value.
 *
 * `null` means "nothing recorded", which is the case for every node that is
 * not a Code node as much as it is for one that printed nothing — the caller
 * tells those apart from the node's type, not from this parse.
 */
export function parseConsole(value: unknown): ConsoleDetail | null {
	if (value === null || typeof value !== 'object' || Array.isArray(value)) return null;
	const record = value as Record<string, unknown>;
	const lines: ConsoleLine[] = [];
	for (const line of Array.isArray(record.lines) ? record.lines : []) {
		if (line === null || typeof line !== 'object') continue;
		const { level, text, at } = line as Record<string, unknown>;
		if (typeof text !== 'string') continue;
		lines.push({ level: typeof level === 'string' && level ? level : 'log', text, at: typeof at === 'string' ? at : undefined });
	}
	const truncated = record.truncated === true;
	return lines.length > 0 || truncated ? { lines, truncated } : null;
}

/** warn and error stand out from ordinary output; debug recedes. */
export function consoleTone(level: string): string {
	switch (level) {
		case 'error':
			return 'text-destructive';
		case 'warn':
			return 'text-warning';
		case 'debug':
			return 'text-muted-foreground';
		default:
			return '';
	}
}

/**
 * Counts the items each connection carried.
 *
 * A node run's output is an array of port slots in the definition's declared
 * output order, so a connection's count is the length of the slot matching its
 * source port. Connections whose source never recorded an output are left out
 * entirely: "no data yet" must not read as "zero items".
 */
export function edgeItemCounts(
	connections: Connection[] | null | undefined,
	nodes: Node[] | null | undefined,
	definitions: Definition[],
	runs: Map<string, ExecutionNodeRunResource>
): Map<string, number> {
	const nodeByID = new Map((nodes ?? []).map((node) => [node.id, node]));
	const counts = new Map<string, number>();

	for (const connection of connections ?? []) {
		const source = nodeByID.get(connection.source.nodeId);
		if (!source) continue;
	const definition = resolveDefinition(source.type, source.typeVersion, definitions);
		const portIndex = (definition?.outputs ?? []).findIndex((port) => port.name === connection.source.port);
		if (portIndex < 0) continue;

		const output = runs.get(connection.source.nodeId)?.output;
		if (!Array.isArray(output)) continue;
		const slot = output[portIndex];
		if (!Array.isArray(slot)) continue;
		counts.set(connection.id, (slot as OutputItem[]).length);
	}
	return counts;
}

export function executionDurationMs(execution: { startedAt: string; finishedAt?: string }): number | null {
	if (!execution.finishedAt) return null;
	const started = Date.parse(execution.startedAt);
	const finished = Date.parse(execution.finishedAt);
	if (Number.isNaN(started) || Number.isNaN(finished)) return null;
	return finished - started;
}

export function formatDuration(milliseconds: number | null | undefined): string {
	if (milliseconds === null || milliseconds === undefined || Number.isNaN(milliseconds)) {
		return m.executions_duration_unknown();
	}
	if (milliseconds < 1000) return m.executions_duration_milliseconds({ value: Math.round(milliseconds) });
	if (milliseconds < 60_000) {
		return m.executions_duration_seconds({ value: Math.round(milliseconds / 100) / 10 });
	}
	const minutes = Math.floor(milliseconds / 60_000);
	const seconds = Math.round((milliseconds % 60_000) / 1000);
	return m.executions_duration_minutes_seconds({ minutes, seconds });
}

export function formatTimestamp(value: string | undefined): string {
	if (!value) return m.executions_timestamp_unknown();
	const date = new Date(value);
	// Year 1 is robfig/cron's zero time, not a real run: an impossible cron
	// used to have it stored as lastRunAt/nextRunAt and shown as "Jan 1,
	// 07:07:12" (BUG-g7ffj1). It reads the same as no timestamp at all.
	if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) {
		return m.executions_timestamp_unknown();
	}
	return date.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

/** Tailwind classes per status, so list rows and canvas badges agree. */
export function statusTone(status: string): string {
	switch (status) {
		case 'succeeded':
			return 'bg-success/15 text-success border-success/30';
		case 'failed':
			return 'bg-destructive/15 text-destructive border-destructive/30';
		case 'cancelled':
		case 'cancelling':
			return 'bg-warning/15 text-warning border-warning/30';
		case 'running':
			return 'bg-primary/15 text-primary border-primary/30';
		case 'waiting':
			return 'bg-violet-500/15 text-violet-600 border-violet-500/30 dark:text-violet-400';
		case 'skipped':
			return 'bg-muted text-muted-foreground border-border';
		default:
			return 'bg-secondary text-secondary-foreground border-border';
	}
}

/**
 * The status labels the catalogs carry, including the client-only `skipped`.
 *
 * Declared rather than switched on, so every message keeps a reference the
 * tree-shaker can see, and a status with no entry (`manual`, `schedule` — the
 * server's trigger tokens) falls through to the capitalised token below.
 */
const STATUS_LABELS: Partial<Record<string, () => string>> = {
	queued: m.executions_status_queued,
	running: m.executions_status_running,
	cancelling: m.executions_status_cancelling,
	waiting: m.executions_status_waiting,
	succeeded: m.executions_status_succeeded,
	failed: m.executions_status_failed,
	cancelled: m.executions_status_cancelled,
	skipped: m.executions_status_skipped
};

/** Human label for a status, including the client-only `skipped`. */
export function statusLabel(status: string): string {
	return STATUS_LABELS[status]?.() ?? status.charAt(0).toUpperCase() + status.slice(1);
}

/**
 * The status as a colour the canvas can paint a node's border with.
 *
 * `statusTone` returns utility classes for badges; a node tile needs the raw
 * value because it composes the colour into a ring with `color-mix`.
 */
export function statusAccent(status: string): string {
	switch (status) {
		case 'succeeded':
			return 'var(--success)';
		case 'failed':
			return 'var(--destructive)';
		case 'cancelled':
		case 'running':
			return 'var(--primary)';
		case 'waiting':
			return 'var(--violet-500, var(--primary))';
		default:
			return 'var(--muted-foreground)';
	}
}
