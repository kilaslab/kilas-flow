<script lang="ts">
	import Activity from '@lucide/svelte/icons/activity';
	import RefreshCw from '@lucide/svelte/icons/refresh-cw';

	import { listExecutions } from '$lib/api/generated/executions/executions';
	import { createListWorkflows } from '$lib/api/generated/workflows/workflows';
	import type { ExecutionSummary, WorkflowSummary } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Table from '$lib/components/ui/table';
	import {
		appendPage,
		canLoadMore,
		emptyPage,
		readPage,
		type CursorPage
	} from '$lib/dashboard/cursor-page';
	import { failedBesideRows } from '$lib/dashboard/list-state';
	import { RequestGuard } from '$lib/dashboard/request-guard';
	import { formatDuration, formatTimestamp, statusLabel, statusTone } from '$lib/workflow-editor/execution';

	const STATUSES = ['queued', 'running', 'cancelling', 'succeeded', 'failed', 'cancelled'];

	const workflows = createListWorkflows<WorkflowSummary[]>(() => ({
		query: {
			select: (response) => (response.status === 200 ? (response.data ?? []) : [])
		}
	}));

	let status = $state('');
	let workflowID = $state('');
	let page = $state<CursorPage<ExecutionSummary>>(emptyPage());
	let loading = $state(true);
	let loadingMore = $state(false);
	// The rejection itself rather than its text: message() is applied once, at
	// the shell, so this page holds no opinion about how a failure is worded.
	let failure = $state<unknown>(null);

	// This list is the only one that pages a cursor and filters on the server,
	// so it drives its own request state instead of TanStack Query.
	const guard = new RequestGuard();

	const workflowNames = $derived(new Map((workflows.data ?? []).map((workflow) => [workflow.id, workflow.name])));

	// A failure with rows behind it came from "Load more" — load() empties the
	// page before it records one, so a first-load failure never reaches here.
	const pagingFailure = $derived(
		failedBesideRows({ loading, failed: failure !== null, count: page.items.length })
	);

	$effect(() => {
		// Re-read whenever a filter changes; the first run also loads the page.
		void load(status, workflowID);
	});

	async function load(activeStatus: string, activeWorkflow: string) {
		const token = guard.start();
		loading = true;
		failure = null;
		try {
			const response = await listExecutions({
				...(activeStatus ? { status: [activeStatus] } : {}),
				...(activeWorkflow ? { workflowId: activeWorkflow } : {})
			});
			if (response.status !== 200) throw new Error('Unexpected execution-list response');
			if (!guard.holds(token)) return;
			page = readPage(response.data);
		} catch (cause) {
			if (!guard.holds(token)) return;
			failure = cause;
			page = emptyPage();
		} finally {
			if (guard.holds(token)) loading = false;
		}
	}

	async function loadMore() {
		if (!canLoadMore(page) || loadingMore) return;
		// Joins the request already in flight rather than starting a new one:
		// starting would supersede that request, which would then discard its
		// own answer and leave the page stuck on its skeleton.
		const token = guard.current;
		loadingMore = true;
		// Clearing here rather than on success is what makes a second press of
		// the button read as a retry: the notice goes away while the attempt
		// it describes is running, and comes back only if this one fails too.
		failure = null;
		try {
			const response = await listExecutions({
				cursor: page.nextCursor,
				...(status ? { status: [status] } : {}),
				...(workflowID ? { workflowId: workflowID } : {})
			});
			if (response.status !== 200) throw new Error('Unexpected execution-list response');
			if (!guard.holds(token)) return;
			page = appendPage(page, response.data);
		} catch (cause) {
			// `page` is deliberately left alone: appendPage is the only thing
			// that moves the cursor, and it only runs on success, so a retry
			// asks for the page that failed rather than the one after it.
			// Resetting the rows here would lose every page already loaded.
			if (guard.holds(token)) failure = cause;
		} finally {
			loadingMore = false;
		}
	}
</script>

<svelte:head>
	<title>Executions · KilasFlow</title>
</svelte:head>

