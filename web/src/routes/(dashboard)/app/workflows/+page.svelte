<script lang="ts">
	import { goto } from '$app/navigation';
	import { page as route } from '$app/state';
	import { onMount } from 'svelte';
	import Ellipsis from '@lucide/svelte/icons/ellipsis';
	import FilePlus2 from '@lucide/svelte/icons/file-plus-2';

	import { message } from '$lib/api/http';
	import { activateWorkflow, deactivateWorkflow } from '$lib/api/generated/workflow-lifecycle/workflow-lifecycle';
	import {
		createWorkflow,
		deleteWorkflow,
		getWorkflow,
		listWorkflows,
		updateWorkflow
	} from '$lib/api/generated/workflows/workflows';
	import type { WorkflowDocumentInput, WorkflowSummary } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import ActivationNotices from '$lib/components/workflow-editor/activation-notices.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Dropdown from '$lib/components/ui/dropdown-menu';
	import { Input } from '$lib/components/ui/input';
	import { activationFailure, activationNotices, dismissNotice, type ActivationNoticeView } from '$lib/workflow-editor/activation';
	import {
		buildWorkflowListSearch,
		defaultWorkflowListQuery,
		duplicateName,
		filterWorkflows,
		pageWorkflows,
		parseWorkflowListQuery,
		WORKFLOWS_PER_PAGE,
		workflowCountLabel
	} from '$lib/dashboard/workflow-list';
	import { DRAIN_PAGE_LIMIT, drainPages, headerCursor, readPage, type CursorPage } from '$lib/dashboard/cursor-page';
	import { RequestGuard } from '$lib/dashboard/request-guard';
	import * as m from '$lib/paraglide/messages.js';
	import ImportDialog from './import-dialog.svelte';

	// The list is paged by the server, and the search, filter, sort and page
	// controls below work over what the page holds — so the whole list is read
	// before any of them is offered. A single request would be the first page of
	// 100 and would under-count the heading and hide every row past it.
	let allRows = $state<WorkflowSummary[]>([]);
	let loading = $state(true);
	// The rejection itself rather than its text: message() is applied once, at the
	// shell, so this page holds no opinion about how a failure is worded.
	let listFailure = $state<unknown>(null);
	const listGuard = new RequestGuard();

	/** One page of the workflow listing: rows from the body, cursor from the header. */
	async function fetchWorkflowPage(cursor: string): Promise<CursorPage<WorkflowSummary>> {
		const response = await listWorkflows({ limit: DRAIN_PAGE_LIMIT, cursor: cursor || undefined });
		if (response.status !== 200) throw new Error(m.workflows_unexpected_workflow_list());
		return readPage({ items: response.data, nextCursor: headerCursor(response.headers) });
	}

	async function loadWorkflows() {
		const token = listGuard.start();
		listFailure = null;
		// Rows already on screen stay while a reload runs, exactly as a refetch
		// behaved: only the first load has nothing to show instead of a skeleton.
		loading = allRows.length === 0;
		try {
			const rows = await drainPages(fetchWorkflowPage);
			if (!listGuard.holds(token)) return;
			allRows = rows;
		} catch (cause) {
			if (!listGuard.holds(token)) return;
			listFailure = cause;
		} finally {
			if (listGuard.holds(token)) loading = false;
		}
	}

	onMount(() => void loadWorkflows());

	// Search, filter, sort and page live in the URL (?q=&active=&sort=&page=)
	// so a narrowed view survives reload and can be linked. Writes use
	// replaceState: a filter keystroke is not a navigation.
	const initialQuery = parseWorkflowListQuery(route.url.search);
	let search = $state(initialQuery.search);
	let activeFilter = $state(initialQuery.active);
	let sort = $state(initialQuery.sort);
	let listPage = $state(initialQuery.page);
	let selected = $state<Set<string>>(new Set());

	let createOpen = $state(false);
	let name = $state('');
	let createError = $state<string | null>(null);
	let creating = $state(false);
	let togglingID = $state<string | null>(null);
	let notices = $state<ActivationNoticeView[]>([]);
	// Names the workflow the notices below belong to. On a list, "the trigger
	// node" is not enough to find the thing that needs configuring.
	let noticesFor = $state<string | null>(null);
	let activationError = $state<string | null>(null);
	let rowError = $state<{ id: string; text: string } | null>(null);
	let rowBusyID = $state<string | null>(null);
	let renameOpen = $state(false);
	let renaming = $state<WorkflowSummary | null>(null);
	let renameName = $state('');
	let renameError = $state<string | null>(null);
	let deleteOpen = $state(false);
	let deleting = $state<WorkflowSummary | null>(null);
	let deleteError = $state<string | null>(null);
	let bulkBusy = $state(false);
	let bulkError = $state<string | null>(null);

	// Read from the drained list rather than through a query object: the rows
	// are also used inside a snippet, where the `!isPending && !isError`
	// narrowing that made `.data` non-optional no longer reaches.
	const filtered = $derived(filterWorkflows(allRows, { search, active: activeFilter, sort }));
	const paged = $derived(pageWorkflows(filtered, listPage));
	// A delete on the last page clamps to the page that still has rows,
	// rather than stranding the user on an empty numbered page.
	const rows = $derived(paged.rows);
	const totalPages = $derived(paged.pages);
	const currentPage = $derived(paged.page);

	$effect(() => {
		const query = { search, active: activeFilter, sort, page: listPage };
		const searchText = buildWorkflowListSearch(query);
		const current = route.url.search.startsWith('?') ? route.url.search.slice(1) : route.url.search;
		if (searchText !== current) {
			void goto(`/app/workflows${searchText ? `?${searchText}` : ''}`, { replaceState: true, keepFocus: true, noScroll: true });
		}
	});

	function resetCreateDialog() {
		name = '';
		createError = null;
	}

	async function createNewWorkflow() {
		const trimmedName = name.trim();
		if (!trimmedName) {
			createError = m.workflows_error_name_required();
			return;
		}

		creating = true;
		createError = null;
		try {
			const document: WorkflowDocumentInput = {
				schemaVersion: 1,
				name: trimmedName,
				nodes: [],
				connections: [],
				settings: {}
			};
			const response = await createWorkflow(document);
			if (response.status !== 201) throw new Error(m.workflows_unexpected_workflow_create());
			await loadWorkflows();
			createOpen = false;
			resetCreateDialog();
			await goto(`/app/workflows/${response.data.id}`);
		} catch (error) {
			createError = message(error);
		} finally {
			creating = false;
		}
	}

	async function toggleActivation(workflow: WorkflowSummary) {
		if (togglingID) return;
		togglingID = workflow.id;
		activationError = null;
		rowError = null;
		// The previous workflow's notices are about a workflow the user has
		// stopped looking at; carrying them under a new heading would attribute
		// them to the wrong trigger.
		notices = [];
		noticesFor = null;
		try {
			if (workflow.active) {
				const response = await deactivateWorkflow(workflow.id);
				if (response.status !== 200) throw new Error(m.workflows_unexpected_workflow_deactivate());
			} else {
				const response = await activateWorkflow(workflow.id);
				if (response.status !== 200) throw new Error(m.workflows_unexpected_workflow_activate());
				notices = activationNotices(response.data.notices, response.data.latestVersion.document.nodes);
				noticesFor = response.data.name;
			}
		} catch (error) {
			// At the row, not only at the top: a row below the fold whose
			// button briefly spins and returns looks like it did nothing.
			rowError = { id: workflow.id, text: activationFailure(error) };
			activationError = `${workflow.name} — ${activationFailure(error)}`;
		} finally {
			togglingID = null;
			// The row's dot is the only thing on this page that says whether a
			// workflow is live, so it is re-read from the server either way: a
			// failed activation the server rolled back must not leave the row
			// claiming otherwise.
			await loadWorkflows();
		}
	}

	function askRename(workflow: WorkflowSummary) {
		renaming = workflow;
		renameName = workflow.name;
		renameError = null;
		renameOpen = true;
	}

	async function confirmRename() {
		if (!renaming || rowBusyID) return;
		const trimmed = renameName.trim();
		if (!trimmed) {
			renameError = m.workflows_error_name_required();
			return;
		}
		if (trimmed.length > 255) {
			renameError = m.workflows_error_name_too_long();
			return;
		}
		rowBusyID = renaming.id;
		renameError = null;
		try {
			// Rename goes through the draft save: the document carries the
			// name, and PUT appends the renamed revision.
			const current = await getWorkflow(renaming.id);
			if (current.status !== 200) throw new Error(m.workflows_unexpected_workflow());
			const document = current.data.latestVersion.document;
			const response = await updateWorkflow(renaming.id, {
				schemaVersion: document.schemaVersion,
				name: trimmed,
				nodes: document.nodes,
				connections: document.connections,
				settings: document.settings
			});
			if (response.status !== 200) throw new Error(m.workflows_unexpected_workflow_rename());
			await loadWorkflows();
			renameOpen = false;
			renaming = null;
		} catch (error) {
			renameError = message(error);
		} finally {
			rowBusyID = null;
		}
	}

	async function duplicate(workflow: WorkflowSummary) {
		if (rowBusyID) return;
		rowBusyID = workflow.id;
		rowError = null;
		try {
			const current = await getWorkflow(workflow.id);
			if (current.status !== 200) throw new Error(m.workflows_unexpected_workflow());
			const document = current.data.latestVersion.document;
			const taken = new Set(allRows.map((entry) => entry.name));
			const response = await createWorkflow({
				schemaVersion: document.schemaVersion,
				name: duplicateName(workflow.name, taken),
				nodes: document.nodes ?? [],
				connections: document.connections ?? [],
				settings: document.settings ?? {}
			});
			if (response.status !== 201) throw new Error(m.workflows_unexpected_workflow_duplicate());
			await loadWorkflows();
			await goto(`/app/workflows/${response.data.id}`);
		} catch (error) {
			rowError = { id: workflow.id, text: message(error) };
		} finally {
			rowBusyID = null;
		}
	}

	function askDelete(workflow: WorkflowSummary) {
		deleting = workflow;
		deleteError = null;
		deleteOpen = true;
	}

	async function confirmDelete() {
		if (!deleting || rowBusyID) return;
		rowBusyID = deleting.id;
		deleteError = null;
		try {
			const response = await deleteWorkflow(deleting.id);
			if (response.status !== 204 && response.status !== 200) throw new Error(m.workflows_unexpected_workflow_delete());
			selected = new Set([...selected].filter((id) => id !== deleting?.id));
			await loadWorkflows();
			deleteOpen = false;
			deleting = null;
		} catch (error) {
			deleteError = message(error);
		} finally {
			rowBusyID = null;
		}
	}

	async function bulkSetActive(active: boolean) {
		if (bulkBusy || selected.size === 0) return;
		bulkBusy = true;
		bulkError = null;
		try {
			for (const id of selected) {
				const response = active ? await activateWorkflow(id) : await deactivateWorkflow(id);
				if (response.status !== 200) throw new Error(
					active ? m.workflows_unexpected_bulk_activate() : m.workflows_unexpected_bulk_deactivate()
				);
			}
			selected = new Set();
			await loadWorkflows();
		} catch (error) {
			bulkError = message(error);
		} finally {
			bulkBusy = false;
		}
	}

	async function bulkDelete() {
		if (bulkBusy || selected.size === 0) return;
		const names = allRows.filter((entry) => selected.has(entry.id)).map((entry) => entry.name);
		const preview = names.slice(0, 3).join(', ') + (names.length > 3 ? ` ${m.workflows_and_more({ count: names.length - 3 })}` : '');
		if (!confirm(m.workflows_bulk_delete_confirm({ count: names.length, preview }))) return;
		bulkBusy = true;
		bulkError = null;
		try {
			for (const id of selected) {
				await deleteWorkflow(id);
			}
			selected = new Set();
			await loadWorkflows();
		} catch (error) {
			bulkError = message(error);
		} finally {
			bulkBusy = false;
		}
	}

	function toggleSelected(id: string, on: boolean) {
		selected = new Set(on ? [...selected, id] : [...selected].filter((entry) => entry !== id));
	}

	function togglePage(on: boolean) {
		const ids = rows.map((entry) => entry.id);
		selected = new Set(on ? [...new Set([...selected, ...ids])] : [...selected].filter((id) => !ids.includes(id)));
	}

	function formatUpdatedAt(value: string): string {
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) return '—';
		const now = Date.now();
		const diffMs = now - date.getTime();
		const dayMs = 86_400_000;
		// Relative within the week ("3d ago"), absolute beyond it: a bare
		// "Sep 17" hides the year on old rows and the time on fresh ones.
		if (diffMs >= 0 && diffMs < 7 * dayMs) {
			const days = Math.floor(diffMs / dayMs);
			if (days <= 0) return m.workflows_updated_today();
			if (days === 1) return m.workflows_updated_yesterday();
			return m.workflows_updated_days_ago({ count: days });
		}
		return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
	}

	function resetListQuery() {
		const defaults = defaultWorkflowListQuery();
		search = defaults.search;
		activeFilter = defaults.active;
		sort = defaults.sort;
		listPage = defaults.page;
	}
