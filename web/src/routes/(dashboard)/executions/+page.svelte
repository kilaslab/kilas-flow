<script lang="ts">
	import { goto } from '$app/navigation';
	import { page as route } from '$app/state';
	import { onMount } from 'svelte';
	import Activity from '@lucide/svelte/icons/activity';
	import RefreshCw from '@lucide/svelte/icons/refresh-cw';
	import Square from '@lucide/svelte/icons/square';

	import { message } from '$lib/api/http';
	import { cancelExecution, listExecutions } from '$lib/api/generated/executions/executions';
	import { listWorkflows } from '$lib/api/generated/workflows/workflows';
	import type { ExecutionSummary, WorkflowSummary } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Table from '$lib/components/ui/table';
	import {
		appendPageIfCurrent,
		canLoadMore,
		DRAIN_PAGE_LIMIT,
		drainPages,
		emptyPage,
		headerCursor,
		mergeHead,
		readPage,
		type CursorPage
	} from '$lib/dashboard/cursor-page';
	import {
		EXECUTION_STATUSES,
		HEAD_POLL_INTERVAL_MS,
		buildExecutionSearch,
		buildListExecutionsParams,
		executionListSummary,
		filterWorkflowOptions,
		hasActiveFilters,
		isDeletedWorkflow,
		isStoppableStatus,
		parseExecutionFilters,
		shouldPollHead,
		type HeadPollSignals
	} from '$lib/dashboard/execution-list';
	import { failedBesideRows } from '$lib/dashboard/list-state';
	import { RequestGuard } from '$lib/dashboard/request-guard';
	import { formatDuration, formatTimestamp, statusLabel, statusTone } from '$lib/workflow-editor/execution';

	// Names, the filter's options and the "deleted" badge all come from the
	// workflow listing, which the server pages: reading one page would put every
	// workflow past it beyond the filter's reach and label its executions
	// "deleted" simply because the page could not see them.
	let workflowRows = $state<WorkflowSummary[]>([]);
	let workflowsLoading = $state(true);
	let workflowsFailed = $state(false);
	const workflowGuard = new RequestGuard();

	/** One page of the workflow listing, read only for its names and ids. */
	async function fetchWorkflowPage(cursor: string): Promise<CursorPage<WorkflowSummary>> {
		const response = await listWorkflows({ limit: DRAIN_PAGE_LIMIT, cursor: cursor || undefined });
		if (response.status !== 200) throw new Error('Unexpected workflow-list response');
		return readPage({ items: response.data, nextCursor: headerCursor(response.headers) });
	}

	async function loadWorkflowNames() {
		const token = workflowGuard.start();
		workflowsFailed = false;
		try {
			const items = await drainPages(fetchWorkflowPage);
			if (!workflowGuard.holds(token)) return;
			workflowRows = items;
		} catch {
			// A failure here leaves the ids as the only labels and no row
			// marked deleted: an unreadable name is not a deleted workflow.
			if (!workflowGuard.holds(token)) return;
			workflowsFailed = true;
		} finally {
			if (workflowGuard.holds(token)) workflowsLoading = false;
		}
	}

	onMount(() => {
		// The tab can already be in the background when this page boots, and
		// `tabHidden` starts false because it must be a value the poll effect
		// can react to: seeding it from the real document here is what keeps a
		// hidden tab from arming a poll nothing would ever re-arm.
		tabHidden = document.hidden;
		void loadWorkflowNames();
	});

	// Filters live in the URL (?status=&workflowId=) so a view survives
	// reload and can be linked. `replaceState` on change keeps the back
	// button for navigation rather than for every keystroke of a filter.
	// The editor's "Open executions" links here with ?workflowId=<id>, which
	// is why the first paint reads the URL before it loads anything.
	let status = $state(parseExecutionFilters(route.url.search).status);
	let workflowID = $state(parseExecutionFilters(route.url.search).workflowId);
	let workflowSearch = $state('');
	let autoRefresh = $state(true);
	// Whether the tab is in the background, as reactive state: a poll effect
	// that reads document.hidden directly never re-runs when the tab comes
	// back, which is how polling used to stop for good after one hide. Seeded
	// in onMount and kept honest by the visibilitychange handler in the markup.
	let tabHidden = $state(false);
	// Bumped after every poll. A poll that finds nothing new changes no other
	// reactive value the poll effect reads, so without this the effect would
	// arm exactly one timer and then stop.
	let pollTurn = $state(0);
	let page = $state<CursorPage<ExecutionSummary>>(emptyPage());
	let loading = $state(true);
	let loadingMore = $state(false);
	// The rejection itself rather than its text: message() is applied once, at
	// the shell, so this page holds no opinion about how a failure is worded.
	let failure = $state<unknown>(null);
	let stoppingID = $state<string | null>(null);
	let stopError = $state<string | null>(null);

	// This list reads lazily by cursor and filters on the server, so it drives
	// its own request state instead of TanStack Query. It pages rather than
	// draining because it grows with traffic (execution.retention keeps every
	// run by default) and the server filters the whole history; the
	// tenant-bounded lists drain every page instead (drainPages in
	// cursor-page.ts has the reasoning).
	const guard = new RequestGuard();

	const workflowNames = $derived(new Map(workflowRows.map((workflow) => [workflow.id, workflow.name])));
	// "Loaded" is what licenses the deleted badge: only a list that was read to
	// the end can say an id is not in it.
	const workflowsLoaded = $derived(!workflowsLoading && !workflowsFailed);
	const workflowFilterOptions = $derived(filterWorkflowOptions(workflowRows, workflowSearch));

	// A failure with rows behind it came from "Load more" — load() empties the
	// page before it records one, so a first-load failure never reaches here.
	const pagingFailure = $derived(
		failedBesideRows({ loading, failed: failure !== null, count: page.items.length })
	);
	const filtersActive = $derived(hasActiveFilters({ status, workflowId: workflowID }));

	$effect(() => {
		// Re-read whenever a filter changes; the first run also loads the page.
		// The URL write is guarded by comparison: writing unconditionally
		// would loop back through $app/state and re-trigger this effect.
		const search = buildExecutionSearch({ status, workflowId: workflowID });
		const current = route.url.search.startsWith('?') ? route.url.search.slice(1) : route.url.search;
		if (search !== current) {
			void goto(`/executions${search ? `?${search}` : ''}`, { replaceState: true, keepFocus: true, noScroll: true });
		}
		void load(status, workflowID);
	});

	// One source of truth for whether the poll may run, read by the effect
	// below and by refreshHead itself (a timer that fired just as a load began
	// must not fetch either).
	function pollSignals(): HeadPollSignals {
		return {
			enabled: autoRefresh,
			loading,
			loadingMore,
			hidden: tabHidden,
			failedFirstLoad: failure !== null && page.items.length === 0
		};
	}

	$effect(() => {
		// Auto-refresh polls the newest page and MERGES it into what is loaded
		// (mergeHead), so a poll keeps the tail the user loaded and the cursor
		// "Load more" resumes from instead of cutting the list back to the
		// first page. Watching production runs without pressing Refresh is the
		// reason this page exists; it pauses on a manual load, on a first-load
		// failure and while the tab is hidden, where a fetch would either
		// discard loaded rows or burn a laptop.
		//
		// `pollTurn` is read so the chain keeps going: a poll that finds
		// nothing new changes no other reactive value this effect reads (the
		// `&&` in pollSignals short-circuits the page read whenever there is
		// no failure), so without it the effect would arm one timer and stop.
		// The effect only READS reactive state — pollTurn is written by
		// refreshHead, outside the effect — so it cannot loop.
		void pollTurn;
		if (!shouldPollHead(pollSignals())) return;
		const timer = setTimeout(() => void refreshHead(status, workflowID), HEAD_POLL_INTERVAL_MS);
		return () => clearTimeout(timer);
	});

	async function load(activeStatus: string, activeWorkflow: string) {
		const token = guard.start();
		loading = true;
		failure = null;
		try {
			const response = await listExecutions(
				buildListExecutionsParams({ status: activeStatus, workflowId: activeWorkflow })
			);
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

	async function refreshHead(activeStatus: string, activeWorkflow: string) {
		// A poll that finds nothing new must never surface as a failure: the
		// rows on screen arrived intact, and a transient poll error is news
		// about nothing. It rewrites the rows the head covers and leaves a
		// Load more tail where it was, but it can move the list's cursor: when
		// the head falls back to the newest page alone, the cursor it carries
		// replaces the loaded one, which is why loadMore re-checks the cursor
		// it asked with before appending (appendPageIfCurrent).
		if (!shouldPollHead(pollSignals())) return;
		const token = guard.current;
		try {
			const response = await listExecutions(
				buildListExecutionsParams({ status: activeStatus, workflowId: activeWorkflow })
			);
			if (response.status !== 200) throw new Error('Unexpected execution-list response');
			if (!guard.holds(token)) return;
			// `page` is read here, when the response resolves, not before the
			// await: a Load more that finished meanwhile is part of the list
			// this merge folds the new page into, and a copy taken earlier
			// would overwrite it.
			page = mergeHead(page, readPage(response.data), (row) => row.id);
		} catch {
			// Deliberately swallowed: see above.
		} finally {
			// Bumped even after a swallowed failure: the chain has to keep
			// going, or one bad poll would end auto-refresh for good.
			pollTurn += 1;
		}
	}

	async function loadMore() {
		if (!canLoadMore(page) || loadingMore) return;
		// Joins the request already in flight rather than starting a new one:
		// starting would supersede that request, which would then discard its
		// own answer and leave the page stuck on its skeleton.
		const token = guard.current;
		// The cursor this request is made with, held so the answer can be
		// checked against it: a poll that lands in the meantime may have moved
		// the list's cursor, and the page that arrives under the old one no
		// longer follows the rows it would be appended to.
		const asked = page.nextCursor;
		loadingMore = true;
		// Clearing here rather than on success is what makes a second press of
		// the button read as a retry: the notice goes away while the attempt
		// it describes is running, and comes back only if this one fails too.
		failure = null;
		try {
			const response = await listExecutions(
				buildListExecutionsParams({ status, workflowId: workflowID }, asked)
			);
			if (response.status !== 200) throw new Error('Unexpected execution-list response');
			if (!guard.holds(token)) return;
			// Drop the page instead of splicing it on when the cursor moved:
			// appending it anyway would leave a hole the user never sees.
			// appendPageIfCurrent reads `page` when the response resolves.
			page = appendPageIfCurrent(page, asked, response.data);
		} catch (cause) {
			// `page` is deliberately left alone: the cursor only moves on
			// success, so a retry asks for the page that failed rather than
			// the one after it. Resetting the rows here would lose every page
			// already loaded.
			if (guard.holds(token)) failure = cause;
		} finally {
			loadingMore = false;
		}
	}

	async function stop(item: ExecutionSummary) {
		if (stoppingID) return;
		stoppingID = item.id;
		stopError = null;
		try {
			const response = await cancelExecution(item.id);
			if (response.status !== 202 && response.status !== 200) throw new Error('Unexpected cancel response');
			await load(status, workflowID);
		} catch (error) {
			stopError = message(error);
		} finally {
			stoppingID = null;
		}
	}

	function clearFilters() {
		status = '';
		workflowID = '';
		workflowSearch = '';
	}
</script>

<svelte:document onvisibilitychange={() => (tabHidden = document.hidden)} />

<svelte:head>
	<title>Executions · KilasFlow</title>
</svelte:head>

<section class="mx-auto w-full max-w-5xl">
	<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
		<div class="max-w-xl">
			<h1 class="text-base font-semibold tracking-tight">Executions</h1>
			<p class="text-xs text-muted-foreground">Every run your workspace has recorded, newest first. Open one to replay its graph.</p>
		</div>
		<div class="flex shrink-0 items-center gap-2">
			<label class="inline-flex h-9 cursor-pointer items-center gap-2 rounded-md border border-border px-2.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted has-checked:text-foreground">
				<input type="checkbox" bind:checked={autoRefresh} class="size-3.5 accent-primary" />
				Auto-refresh
			</label>
			<Button variant="outline" onclick={() => void load(status, workflowID)} disabled={loading}>
				<RefreshCw aria-hidden="true" />
				Refresh
			</Button>
		</div>
	</div>
	{#if stopError}
		<p role="alert" class="mt-4 rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">Stop failed: {stopError}</p>
	{/if}

	<div class="mt-6 flex flex-wrap items-end gap-3">
		<div class="grid gap-1">
			<label for="execution-status" class="text-xs font-medium text-muted-foreground">Status</label>
			<select id="execution-status" bind:value={status} class="h-7 min-w-36 rounded-md border border-input bg-background px-1.5 text-xs">
				<option value="">All statuses</option>
				{#each EXECUTION_STATUSES as option (option)}
					<option value={option}>{statusLabel(option)}</option>
				{/each}
			</select>
		</div>
		<div class="grid gap-1">
			<label for="execution-workflow" class="text-xs font-medium text-muted-foreground">Workflow</label>
			<select id="execution-workflow" bind:value={workflowID} class="h-7 min-w-44 max-w-64 rounded-md border border-input bg-background px-1.5 text-xs">
				<option value="">All workflows</option>
				{#each workflowFilterOptions as workflow (workflow.id)}
					<option value={workflow.id}>{workflow.name}</option>
				{/each}
			</select>
		</div>
		{#if workflowRows.length > 8}
			<div class="grid gap-1">
				<label for="execution-workflow-search" class="text-xs font-medium text-muted-foreground">Find workflow</label>
				<input id="execution-workflow-search" type="search" bind:value={workflowSearch} placeholder="Type to narrow…" class="h-7 w-44 rounded-md border border-input bg-background px-2 text-xs" />
			</div>
		{/if}
		{#if filtersActive}
			<Button variant="ghost" size="sm" onclick={clearFilters}>Clear filters</Button>
		{/if}
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
			emptyTitle={filtersActive ? 'No executions match these filters' : 'No executions yet'}
			emptyBody={filtersActive ? 'Loosen the filters above, or clear them to see everything again.' : 'Run a workflow from its editor and its history will appear here.'}
		>
			{#snippet emptyAction()}
				{#if filtersActive}
					<Button class="mt-3" size="sm" variant="outline" onclick={clearFilters}>Clear filters</Button>
				{/if}
			{/snippet}
			<!-- A plain paragraph, not role=status: with auto-refresh on, a live
			     region would announce every new run to a screen reader. -->
			<p class="mb-2 text-xs text-muted-foreground">{executionListSummary({ count: page.items.length, hasMore: canLoadMore(page), filtered: filtersActive })}</p>
			<div class="overflow-hidden rounded-xl border border-border bg-card">
				<Table.Root class="min-w-[48rem]">
					<Table.Caption class="sr-only">Workflow executions, newest first</Table.Caption>
					<Table.Header class="[&_th]:h-7 [&_th]:px-3 [&_th]:text-xs [&_th]:uppercase [&_th]:tracking-wide [&_th]:text-muted-foreground">
						<Table.Row>
							<Table.Head scope="col">Status</Table.Head>
							<Table.Head scope="col">Workflow</Table.Head>
							<Table.Head scope="col">Started</Table.Head>
							<Table.Head scope="col">Duration</Table.Head>
							<Table.Head scope="col">Trigger</Table.Head>
							<Table.Head scope="col">Execution</Table.Head>
							<Table.Head scope="col"><span class="sr-only">Actions</span></Table.Head>
						</Table.Row>
					</Table.Header>
					<Table.Body>
						{#each page.items as item (item.id)}
							<Table.Row>
								<Table.Cell class="px-3 py-2">
									<span class={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${statusTone(item.status)}`}>{statusLabel(item.status)}</span>
								</Table.Cell>
								<Table.Cell class="max-w-56 truncate px-3 py-2" title={workflowNames.get(item.workflowId) ?? item.workflowId}>
									{workflowNames.get(item.workflowId) ?? item.workflowId}
									{#if isDeletedWorkflow(item.workflowId, workflowsLoaded, workflowNames)}
										<span class="ml-1 rounded border border-border bg-muted px-1 py-px text-[0.625rem] font-medium text-muted-foreground">deleted</span>
									{/if}
								</Table.Cell>
								<Table.Cell class="px-3 py-2 text-muted-foreground">{formatTimestamp(item.startedAt)}</Table.Cell>
								<Table.Cell class="px-3 py-2 tabular-nums text-muted-foreground">{formatDuration(item.durationMs ?? null)}</Table.Cell>
								<Table.Cell class="px-3 py-2 text-muted-foreground">{statusLabel(item.trigger)}</Table.Cell>
								<Table.Cell class="px-3 py-2">
									<a href={`/executions/${item.id}`} class="rounded font-medium text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2">
										<span class="font-mono text-xs">{item.id}</span>
									</a>
								</Table.Cell>
								<Table.Cell class="px-3 py-2 text-right">
									{#if isStoppableStatus(item.status)}
										<Button variant="ghost" size="sm" class="h-7 px-2 text-[0.6875rem]" disabled={stoppingID !== null} onclick={() => void stop(item)} aria-label={`Stop execution ${item.id}`}>
											<Square aria-hidden="true" class="size-3" />
											{stoppingID === item.id ? 'Stopping…' : 'Stop'}
										</Button>
									{/if}
								</Table.Cell>
							</Table.Row>
						{/each}
					</Table.Body>
				</Table.Root>
			</div>
			{#if canLoadMore(page) && !pagingFailure}
				<div class="mt-4 flex justify-center">
					<Button variant="outline" onclick={() => void loadMore()} disabled={loadingMore}>{loadingMore ? 'Loading…' : 'Load more'}</Button>
				</div>
			{/if}
		</ListStates>
	</div>
</section>
