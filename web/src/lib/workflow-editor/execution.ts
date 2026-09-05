import type { Connection, Definition, ExecutionNodeRunResource, Node } from '$lib/api/generated/models';

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
type OutputItem = { json?: unknown; binary?: unknown };

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
		const definition = definitions.find((candidate) => candidate.type === source.type && candidate.version === source.typeVersion);
		const portIndex = (definition?.outputs ?? []).findIndex((port) => port.Name === connection.source.port);
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
	if (milliseconds === null || milliseconds === undefined || Number.isNaN(milliseconds)) return '—';
	if (milliseconds < 1000) return `${Math.round(milliseconds)} ms`;
	if (milliseconds < 60_000) return `${Math.round(milliseconds / 100) / 10} s`;
	const minutes = Math.floor(milliseconds / 60_000);
	const seconds = Math.round((milliseconds % 60_000) / 1000);
	return `${minutes} m ${seconds} s`;
}

export function formatTimestamp(value: string | undefined): string {
	if (!value) return '—';
	const date = new Date(value);
	return Number.isNaN(date.getTime())
		? '—'
		: date.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' });
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
		case 'skipped':
			return 'bg-muted text-muted-foreground border-border';
		default:
			return 'bg-secondary text-secondary-foreground border-border';
	}
}

/** Human label for a status, including the client-only `skipped`. */
export function statusLabel(status: string): string {
	if (status === 'skipped') return 'Not reached';
	return status.charAt(0).toUpperCase() + status.slice(1);
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
		case 'cancelling':
			return 'var(--warning)';
		case 'running':
			return 'var(--primary)';
		default:
			return 'var(--muted-foreground)';
	}
}