</script>

<svelte:head>
	<title>{m.workflows_page_title()}</title>
</svelte:head>

<section class="mx-auto w-full max-w-5xl">
	<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
		<div class="min-w-0">
			<h1 class="text-base font-semibold tracking-tight">{m.nav_workflows()}</h1>
			<p class="text-xs text-muted-foreground">
				{#if !loading && listFailure === null}{m.workflows_in_workspace({ count: allRows.length })}{:else}{m.workflows_subtitle()}{/if}
			</p>
		</div>
		<div class="flex shrink-0 items-center gap-2">
			<ImportDialog onImported={() => void loadWorkflows()} />
			<Dialog.Root bind:open={createOpen} onOpenChange={(open) => !open && resetCreateDialog()}>
			<Dialog.Trigger>
				{#snippet child({ props })}
					<Button {...props} size="sm">
						<FilePlus2 aria-hidden="true" />
						{m.workflows_new()}
					</Button>
				{/snippet}
			</Dialog.Trigger>
			<Dialog.Content aria-describedby="new-workflow-description">
				<Dialog.Header>
					<Dialog.Title>{m.workflows_create_title()}</Dialog.Title>
					<Dialog.Description id="new-workflow-description">{m.workflows_create_description()}</Dialog.Description>
				</Dialog.Header>
				<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void createNewWorkflow(); }}>
					<div class="grid gap-2">
						<label for="workflow-name" class="text-sm font-medium">{m.workflows_name_label()}</label>
						<Input id="workflow-name" bind:value={name} placeholder={m.workflows_name_placeholder()} maxlength={255} aria-invalid={Boolean(createError)} autofocus />
						{#if createError}
							<p role="alert" class="text-sm text-destructive">{createError}</p>
						{/if}
					</div>
					<Dialog.Footer>
						<Button type="button" variant="outline" onclick={() => (createOpen = false)} disabled={creating}>{m.workflows_cancel()}</Button>
						<Button type="submit" disabled={creating}>{creating ? m.workflows_creating() : m.workflows_create_submit()}</Button>
					</Dialog.Footer>
				</form>
			</Dialog.Content>
		</Dialog.Root>
		</div>
	</div>

	{#if activationError}
		<p role="alert" class="mt-4 rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">{m.workflows_activation_failed({ message: activationError })}</p>
	{/if}
	<ActivationNotices
		{notices}
		heading={noticesFor ? m.workflows_notices_heading({ name: noticesFor }) : undefined}
		class="mt-4 overflow-hidden rounded-lg border border-warning/30"
		onDismiss={(key) => (notices = dismissNotice(notices, key))}
	/>

	<div class="mt-4 flex flex-wrap items-end gap-3">
		<div class="grid gap-1">
			<label for="workflow-search" class="text-xs font-medium text-muted-foreground">{m.workflows_search_label()}</label>
			<input id="workflow-search" type="search" bind:value={search} placeholder={m.workflows_search_placeholder()} class="h-7 w-52 rounded-md border border-input bg-background px-2 text-xs" oninput={() => (listPage = 1)} />
		</div>
		<div class="grid gap-1">
			<label for="workflow-active" class="text-xs font-medium text-muted-foreground">{m.workflows_state_label()}</label>
			<select id="workflow-active" bind:value={activeFilter} class="h-7 rounded-md border border-input bg-background px-1.5 text-xs" onchange={() => (listPage = 1)}>
				<option value="all">{m.workflows_state_all()}</option>
				<option value="active">{m.workflows_state_active()}</option>
				<option value="draft">{m.workflows_state_draft()}</option>
			</select>
		</div>
		<div class="grid gap-1">
			<label for="workflow-sort" class="text-xs font-medium text-muted-foreground">{m.workflows_sort_label()}</label>
			<select id="workflow-sort" bind:value={sort} class="h-7 rounded-md border border-input bg-background px-1.5 text-xs" onchange={() => (listPage = 1)}>
				<option value="updated">{m.workflows_sort_updated()}</option>
				<option value="created">{m.workflows_sort_created()}</option>
				<option value="name">{m.workflows_sort_name()}</option>
			</select>
		</div>
	</div>
	{#if bulkError}
		<p role="alert" class="mt-4 rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">{bulkError}</p>
	{/if}
	{#if selected.size > 0}
		<div class="mt-4 flex flex-wrap items-center gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2">
			<p class="text-xs font-medium">{m.workflows_selected({ count: selected.size })}</p>
			<Button variant="outline" size="sm" class="h-7 text-xs" disabled={bulkBusy} onclick={() => void bulkSetActive(true)}>{m.workflows_activate()}</Button>
			<Button variant="outline" size="sm" class="h-7 text-xs" disabled={bulkBusy} onclick={() => void bulkSetActive(false)}>{m.workflows_deactivate()}</Button>
			<Button variant="ghost" size="sm" class="h-7 text-xs text-destructive" disabled={bulkBusy} onclick={() => void bulkDelete()}>{m.workflows_delete()}</Button>
			<Button variant="ghost" size="sm" class="h-7 text-xs" disabled={bulkBusy} onclick={() => (selected = new Set())}>{m.workflows_clear()}</Button>
		</div>
	{/if}

	<div class="mt-4">
		<ListStates
			label={m.nav_workflows()}
			loading={loading}
			failed={listFailure !== null}
			error={listFailure}
			count={rows.length}
			rows={4}
			onRetry={() => void loadWorkflows()}
			emptyIcon={FilePlus2}
			emptyTitle={allRows.length === 0 ? m.workflows_empty_title() : m.workflows_empty_filtered_title()}
			emptyBody={allRows.length === 0 ? m.workflows_empty_body() : m.workflows_empty_filtered_body()}
		>
			{#snippet emptyAction()}
				{#if allRows.length === 0}
					<div class="mt-3 flex flex-wrap justify-center gap-2">
						<Button size="sm" onclick={() => (createOpen = true)}>
							<FilePlus2 aria-hidden="true" />
							{m.workflows_new()}
						</Button>
						<ImportDialog onImported={() => void loadWorkflows()} />
					</div>
				{:else}
					<Button class="mt-3" size="sm" variant="outline" onclick={resetListQuery}>{m.workflows_clear_filters()}</Button>
				{/if}
			{/snippet}
			<div class="overflow-hidden rounded-lg border border-border">
				<ul aria-label={m.nav_workflows()} class="divide-y divide-border">
					<li class="flex items-center gap-3 border-b border-border bg-muted/40 px-3 py-1.5">
						<input type="checkbox" checked={rows.length > 0 && rows.every((entry) => selected.has(entry.id))} onchange={(event) => togglePage(event.currentTarget.checked)} aria-label={m.workflows_select_all()} class="size-3.5 accent-primary" />
						<span class="text-[0.6875rem] text-muted-foreground">{workflowCountLabel(filtered.length, allRows.length)} · {m.workflows_page_of({ page: currentPage, pages: totalPages })}</span>
						{#if totalPages > 1}
							<span class="ml-auto flex items-center gap-1">
								<Button variant="ghost" size="sm" class="h-6 px-2 text-xs" disabled={currentPage <= 1} onclick={() => (listPage = currentPage - 1)}>{m.workflows_previous()}</Button>
								<Button variant="ghost" size="sm" class="h-6 px-2 text-xs" disabled={currentPage >= totalPages} onclick={() => (listPage = currentPage + 1)}>{m.workflows_next()}</Button>
							</span>
						{/if}
					</li>
					{#each rows as workflow (workflow.id)}
						<li class="flex items-center gap-2 transition-colors hover:bg-muted/50">
							<input type="checkbox" checked={selected.has(workflow.id)} onchange={(event) => toggleSelected(workflow.id, event.currentTarget.checked)} aria-label={m.workflows_select_row({ name: workflow.name })} class="ml-3 size-3.5 shrink-0 accent-primary" />
							<a href={`/app/workflows/${workflow.id}`} class="flex h-11 min-w-0 flex-1 items-center gap-3 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring">
								<span aria-hidden="true" class="size-1.5 shrink-0 rounded-full {workflow.active ? 'bg-success' : 'bg-muted-foreground/40'}"></span>
								<span class="min-w-0 flex-1 truncate text-[0.8125rem] font-medium" title={workflow.name}>{workflow.name}</span>
								<span class="hidden shrink-0 text-[0.6875rem] text-muted-foreground sm:inline">{workflow.active ? m.workflows_state_active() : m.workflows_state_draft()}</span>
								<span class="sr-only sm:hidden">{workflow.active ? m.workflows_state_active() : m.workflows_state_draft()}</span>
								<span class="hidden shrink-0 font-mono text-[0.6875rem] text-muted-foreground md:inline">
									<span class="sr-only">{m.workflows_revision_label()}{' '}</span>{m.workflows_revision_abbrev()}{workflow.latestRevision}
								</span>
								<span class="hidden shrink-0 text-[0.6875rem] text-muted-foreground lg:inline" title={workflow.updatedAt}>
									<span class="sr-only">{m.workflows_updated_label()}{' '}</span>{formatUpdatedAt(workflow.updatedAt)}
								</span>
							</a>
							{#if rowError?.id === workflow.id}
								<p role="alert" class="hidden max-w-64 truncate text-[0.6875rem] text-destructive xl:block" title={rowError.text}>{rowError.text}</p>
							{/if}
							<button
								type="button"
								class="inline-flex h-7 shrink-0 items-center whitespace-nowrap rounded-md border border-border px-2 text-[0.6875rem] font-medium transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-40"
								disabled={togglingID !== null}
								onclick={() => void toggleActivation(workflow)}
							>
								{#if togglingID === workflow.id}
									{workflow.active ? m.workflows_deactivating() : m.workflows_activating()}
								{:else}
									{workflow.active ? m.workflows_deactivate() : m.workflows_activate()}
								{/if}
								<span class="sr-only"> {workflow.name}</span>
							</button>
							<Dropdown.Root>
								<Dropdown.Trigger class="mr-2 inline-flex size-7 shrink-0 items-center justify-center rounded-md border border-border transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring" aria-label={m.workflows_actions_for({ name: workflow.name })}>
									<Ellipsis aria-hidden="true" class="size-3.5" />
								</Dropdown.Trigger>
								<Dropdown.Content align="end">
									<Dropdown.Item onclick={() => askRename(workflow)}>{m.workflows_rename()}</Dropdown.Item>
									<Dropdown.Item onclick={() => void duplicate(workflow)}>{m.workflows_duplicate()}</Dropdown.Item>
									<Dropdown.Item variant="destructive" onclick={() => askDelete(workflow)}>{m.workflows_delete()}</Dropdown.Item>
								</Dropdown.Content>
							</Dropdown.Root>
						</li>
					{/each}
				</ul>
			</div>
			{#if totalPages > 1}
				<div class="mt-4 flex items-center justify-center gap-2">
					<Button variant="outline" size="sm" disabled={currentPage <= 1} onclick={() => (listPage = currentPage - 1)}>{m.workflows_previous()}</Button>
					<span class="text-xs text-muted-foreground">{m.workflows_page_of({ page: currentPage, pages: totalPages })} · {workflowCountLabel(filtered.length, allRows.length)}</span>
					<Button variant="outline" size="sm" disabled={currentPage >= totalPages} onclick={() => (listPage = currentPage + 1)}>{m.workflows_next()}</Button>
				</div>
			{/if}
		</ListStates>
	</div>

	<Dialog.Root bind:open={renameOpen}>
		<Dialog.Content aria-describedby="rename-workflow-description">
			<Dialog.Header>
				<Dialog.Title>{m.workflows_rename_title()}</Dialog.Title>
				<Dialog.Description id="rename-workflow-description">{m.workflows_rename_description()}</Dialog.Description>
			</Dialog.Header>
			<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void confirmRename(); }}>
				<div class="grid gap-2">
					<label for="rename-workflow-name" class="text-sm font-medium">{m.workflows_name_label()}</label>
					<Input id="rename-workflow-name" bind:value={renameName} maxlength={255} aria-invalid={Boolean(renameError)} />
					{#if renameError}<p role="alert" class="text-sm text-destructive">{renameError}</p>{/if}
				</div>
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => (renameOpen = false)} disabled={rowBusyID !== null}>{m.workflows_cancel()}</Button>
					<Button type="submit" disabled={rowBusyID !== null}>{rowBusyID ? m.workflows_renaming() : m.workflows_rename()}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root bind:open={deleteOpen}>
		<Dialog.Content aria-describedby="delete-workflow-description">
			<Dialog.Header>
				<Dialog.Title>{m.workflows_delete_title({ name: deleting?.name ?? m.workflows_noun() })}</Dialog.Title>
				<Dialog.Description id="delete-workflow-description">
					{#if deleting?.active}{m.workflows_delete_active_note()}{' '}{/if}{m.workflows_delete_description()}
				</Dialog.Description>
			</Dialog.Header>
			{#if deleteError}<p role="alert" class="text-sm text-destructive">{deleteError}</p>{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (deleteOpen = false)} disabled={rowBusyID !== null}>{m.workflows_cancel()}</Button>
				<Button type="button" variant="destructive" onclick={() => void confirmDelete()} disabled={rowBusyID !== null}>{rowBusyID ? m.workflows_deleting() : m.workflows_delete()}</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
</section>
