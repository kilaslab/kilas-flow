<script lang="ts">
	import { page } from '$app/state';
	import ArrowLeft from '@lucide/svelte/icons/arrow-left';
	import Paperclip from '@lucide/svelte/icons/paperclip';

	import { message } from '$lib/api/http';
	import { createGetExecution } from '$lib/api/generated/executions/executions';
	import { createListNodeTypes } from '$lib/api/generated/nodes/nodes';
	import { createGetWorkflowVersion } from '$lib/api/generated/workflows/workflows';
	import type { Definition, ExecutionResource, WorkflowVersionResource } from '$lib/api/generated/models';
	import { Button } from '$lib/components/ui/button';
	import ExecutionCanvas from '$lib/components/workflow-editor/execution-canvas.svelte';
	import { applyEvents, executionEvents, latestExecutionStatus } from '$lib/workflow-editor/event-stream.svelte';
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

	// The live feed advances what the fetched trace already showed. It never
	// replaces it: a refresh and a live update converge on the same picture.
	const live = executionEvents(() => page.params.id ?? '');

	const runs = $derived(latestNodeRuns(execution.data?.nodeRuns));
	const nodeStatuses = $derived(applyEvents(runs, live.events));
	const liveStatus = $derived(latestExecutionStatus(live.events));
	const status = $derived(liveStatus ?? execution.data?.status ?? 'queued');
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

	$effect(() => {
		// Live events carry status, not payloads. Once the run ends, re-read the
		// durable trace so the inspector shows what was actually persisted.
		if (live.finished) void execution.refetch();
	});

	function asJSON(value: unknown): string {
		if (value === undefined || value === null) return 'null';
		try {
			return JSON.stringify(value, null, 2);
		} catch {
			return String(value);
		}
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
	{:else if execution.isError}
		<div class="max-w-xl rounded-lg border border-destructive/25 bg-destructive/5 p-3">
			<h1 class="font-medium">Execution could not be loaded</h1>
			<p class="mt-0.5 text-xs leading-5 text-muted-foreground">{message(execution.error)}</p>
			<Button class="mt-4" variant="outline" onclick={() => void execution.refetch()}>Try again</Button>
		</div>
	{:else if execution.data}
		<div class="flex flex-wrap items-start justify-between gap-3">
			<div class="min-w-0">
				<div class="flex items-center gap-2">
					<h1 class="text-base font-semibold tracking-tight">Execution</h1>
					<span class={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${statusTone(status)}`}>{statusLabel(status)}</span>
					{#if live.connected}
						<span class="inline-flex items-center gap-1.5 text-xs text-muted-foreground" aria-live="polite">
							<span aria-hidden="true" class="size-1.5 animate-pulse rounded-full bg-primary"></span>
							Live
						</span>
					{/if}
				</div>
				<p class="mt-1 font-mono text-xs text-muted-foreground">{execution.data.id}</p>
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

		{#if execution.data.error}
			<div role="alert" class="rounded-xl border border-destructive/25 bg-destructive/5 p-4">
				<h2 class="text-sm font-medium text-destructive">Execution error</h2>
				<pre class="mt-2 max-h-40 overflow-auto text-xs leading-5 text-destructive">{asJSON(execution.data.error)}</pre>
			</div>
		{/if}

		<div class="grid gap-3 lg:grid-cols-[minmax(0,1fr)_22rem]">
			<div class="h-[26rem] overflow-hidden rounded-lg border border-border lg:h-[calc(100dvh-13rem)] lg:min-h-[26rem]">
				{#if version.isPending}
					<p aria-live="polite" class="grid h-full place-items-center text-sm text-muted-foreground">Loading the graph this execution ran…</p>
				{:else if version.isError || nodeTypes.isError}
					<div class="grid h-full place-items-center p-6 text-center">
						<div class="max-w-sm">
							<p class="text-sm text-muted-foreground">{message(version.isError ? version.error : nodeTypes.error)}</p>
							<Button class="mt-4" variant="outline" onclick={() => { void version.refetch(); void nodeTypes.refetch(); }}>Try again</Button>
						</div>
					</div>
				{:else if version.data && nodeTypes.data}
					<ExecutionCanvas document={version.data.document} definitions={nodeTypes.data} {runs} statuses={nodeStatuses} bind:selectedNodeID />
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
										<pre class="mt-1.5 overflow-x-auto rounded-lg bg-destructive/5 p-3 text-xs leading-5 text-destructive">{asJSON(selectedRun.error)}</pre>
									</div>
								{/if}
								<div>
									<h3 class="text-sm font-medium">Input</h3>
									<pre class="mt-1.5 overflow-x-auto rounded-lg bg-muted p-3 text-xs leading-5">{asJSON(selectedRun.input)}</pre>
								</div>
								<div>
									<h3 class="text-sm font-medium">Output</h3>
									<pre class="mt-1.5 overflow-x-auto rounded-lg bg-muted p-3 text-xs leading-5">{asJSON(selectedRun.output)}</pre>
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
