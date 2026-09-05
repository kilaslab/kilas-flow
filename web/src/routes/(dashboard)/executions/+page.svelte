<script lang="ts">
	import Activity from '@lucide/svelte/icons/activity';
	import RefreshCw from '@lucide/svelte/icons/refresh-cw';

	import { ApiError } from '$lib/api/http';
	import { listExecutions } from '$lib/api/generated/executions/executions';
	import { createListWorkflows } from '$lib/api/generated/workflows/workflows';
	import type { ExecutionSummary, WorkflowSummary } from '$lib/api/generated/models';
	import { Button } from '$lib/components/ui/button';
	import { formatDuration, formatTimestamp, statusLabel, statusTone } from '$lib/workflow-editor/execution';

	const STATUSES = ['queued', 'running', 'cancelling', 'succeeded', 'failed', 'cancelled'];

	const workflows = createListWorkflows<WorkflowSummary[]>(() => ({
		query: {
			select: (response) => (response.status === 200 ? (response.data ?? []) : [])
		}
	}));

	let status = $state('');
	let workflowID = $state('');
	let items = $state<ExecutionSummary[]>([]);
	let nextCursor = $state('');
	let loading = $state(true);
	let loadingMore = $state(false);
	let error = $state<string | null>(null);
	// Guards against a slower earlier request overwriting a newer filter's result.
	let requestToken = 0;

	const workflowNames = $derived(new Map((workflows.data ?? []).map((workflow) => [workflow.id, workflow.name])));

	$effect(() => {
		// Re-read whenever a filter changes; the first run also loads the page.
		void load(status, workflowID);
	});

	function message(error: unknown): string {
		if (error instanceof ApiError) return `${error.status} — ${error.message}`;
		return error instanceof Error ? error.message : 'The request could not be completed.';
	}

	async function load(activeStatus: string, activeWorkflow: string) {
		const token = ++requestToken;
		loading = true;
		error = null;
		try {
			const response = await listExecutions({
				...(activeStatus ? { status: [activeStatus] } : {}),
				...(activeWorkflow ? { workflowId: activeWorkflow } : {})
			});
			if (response.status !== 200) throw new Error('Unexpected execution-list response');
			if (token !== requestToken) return;
			items = response.data.items ?? [];
			nextCursor = response.data.nextCursor ?? '';
		} catch (cause) {
			if (token !== requestToken) return;
			error = message(cause);
			items = [];
			nextCursor = '';
		} finally {
			if (token === requestToken) loading = false;
		}
	}

	async function loadMore() {
		if (!nextCursor || loadingMore) return;
		const token = requestToken;
		loadingMore = true;
		try {
			const response = await listExecutions({
				cursor: nextCursor,
				...(status ? { status: [status] } : {}),
				...(workflowID ? { workflowId: workflowID } : {})
			});
			if (response.status !== 200) throw new Error('Unexpected execution-list response');
			if (token !== requestToken) return;
			items = [...items, ...(response.data.items ?? [])];
			nextCursor = response.data.nextCursor ?? '';
		} catch (cause) {
			if (token === requestToken) error = message(cause);
		} finally {
			loadingMore = false;
		}
	}
</script>

<svelte:head>
	<title>Executions · KilasFlow</title>
</svelte:head>

