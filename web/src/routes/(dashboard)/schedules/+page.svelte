<script lang="ts">
	import { onMount } from 'svelte';
	import CalendarClock from '@lucide/svelte/icons/calendar-clock';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import { message } from '$lib/api/http';
	import { createSchedule, deleteSchedule, listSchedules, updateSchedule } from '$lib/api/generated/schedules/schedules';
	import { listWorkflows } from '$lib/api/generated/workflows/workflows';
	import type { ScheduleResource, WorkflowSummary } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { DRAIN_PAGE_LIMIT, drainPages, headerCursor, readPage, type CursorPage } from '$lib/dashboard/cursor-page';
	import { RequestGuard } from '$lib/dashboard/request-guard';
	import * as m from '$lib/paraglide/messages.js';
	import { formatTimestamp } from '$lib/workflow-editor/execution';

	// Both lists are paged by the server, so one request is only the first page
	// of each: the schedules are read to the end before they are shown, and so
	// are the workflows whose names the rows carry and whose picker the form
	// offers — a workflow past the first page would otherwise show as a bare id
	// and be unselectable.
	let rows = $state<ScheduleResource[]>([]);
	let workflows = $state<WorkflowSummary[]>([]);
	let loading = $state(true);
	let listFailure = $state<unknown>(null);
	const listGuard = new RequestGuard();

	/** One page of the schedule listing: rows from the body, cursor from the header. */
	async function fetchSchedulePage(cursor: string): Promise<CursorPage<ScheduleResource>> {
		const response = await listSchedules({ limit: DRAIN_PAGE_LIMIT, cursor: cursor || undefined });
		if (response.status !== 200) throw new Error(m.schedules_error_list());
		return readPage({ items: response.data, nextCursor: headerCursor(response.headers) });
	}

	/** One page of the workflow listing, read only for the names and the picker. */
	async function fetchWorkflowPage(cursor: string): Promise<CursorPage<WorkflowSummary>> {
		const response = await listWorkflows({ limit: DRAIN_PAGE_LIMIT, cursor: cursor || undefined });
		if (response.status !== 200) throw new Error(m.schedules_error_workflows());
		return readPage({ items: response.data, nextCursor: headerCursor(response.headers) });
	}

	async function loadSchedules() {
		const token = listGuard.start();
		listFailure = null;
		loading = rows.length === 0;
		try {
			// The schedules are what this page is about; the workflow names are
			// read alongside them and failing to get them must not hide the
			// rows, so that half is allowed to come back empty.
			const [schedules, names] = await Promise.all([
				drainPages(fetchSchedulePage),
				drainPages(fetchWorkflowPage).catch(() => [] as WorkflowSummary[])
			]);
			if (!listGuard.holds(token)) return;
			rows = schedules;
			workflows = names;
		} catch (cause) {
			if (!listGuard.holds(token)) return;
			listFailure = cause;
		} finally {
			if (listGuard.holds(token)) loading = false;
		}
	}

	onMount(() => void loadSchedules());

	let editorOpen = $state(false);
	let editing = $state<ScheduleResource | null>(null);
	let workflowID = $state('');
	let cron = $state('0 * * * *');
	let active = $state(true);
	let saving = $state(false);
	let formError = $state<string | null>(null);

	const workflowNames = $derived(new Map(workflows.map((workflow) => [workflow.id, workflow.name])));

	function openCreate() {
		editing = null;
		workflowID = workflows[0]?.id ?? '';
		cron = '0 * * * *';
		active = true;
		formError = null;
		editorOpen = true;
	}

	/** A row owned by a Schedule Trigger node is edited on the canvas, never
	 * here: editing it here would put the list out of sync with the node. */
	function triggerOwned(schedule: ScheduleResource): boolean {
		return Boolean(schedule.nodeId);
	}

	/** Warns rather than refuses: the scheduler silently pauses schedules of
	 * inactive workflows, so saving Active for one must say it will not run. */
	function inactiveWarning(schedule: ScheduleResource): string | null {
		if (!schedule.active) return null;
		const workflow = workflows.find((candidate) => candidate.id === schedule.workflowId);
		if (workflow && !workflow.active) return m.schedules_inactive_warning();
		return null;
	}

	function openEdit(schedule: ScheduleResource) {
		editing = schedule;
		workflowID = schedule.workflowId;
		cron = schedule.cron;
		active = schedule.active;
		formError = null;
		editorOpen = true;
	}

	async function save() {
		if (!workflowID) {
			formError = m.schedules_error_choose_workflow();
			return;
		}
		if (!editing) {
			const workflow = workflows.find((candidate) => candidate.id === workflowID);
			if (workflow && !workflow.active) {
				formError = m.schedules_error_inactive_workflow();
				return;
			}
		}
		saving = true;
		formError = null;
		try {
			const body = { workflowId: workflowID, cron: cron.trim(), active };
			const response = editing ? await updateSchedule(editing.id, body) : await createSchedule(body);
			if (response.status !== 200 && response.status !== 201) throw new Error(m.schedules_error_save());
			await loadSchedules();
			editorOpen = false;
		} catch (error) {
			formError = message(error);
		} finally {
			saving = false;
		}
	}

	async function remove(schedule: ScheduleResource) {
		if (triggerOwned(schedule)) return;
		const name = workflowNames.get(schedule.workflowId) ?? schedule.workflowId;
		if (!confirm(m.schedules_confirm_delete({ cron: schedule.cron, name }))) return;
		try {
			await deleteSchedule(schedule.id);
			await loadSchedules();
		} catch (error) {
			formError = message(error);
		}
	}
