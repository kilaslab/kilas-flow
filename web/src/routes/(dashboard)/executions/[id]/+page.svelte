<script lang="ts">
	import { untrack } from 'svelte';

	import { page } from '$app/state';
	import ArrowLeft from '@lucide/svelte/icons/arrow-left';
	import Paperclip from '@lucide/svelte/icons/paperclip';

	import { message } from '$lib/api/http';
	import { cancelExecution, createGetExecution } from '$lib/api/generated/executions/executions';
	import { createListNodeTypes } from '$lib/api/generated/nodes/nodes';
	import { createGetWorkflowVersion } from '$lib/api/generated/workflows/workflows';
	import type { Definition, ExecutionResource, WorkflowVersionResource } from '$lib/api/generated/models';
	import { Button } from '$lib/components/ui/button';
	import ExecutionCanvas from '$lib/components/workflow-editor/execution-canvas.svelte';
	import { applyEvents, executionEvents, isTerminalStatus, latestExecutionStatus } from '$lib/workflow-editor/event-stream.svelte';
	import {
		binaryAttachments,
		executionDurationMs,
		formatBytes,
		formatDuration,
		formatTimestamp,
		latestNodeRuns,
		statusLabel,
		statusTone
	} from '$lib/workflow-editor/execution';

	const execution = createGetExecution<ExecutionResource>(() => page.params.id ?? '', () => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected execution response');
				return response.data;
			}
		}
	}));
	const nodeTypes = createListNodeTypes<Definition[]>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected node catalogue response');
				return response.data ?? [];
			}
		}
	}));
	// Replay the revision this execution pinned, not the workflow's current
	// draft: the graph may have changed many times since the run.
	const version = createGetWorkflowVersion<WorkflowVersionResource>(
		() => execution.data?.workflowId ?? '',
		() => execution.data?.workflowVersionId ?? '',
		() => ({
			query: {
				enabled: Boolean(execution.data),
				select: (response) => {
					if (response.status !== 200) throw new Error('Unexpected workflow-revision response');
					return response.data;
				}
			}
		})
	);

	let selectedNodeID = $state<string | null>(null);
	let stopping = $state(false);
	let stopError = $state<string | null>(null);

	async function stop() {
		if (stopping || !execution.data) return;
		stopping = true;
		stopError = null;
		try {
			const response = await cancelExecution(execution.data.id);
			if (response.status !== 202 && response.status !== 200) throw new Error('Unexpected cancel response');
			await execution.refetch();
		} catch (error) {
			stopError = message(error);
		} finally {
			stopping = false;
		}
	}

	// The live feed advances what the fetched trace already showed. It never
	// replaces it: a refresh and a live update converge on the same picture.
	// Nothing is streamed before the trace arrives, and nothing at all for a
	// trace that is already terminal — opening on mount put a request on the
	// wire for every finished execution, whose stream was closed a moment later.
	const live = executionEvents(
		() => page.params.id ?? '',
		() => Boolean(execution.data) && !isTerminalStatus(execution.data?.status)
	);

	const runs = $derived(latestNodeRuns(execution.data?.nodeRuns));
	const nodeStatuses = $derived(applyEvents(runs, live.events));
	const liveStatus = $derived(latestExecutionStatus(live.events));
	const status = $derived(liveStatus ?? execution.data?.status ?? 'queued');
	const stoppable = $derived(status === 'running' || status === 'queued' || status === 'waiting' || status === 'cancelling');
	const selectedRun = $derived(selectedNodeID ? (runs.get(selectedNodeID) ?? null) : null);
	// Payloads never leave the server's binary store, so the inspector lists
	// what an attachment *is* — name, type, size — and never tries to render
	// one. There is nothing to render: the API serves the reference only.
	const attachments = $derived(binaryAttachments(selectedRun?.output));
	const selectedNode = $derived(
		selectedNodeID ? ((version.data?.document.nodes ?? []).find((node) => node.id === selectedNodeID) ?? null) : null
	);
	const duration = $derived(execution.data ? executionDurationMs(execution.data) : null);
	const selectedStatus = $derived(
		(selectedNodeID ? nodeStatuses.get(selectedNodeID) : undefined) ?? selectedRun?.status ?? 'skipped'
	);

	/**
	 * Re-reads the durable trace once, the moment the live feed reports the run
	 * ended.
	 *
	 * Once, because the effect used to read the whole query object while it
	 * refetched, so every notify re-triggered it: a finished execution page sat
	 * in a 200/abort loop at ~900 requests a second, and the constant
	 * re-rendering is what kept a node click from ever opening the inspector.
	 * `untrack` keeps the call out of the dependency set, and the flag makes
	 * the false→true edge the only thing that fires it.
	 */
	let refetchedAfterFinish = false;
	$effect(() => {
		if (!live.finished) {
			refetchedAfterFinish = false;
			return;
		}
		if (refetchedAfterFinish) return;
		refetchedAfterFinish = true;
		untrack(() => void execution.refetch());
	});

	function asJSON(value: unknown): string {
		if (value === undefined || value === null) return 'null';
		try {
			return JSON.stringify(value, null, 2);
		} catch {
			return String(value);
		}
	}

	/** A wrapped error title: the message first, never a sideways-scrolling blob. */
	function errorTitle(error: unknown): string {
		if (typeof error === 'string') return error;
		if (error !== null && typeof error === 'object') {
			const record = error as Record<string, unknown>;
			if (typeof record.message === 'string' && record.message) return record.message;
			if (typeof record.code === 'string' && record.code) return record.code;
		}
		return 'The run failed.';
	}

	/** The rest of the error record, when it carries more than the title. */
	function errorDetail(error: unknown): string | null {
		if (error === null || typeof error !== 'object' || Array.isArray(error)) return null;
		const record = error as Record<string, unknown>;
		const extras = Object.entries(record).filter(([key, value]) => key !== 'message' && typeof value !== 'object' && value !== undefined && value !== null && String(value) !== '');
		if (extras.length === 0) return null;
		return extras.map(([key, value]) => `${key}: ${String(value)}`).join(' · ');
	}

	let copied = $state<string | null>(null);

	async function copyText(text: string, key: string) {
		try {
			await navigator.clipboard.writeText(text);
			copied = key;
		} catch {
			copied = null;
		}
	}

	/** Payloads over this size are hidden behind an explicit opt-in: dumping
	 * megabytes of JSON into <pre> blocks is what made realistic runs hang
	 * the tab. The count still shows, so nothing reads as missing. */
	const LARGE_PAYLOAD_CHARS = 200_000;

	function payloadSize(value: unknown): number {
		return asJSON(value).length;
	}

	let expandedPayloads = $state<Set<string>>(new Set());

	function payloadExpanded(key: string): boolean {
		return expandedPayloads.has(key);
	}

	function expandPayload(key: string) {
		expandedPayloads = new Set(expandedPayloads).add(key);
	}