<section class="mx-auto w-full max-w-6xl">
	<div class="flex flex-col gap-5 sm:flex-row sm:items-end sm:justify-between">
		<div class="max-w-xl">
			<h1 class="text-balance text-2xl font-semibold tracking-tight sm:text-3xl">Executions</h1>
			<p class="mt-2 text-pretty text-sm leading-6 text-muted-foreground">Every run your workspace has recorded, newest first. Open one to replay its graph.</p>
		</div>
		<Button variant="outline" onclick={() => void load(status, workflowID)} disabled={loading}>
			<RefreshCw aria-hidden="true" />
			Refresh
		</Button>
	</div>

	<div class="mt-6 flex flex-wrap gap-3">
		<div class="grid gap-1.5">
			<label for="execution-status" class="text-xs font-medium text-muted-foreground">Status</label>
			<select id="execution-status" bind:value={status} class="h-9 min-w-40 rounded-lg border border-input bg-background px-2 text-sm">
				<option value="">All statuses</option>
				{#each STATUSES as option (option)}
					<option value={option}>{statusLabel(option)}</option>
				{/each}
			</select>
		</div>
		<div class="grid gap-1.5">
			<label for="execution-workflow" class="text-xs font-medium text-muted-foreground">Workflow</label>
			<select id="execution-workflow" bind:value={workflowID} class="h-9 min-w-52 rounded-lg border border-input bg-background px-2 text-sm">
				<option value="">All workflows</option>
				{#each workflows.data ?? [] as workflow (workflow.id)}
					<option value={workflow.id}>{workflow.name}</option>
				{/each}
			</select>
		</div>
	</div>

	<div class="mt-6">
		{#if loading}
			<div aria-live="polite" class="grid gap-3">
				<p class="text-sm text-muted-foreground">Loading executions…</p>
				{#each Array(3) as _}
					<div class="h-16 animate-pulse rounded-xl bg-muted" aria-hidden="true"></div>
				{/each}
			</div>
		{:else if error}
			<div class="max-w-xl rounded-xl border border-destructive/25 bg-destructive/5 p-5">
				<h2 class="font-medium">Executions could not be loaded</h2>
				<p class="mt-1 text-sm leading-6 text-muted-foreground">{error}</p>
				<Button class="mt-4" variant="outline" onclick={() => void load(status, workflowID)}>
					<RefreshCw aria-hidden="true" />
					Try again
				</Button>
			</div>
		{:else if items.length === 0}
			<div class="grid min-h-64 place-items-center rounded-2xl border border-dashed border-border bg-card px-6 py-12 text-center">
				<div class="max-w-sm">
					<div aria-hidden="true" class="mx-auto grid size-11 place-items-center rounded-xl bg-accent text-accent-foreground"><Activity class="size-5" /></div>
					<h2 class="mt-5 text-lg font-semibold tracking-tight">No executions yet</h2>
					<p class="mt-2 text-sm leading-6 text-muted-foreground">Run a workflow from its editor and its history will appear here.</p>
				</div>
			</div>
		{:else}
			<div class="overflow-x-auto rounded-xl border border-border bg-card">
				<table class="w-full min-w-[44rem] text-sm">
					<caption class="sr-only">Workflow executions, newest first</caption>
					<thead class="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
						<tr>
							<th scope="col" class="px-4 py-2.5 font-medium">Status</th>
							<th scope="col" class="px-4 py-2.5 font-medium">Workflow</th>
							<th scope="col" class="px-4 py-2.5 font-medium">Started</th>
							<th scope="col" class="px-4 py-2.5 font-medium">Duration</th>
							<th scope="col" class="px-4 py-2.5 font-medium">Trigger</th>
							<th scope="col" class="px-4 py-2.5 font-medium">Execution</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-border">
						{#each items as item (item.id)}
							<tr class="transition-colors hover:bg-muted/50">
								<td class="px-4 py-3">
									<span class={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${statusTone(item.status)}`}>{statusLabel(item.status)}</span>
								</td>
								<td class="max-w-56 truncate px-4 py-3">{workflowNames.get(item.workflowId) ?? item.workflowId}</td>
								<td class="whitespace-nowrap px-4 py-3 text-muted-foreground">{formatTimestamp(item.startedAt)}</td>
								<td class="whitespace-nowrap px-4 py-3 tabular-nums text-muted-foreground">{formatDuration(item.durationMs ?? null)}</td>
								<td class="px-4 py-3 text-muted-foreground">{statusLabel(item.trigger)}</td>
								<td class="px-4 py-3">
									<a href={`/executions/${item.id}`} class="rounded font-medium text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2">
										<span class="font-mono text-xs">{item.id}</span>
									</a>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			{#if nextCursor}
				<div class="mt-4 flex justify-center">
					<Button variant="outline" onclick={() => void loadMore()} disabled={loadingMore}>{loadingMore ? 'Loading…' : 'Load more'}</Button>
				</div>
			{/if}
		{/if}
	</div>
</section>