<section class="mx-auto w-full max-w-5xl">
	<div class="flex items-center justify-between gap-4">
		<div class="max-w-xl">
			<h1 class="text-base font-semibold tracking-tight">Executions</h1>
			<p class="text-xs text-muted-foreground">Every run your workspace has recorded, newest first. Open one to replay its graph.</p>
		</div>
		<Button variant="outline" onclick={() => void load(status, workflowID)} disabled={loading}>
			<RefreshCw aria-hidden="true" />
			Refresh
		</Button>
	</div>

	<div class="mt-6 flex flex-wrap gap-3">
		<div class="grid gap-1">
			<label for="execution-status" class="text-xs font-medium text-muted-foreground">Status</label>
			<select id="execution-status" bind:value={status} class="h-7 min-w-36 rounded-md border border-input bg-background px-1.5 text-xs">
				<option value="">All statuses</option>
				{#each STATUSES as option (option)}
					<option value={option}>{statusLabel(option)}</option>
				{/each}
			</select>
		</div>
		<div class="grid gap-1">
			<label for="execution-workflow" class="text-xs font-medium text-muted-foreground">Workflow</label>
			<select id="execution-workflow" bind:value={workflowID} class="h-7 min-w-44 rounded-md border border-input bg-background px-1.5 text-xs">
				<option value="">All workflows</option>
				{#each workflows.data ?? [] as workflow (workflow.id)}
					<option value={workflow.id}>{workflow.name}</option>
				{/each}
			</select>
		</div>
	</div>

	<div class="mt-6">
		<ListStates
			label="Executions"
			{loading}
			failed={failure !== null}
			error={failure}
			count={page.items.length}
			onRetry={() => void load(status, workflowID)}
			onRetryMore={() => void loadMore()}
			emptyIcon={Activity}
			emptyTitle="No executions yet"
			emptyBody="Run a workflow from its editor and its history will appear here."
		>
			<!--
				overflow-hidden, not overflow-x-auto: the primitive owns the scroll
				container now, and this element only draws the card around it. The
				two cannot be the same element any more, and without the clip a
				hovered row paints over the rounded corners.
			-->
			<div class="overflow-hidden rounded-xl border border-border bg-card">
				<Table.Root class="min-w-[44rem]">
					<Table.Caption class="sr-only">Workflow executions, newest first</Table.Caption>
					<!--
						The header's type scale is set on the row's cells rather than on
						each <th>: the primitive gives a th its own text-foreground, and
						a colour inherited from thead would lose to it.
					-->
					<Table.Header class="[&_th]:h-7 [&_th]:px-3 [&_th]:text-xs [&_th]:uppercase [&_th]:tracking-wide [&_th]:text-muted-foreground">
						<Table.Row>
							<Table.Head scope="col">Status</Table.Head>
							<Table.Head scope="col">Workflow</Table.Head>
							<Table.Head scope="col">Started</Table.Head>
							<Table.Head scope="col">Duration</Table.Head>
							<Table.Head scope="col">Trigger</Table.Head>
							<Table.Head scope="col">Execution</Table.Head>
						</Table.Row>
					</Table.Header>
					<Table.Body>
						{#each page.items as item (item.id)}
							<Table.Row>
								<Table.Cell class="px-3 py-2">
									<span class={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${statusTone(item.status)}`}>{statusLabel(item.status)}</span>
								</Table.Cell>
								<Table.Cell class="max-w-56 truncate px-3 py-2">{workflowNames.get(item.workflowId) ?? item.workflowId}</Table.Cell>
								<Table.Cell class="px-3 py-2 text-muted-foreground">{formatTimestamp(item.startedAt)}</Table.Cell>
								<Table.Cell class="px-3 py-2 tabular-nums text-muted-foreground">{formatDuration(item.durationMs ?? null)}</Table.Cell>
								<Table.Cell class="px-3 py-2 text-muted-foreground">{statusLabel(item.trigger)}</Table.Cell>
								<Table.Cell class="px-3 py-2">
									<a href={`/executions/${item.id}`} class="rounded font-medium text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2">
										<span class="font-mono text-xs">{item.id}</span>
									</a>
								</Table.Cell>
							</Table.Row>
						{/each}
					</Table.Body>
				</Table.Root>
			</div>
			<!--
				Hidden while the paging notice is up, because that notice
				carries its own "Try again" for the same request — two buttons
				a thumb's width apart doing the same thing is a worse answer
				than one.
			-->
			{#if canLoadMore(page) && !pagingFailure}
				<div class="mt-4 flex justify-center">
					<Button variant="outline" onclick={() => void loadMore()} disabled={loadingMore}>{loadingMore ? 'Loading…' : 'Load more'}</Button>
				</div>
			{/if}
		</ListStates>
	</div>
</section>