</script>

<svelte:head>
	<title>{execution.data?.id ?? 'Execution'} · KilasFlow</title>
</svelte:head>

<section class="mx-auto flex w-full max-w-7xl flex-col gap-3">
	<div>
		<Button href="/executions" variant="ghost" size="sm"><ArrowLeft aria-hidden="true" />All executions</Button>
	</div>

	{#if execution.isPending}
		<p aria-live="polite" class="text-sm text-muted-foreground">Loading execution…</p>
	{:else if execution.isError && !execution.data}
		<div class="max-w-xl rounded-lg border border-destructive/25 bg-destructive/5 p-3">
			<h1 class="font-medium">Execution could not be loaded</h1>
			<p class="mt-0.5 text-xs leading-5 text-muted-foreground">{message(execution.error)}</p>
			<Button class="mt-4" variant="outline" onclick={() => void execution.refetch()}>Try again</Button>
		</div>
	{:else if execution.data}
		<!-- A refetch that fails once the trace is already on screen is a
		     refresh failure, not a page failure: replacing the inspector with an
		     error card throws away the only view of the run the user has. -->
		{#if execution.isError}
			<div role="alert" class="flex flex-wrap items-center gap-2 rounded-lg border border-destructive/25 bg-destructive/5 p-3">
				<p class="min-w-0 flex-1 text-xs leading-5 text-destructive">This view may be out of date — the last refresh failed: {message(execution.error)}</p>
				<Button variant="outline" size="sm" onclick={() => void execution.refetch()}>Refresh</Button>
			</div>
		{/if}
		<div class="flex flex-wrap items-start justify-between gap-3">
			<div class="min-w-0">
				<div class="flex flex-wrap items-center gap-2">
					<h1 class="text-base font-semibold tracking-tight">Execution</h1>
					<span class={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${statusTone(status)}`}>{statusLabel(status)}</span>
					{#if live.connected && !live.finished}
						<span class="inline-flex items-center gap-1.5 text-xs text-muted-foreground" aria-live="polite">
							<span aria-hidden="true" class="size-1.5 animate-pulse rounded-full bg-primary"></span>
							Live
						</span>
					{/if}
					{#if stoppable}
						<Button variant="outline" size="sm" onclick={() => void stop()} disabled={stopping}>
							{stopping ? 'Stopping…' : 'Stop'}
						</Button>
					{/if}
				</div>
				<p class="mt-1 font-mono text-xs text-muted-foreground">{execution.data.id}</p>
				<p class="mt-1 text-xs">
					<a class="rounded text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2" href={`/app/workflows/${execution.data.workflowId}`}>Open workflow</a>
					<span class="text-muted-foreground"> · {version.data?.document.name ?? execution.data.workflowId}</span>
				</p>
				{#if stopError}<p role="alert" class="mt-1 text-xs text-destructive">Stop failed: {stopError}</p>{/if}
			</div>
			<dl class="grid grid-cols-3 gap-x-6 gap-y-1 text-[0.8125rem]">
				<div>
					<dt class="text-xs text-muted-foreground">Started</dt>
					<dd class="mt-0.5">{formatTimestamp(execution.data.startedAt)}</dd>
				</div>
				<div>
					<dt class="text-xs text-muted-foreground">Duration</dt>
					<dd class="mt-0.5 tabular-nums">{formatDuration(duration)}</dd>
				</div>
				<div>
					<dt class="text-xs text-muted-foreground">Trigger</dt>
					<dd class="mt-0.5">{statusLabel(execution.data.trigger)}</dd>
				</div>
			</dl>
		</div>

		{#if status === 'waiting'}
			{#if execution.data.approvalUrl}
				<div class="rounded-xl border border-violet-500/30 bg-violet-500/5 p-4">
					<h2 class="text-sm font-medium">Waiting for approval</h2>
					<p class="mt-0.5 text-xs leading-5 text-muted-foreground">
						This run is suspended and holds no worker. Open the approval page to record a decision, or stop it below.
					</p>
					<div class="mt-3 flex flex-wrap gap-2">
						<Button variant="outline" href={execution.data.approvalUrl}>Open approval page</Button>
						{#if stoppable}
							<Button variant="outline" onclick={() => void stop()} disabled={stopping}>
								{stopping ? 'Stopping…' : 'Stop waiting run'}
							</Button>
						{/if}
					</div>
				</div>
			{:else}
				<div class="rounded-xl border border-border bg-muted/40 p-4">
					<h2 class="text-sm font-medium">Waiting — resumes on its own</h2>
					<p class="mt-0.5 text-xs leading-5 text-muted-foreground">
						This is a timer or webhook wait, not an approval: it resumes when its time comes or its webhook arrives. There is no decision to record.
						{#if stoppable}Stopping it cancels the wait.{/if}
					</p>
					{#if stoppable}
						<Button class="mt-3" variant="outline" onclick={() => void stop()} disabled={stopping}>
							{stopping ? 'Stopping…' : 'Stop waiting run'}
						</Button>
					{/if}
				</div>
			{/if}
		{/if}

		{#if execution.data.error}
			<div role="alert" class="rounded-xl border border-destructive/25 bg-destructive/5 p-4">
				<h2 class="text-sm font-medium text-destructive">Execution error</h2>
				<p class="mt-1 text-xs font-medium break-words text-destructive">{errorTitle(execution.data.error)}</p>
				{#if errorDetail(execution.data.error)}
					<p class="mt-1 text-xs leading-5 break-words text-muted-foreground">{errorDetail(execution.data.error)}</p>
				{/if}
				<details class="mt-2">
					<summary class="cursor-pointer text-xs text-muted-foreground underline-offset-4 hover:underline">Full error JSON</summary>
					<pre class="mt-1.5 max-h-40 overflow-auto rounded-lg bg-background p-3 font-mono text-[0.6875rem] leading-5 break-all whitespace-pre-wrap text-muted-foreground">{asJSON(execution.data.error)}</pre>
				</details>
				<Button class="mt-2" variant="outline" size="sm" onclick={() => copyText(asJSON(execution.data.error), 'error')}>Copy error</Button>
				{#if copied === 'error'}<span class="ml-2 text-xs text-muted-foreground" role="status">Copied.</span>{/if}
			</div>
		{/if}

		<div class="grid gap-3 lg:grid-cols-[minmax(0,1fr)_22rem]">
			<div class="h-[26rem] overflow-hidden rounded-lg border border-border lg:h-[calc(100dvh-13rem)] lg:min-h-[26rem]">
				{#if version.isPending}
					<p aria-live="polite" class="grid h-full place-items-center text-sm text-muted-foreground">Loading the graph this execution ran…</p>
				{:else if version.data && nodeTypes.data}
					<ExecutionCanvas document={version.data.document} definitions={nodeTypes.data} {runs} statuses={nodeStatuses} bind:selectedNodeID />
				{:else if version.isError || nodeTypes.isError}
					<div class="grid h-full place-items-center p-6 text-center">
						<div class="max-w-sm">
							<p class="text-sm text-muted-foreground">{message(version.isError ? version.error : nodeTypes.error)}</p>
							<Button class="mt-4" variant="outline" onclick={() => { void version.refetch(); void nodeTypes.refetch(); }}>Try again</Button>
						</div>
					</div>
				{:else}
					<p aria-live="polite" class="grid h-full place-items-center text-sm text-muted-foreground">Loading the graph this execution ran…</p>
				{/if}
			</div>

			<aside aria-label="Node data" class="min-h-0 overflow-hidden rounded-lg border border-border">
				{#if !selectedNodeID}
					<p class="grid h-full place-items-center p-6 text-center text-sm leading-6 text-muted-foreground">Select a node on the canvas to inspect the data it received and produced.</p>
				{:else}
					<div class="flex h-full flex-col">
						<div class="border-b border-border px-4 py-3">
							<p class="text-xs font-medium text-muted-foreground">Node</p>
							<h2 class="mt-0.5 truncate text-base font-semibold">{selectedNode?.name ?? selectedNodeID}</h2>
							<p class="mt-2">
								<span class={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${statusTone(selectedStatus)}`}>{statusLabel(selectedStatus)}</span>
								{#if selectedRun && selectedRun.attempt > 1}
									<span class="ml-2 text-xs text-muted-foreground">Attempt {selectedRun.attempt}</span>
								{/if}
							</p>
						</div>
						<div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
							{#if !selectedRun}
								<p class="text-sm leading-6 text-muted-foreground">This node was never reached, so the execution recorded no data for it.</p>
							{:else}
								{#if selectedRun.error}
									<div>
										<h3 class="text-sm font-medium text-destructive">Error</h3>
										<p class="mt-1 text-xs font-medium break-words text-destructive">{errorTitle(selectedRun.error)}</p>
										{#if errorDetail(selectedRun.error)}
											<p class="mt-1 text-xs leading-5 break-words text-muted-foreground">{errorDetail(selectedRun.error)}</p>
										{/if}
									<details class="mt-1.5">
										<summary class="cursor-pointer text-xs text-muted-foreground underline-offset-4 hover:underline">Full error JSON</summary>
										<pre class="mt-1.5 overflow-x-auto rounded-lg bg-destructive/5 p-3 font-mono text-[0.6875rem] leading-5 break-all whitespace-pre-wrap text-destructive">{asJSON(selectedRun.error)}</pre>
									</details>
								</div>
							{/if}
							<div>
								<h3 class="text-sm font-medium">Input {selectedNodeID ? `(${payloadSize(selectedRun.input).toLocaleString()} chars)` : ''}</h3>
								{#if payloadSize(selectedRun.input) > LARGE_PAYLOAD_CHARS && !payloadExpanded(`input:${selectedNodeID}`)}
									<div class="mt-1.5 rounded-lg border border-border bg-muted/40 p-3">
										<p class="text-xs leading-5 text-muted-foreground">This payload is {payloadSize(selectedRun.input).toLocaleString()} characters — too large to render without hanging the tab.</p>
										<Button class="mt-2" variant="outline" size="sm" onclick={() => expandPayload(`input:${selectedNodeID}`)}>Show anyway</Button>
									</div>
								{:else}
									<pre class="mt-1.5 max-h-96 overflow-auto rounded-lg bg-muted p-3 font-mono text-[0.6875rem] leading-5 break-all whitespace-pre-wrap">{asJSON(selectedRun.input)}</pre>
								{/if}
							</div>
							<div>
								<h3 class="text-sm font-medium">Output {selectedNodeID ? `(${payloadSize(selectedRun.output).toLocaleString()} chars)` : ''}</h3>
								{#if payloadSize(selectedRun.output) > LARGE_PAYLOAD_CHARS && !payloadExpanded(`output:${selectedNodeID}`)}
									<div class="mt-1.5 rounded-lg border border-border bg-muted/40 p-3">
										<p class="text-xs leading-5 text-muted-foreground">This payload is {payloadSize(selectedRun.output).toLocaleString()} characters — too large to render without hanging the tab.</p>
										<Button class="mt-2" variant="outline" size="sm" onclick={() => expandPayload(`output:${selectedNodeID}`)}>Show anyway</Button>
									</div>
								{:else}
									<pre class="mt-1.5 max-h-96 overflow-auto rounded-lg bg-muted p-3 font-mono text-[0.6875rem] leading-5 break-all whitespace-pre-wrap">{asJSON(selectedRun.output)}</pre>
								{/if}
							</div>
								{#if attachments.length > 0}
									<div>
										<h3 class="text-sm font-medium">Attachments</h3>
										<ul class="mt-1.5 space-y-1.5">
											{#each attachments as attachment (`${attachment.port}:${attachment.item}:${attachment.property}`)}
												<li class="flex items-start gap-2 rounded-lg border border-border px-3 py-2">
													<Paperclip class="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
													<div class="min-w-0">
														<p class="truncate text-xs font-medium">{attachment.reference.fileName || attachment.property}</p>
														<p class="mt-0.5 text-xs text-muted-foreground">
															{attachment.reference.mediaType || 'unknown type'} · {formatBytes(attachment.reference.size)} · item {attachment.item + 1}
														</p>
													</div>
												</li>
											{/each}
										</ul>
										<p class="mt-1.5 text-xs text-muted-foreground">Contents are held by the server and are not shown here.</p>
									</div>
								{/if}
								<p class="text-xs text-muted-foreground">
									{formatTimestamp(selectedRun.startedAt)} · {formatDuration(executionDurationMs(selectedRun))}
								</p>
							{/if}
						</div>
					</div>
				{/if}
			</aside>
		</div>
	{/if}
</section>