</script>

<svelte:head>
	<title>{m.schedules_page_title()}</title>
</svelte:head>

<section class="mx-auto w-full max-w-4xl">
	<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
		<div class="max-w-xl">
			<h1 class="text-base font-semibold tracking-tight">{m.nav_schedules()}</h1>
			<p class="text-xs text-muted-foreground">
				{m.schedules_description()}
			</p>
		</div>
		<Button onclick={openCreate} disabled={loading} class="w-full sm:w-auto sm:shrink-0">
			<CalendarClock aria-hidden="true" />
			{m.schedules_new()}
		</Button>
	</div>

	<div class="mt-4">
		<ListStates
			label={m.nav_schedules()}
			loading={loading}
			failed={listFailure !== null}
			error={listFailure}
			count={rows.length}
			rows={2}
			onRetry={() => void loadSchedules()}
			emptyIcon={CalendarClock}
			emptyTitle={m.schedules_empty_title()}
			emptyBody={m.schedules_empty_body()}
		>
			<ul aria-label={m.nav_schedules()} class="divide-y divide-border overflow-hidden rounded-lg border border-border">
				{#each rows as schedule (schedule.id)}
					<li class="flex min-h-11 items-center gap-3 px-3 py-1.5">
						<div class="min-w-0 flex-1">
							<p class="truncate text-sm font-medium" title={workflowNames.get(schedule.workflowId) ?? schedule.workflowId}>{workflowNames.get(schedule.workflowId) ?? schedule.workflowId}</p>
							<p class="truncate text-xs text-muted-foreground" title={`${schedule.cron} ${m.schedules_row_timing({ next: formatTimestamp(schedule.nextRunAt), last: formatTimestamp(schedule.lastRunAt) })}`}>
								<code class="font-mono">{schedule.cron}</code>
								{m.schedules_row_timing({ next: formatTimestamp(schedule.nextRunAt), last: formatTimestamp(schedule.lastRunAt) })}
							</p>
							{#if triggerOwned(schedule)}
								<p class="text-[0.625rem] text-muted-foreground">{m.schedules_trigger_managed()} <a class="underline underline-offset-2" href={`/app/workflows/${schedule.workflowId}`}>{m.schedules_open_in_editor()}</a>.</p>
							{:else if inactiveWarning(schedule)}
								<p class="text-[0.625rem] text-warning" role="note">{inactiveWarning(schedule)}</p>
							{/if}
						</div>
						<span class="shrink-0 rounded-full border px-2 py-0.5 text-xs font-medium {schedule.active ? 'border-success/30 bg-success/15 text-success' : 'border-border bg-muted text-muted-foreground'}">
							{schedule.active ? m.schedules_active() : m.schedules_paused()}
						</span>
						{#if triggerOwned(schedule)}
							<Button variant="outline" size="sm" disabled title={m.schedules_trigger_edit_title()}>{m.schedules_edit()}</Button>
							<Button variant="ghost" size="sm" disabled title={m.schedules_trigger_delete_title()} aria-label={m.schedules_delete_aria({ cron: schedule.cron })}><Trash2 aria-hidden="true" class="size-4 text-destructive" /></Button>
						{:else}
							<Button variant="outline" size="sm" onclick={() => openEdit(schedule)}>{m.schedules_edit()}</Button>
							<Button variant="ghost" size="sm" aria-label={m.schedules_delete_aria({ cron: schedule.cron })} onclick={() => void remove(schedule)}>
								<Trash2 aria-hidden="true" class="size-4 text-destructive" />
							</Button>
						{/if}
					</li>
				{/each}
			</ul>
		</ListStates>
	</div>
	<Dialog.Root bind:open={editorOpen}>
		<Dialog.Content aria-describedby="schedule-form-description">
			<Dialog.Header>
				<Dialog.Title>{editing ? m.schedules_edit_title() : m.schedules_new()}</Dialog.Title>
				<Dialog.Description id="schedule-form-description">
					{m.schedules_form_description()}
				</Dialog.Description>
			</Dialog.Header>
			<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void save(); }}>
				<div class="grid gap-2">
					<label for="schedule-workflow" class="text-sm font-medium">{m.schedules_field_workflow()}</label>
					<select id="schedule-workflow" bind:value={workflowID} disabled={Boolean(editing)} class="h-7 rounded-md border border-input bg-background px-2 text-xs disabled:opacity-60">
						{#each workflows as workflow (workflow.id)}
							<option value={workflow.id}>{workflow.active ? workflow.name : m.schedules_option_not_activated({ name: workflow.name })}</option>
						{/each}
					</select>
				</div>
				<div class="grid gap-2">
					<label for="schedule-cron" class="text-sm font-medium">{m.schedules_field_cron()}</label>
					<Input id="schedule-cron" bind:value={cron} spellcheck={false} class="font-mono" />
					<p class="text-xs leading-5 text-muted-foreground">{m.schedules_cron_hint_prefix()} <code class="font-mono">0 9 * * 1-5</code> {m.schedules_cron_hint_suffix()}</p>
				</div>
				<label class="flex h-7 items-center gap-2 rounded-md border border-input px-2 text-xs">
					<input type="checkbox" bind:checked={active} />
					<span>{m.schedules_active()}</span>
				</label>
				{#if formError}<p role="alert" class="text-sm text-destructive">{formError}</p>{/if}
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => (editorOpen = false)} disabled={saving}>{m.schedules_cancel()}</Button>
					<Button type="submit" disabled={saving}>{saving ? m.schedules_saving() : m.schedules_save()}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>
</section>
