<script lang="ts">
	import CalendarClock from '@lucide/svelte/icons/calendar-clock';
	import RefreshCw from '@lucide/svelte/icons/refresh-cw';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import { ApiError } from '$lib/api/http';
	import { createListSchedules, createSchedule, deleteSchedule, updateSchedule } from '$lib/api/generated/schedules/schedules';
	import { createListWorkflows } from '$lib/api/generated/workflows/workflows';
	import type { ScheduleResource, WorkflowSummary } from '$lib/api/generated/models';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { formatTimestamp } from '$lib/workflow-editor/execution';

	const schedules = createListSchedules<ScheduleResource[]>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected schedule-list response');
				return response.data ?? [];
			}
		}
	}));
	const workflows = createListWorkflows<WorkflowSummary[]>(() => ({
		query: { select: (response) => (response.status === 200 ? (response.data ?? []) : []) }
	}));

	let editorOpen = $state(false);
	let editing = $state<ScheduleResource | null>(null);
	let workflowID = $state('');
	let cron = $state('0 * * * *');
	let active = $state(true);
	let saving = $state(false);
	let formError = $state<string | null>(null);

	const workflowNames = $derived(new Map((workflows.data ?? []).map((workflow) => [workflow.id, workflow.name])));

	function message(error: unknown): string {
		if (error instanceof ApiError) return `${error.status} — ${error.message}`;
		return error instanceof Error ? error.message : 'The request could not be completed.';
	}

	function openCreate() {
		editing = null;
		workflowID = workflows.data?.[0]?.id ?? '';
		cron = '0 * * * *';
		active = true;
		formError = null;
		editorOpen = true;
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
			formError = 'Choose the workflow this schedule runs.';
			return;
		}
		saving = true;
		formError = null;
		try {
			const body = { workflowId: workflowID, cron: cron.trim(), active };
			const response = editing ? await updateSchedule(editing.id, body) : await createSchedule(body);
			if (response.status !== 200 && response.status !== 201) throw new Error('Unexpected schedule-save response');
			await schedules.refetch();
			editorOpen = false;
		} catch (error) {
			formError = message(error);
		} finally {
			saving = false;
		}
	}

	async function remove(schedule: ScheduleResource) {
		try {
			await deleteSchedule(schedule.id);
			await schedules.refetch();
		} catch (error) {
			formError = message(error);
		}
	}
</script>

<svelte:head>
	<title>Schedules · KilasFlow</title>
</svelte:head>

<section class="mx-auto w-full max-w-4xl">
	<div class="flex flex-col gap-5 sm:flex-row sm:items-end sm:justify-between">
		<div class="max-w-xl">
			<h1 class="text-balance text-2xl font-semibold tracking-tight sm:text-3xl">Schedules</h1>
			<p class="mt-2 text-pretty text-sm leading-6 text-muted-foreground">
				Run an active workflow on a cron expression. Times are evaluated in UTC.
			</p>
		</div>
		<Button onclick={openCreate} disabled={workflows.isPending} class="w-full sm:w-auto">
			<CalendarClock aria-hidden="true" />
			New schedule
		</Button>
	</div>

	<div class="mt-8">
		{#if schedules.isPending}
			<div aria-live="polite" class="grid gap-3">
				<p class="text-sm text-muted-foreground">Loading schedules…</p>
				{#each Array(2) as _}
					<div class="h-16 animate-pulse rounded-xl bg-muted" aria-hidden="true"></div>
				{/each}
			</div>
		{:else if schedules.isError}
			<div class="max-w-xl rounded-xl border border-destructive/25 bg-destructive/5 p-5">
				<h2 class="font-medium">Schedules could not be loaded</h2>
				<p class="mt-1 text-sm leading-6 text-muted-foreground">{message(schedules.error)}</p>
				<Button class="mt-4" variant="outline" onclick={() => void schedules.refetch()}>
					<RefreshCw aria-hidden="true" />
					Try again
				</Button>
			</div>
		{:else if schedules.data.length === 0}
			<div class="grid min-h-56 place-items-center rounded-2xl border border-dashed border-border bg-card px-6 py-12 text-center">
				<div class="max-w-sm">
					<div aria-hidden="true" class="mx-auto grid size-11 place-items-center rounded-xl bg-accent text-accent-foreground"><CalendarClock class="size-5" /></div>
					<h2 class="mt-5 text-lg font-semibold tracking-tight">No schedules yet</h2>
					<p class="mt-2 text-sm leading-6 text-muted-foreground">Add one to run an activated workflow on a recurring cadence.</p>
				</div>
			</div>
		{:else}
			<ul aria-label="Schedules" class="divide-y divide-border overflow-hidden rounded-xl border border-border bg-card">
				{#each schedules.data as schedule (schedule.id)}
					<li class="flex items-center gap-4 px-4 py-3 sm:px-5">
						<div class="min-w-0 flex-1">
							<p class="truncate text-sm font-medium">{workflowNames.get(schedule.workflowId) ?? schedule.workflowId}</p>
							<p class="mt-1 text-xs text-muted-foreground">
								<code class="font-mono">{schedule.cron}</code>
								· Next {formatTimestamp(schedule.nextRunAt)}
								· Last {formatTimestamp(schedule.lastRunAt)}
							</p>
						</div>
						<span class="rounded-full border px-2 py-0.5 text-xs font-medium {schedule.active ? 'border-success/30 bg-success/15 text-success' : 'border-border bg-muted text-muted-foreground'}">
							{schedule.active ? 'Active' : 'Paused'}
						</span>
						<Button variant="outline" size="sm" onclick={() => openEdit(schedule)}>Edit</Button>
						<Button variant="ghost" size="sm" aria-label={`Delete schedule ${schedule.cron}`} onclick={() => void remove(schedule)}>
							<Trash2 aria-hidden="true" class="size-4 text-destructive" />
						</Button>
					</li>
				{/each}
			</ul>
		{/if}
	</div>

	<Dialog.Root bind:open={editorOpen}>
		<Dialog.Content aria-describedby="schedule-form-description">
			<Dialog.Header>
				<Dialog.Title>{editing ? 'Edit schedule' : 'New schedule'}</Dialog.Title>
				<Dialog.Description id="schedule-form-description">
					The workflow must be activated before a schedule can start it.
				</Dialog.Description>
			</Dialog.Header>
			<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void save(); }}>
				<div class="grid gap-2">
					<label for="schedule-workflow" class="text-sm font-medium">Workflow</label>
					<select id="schedule-workflow" bind:value={workflowID} disabled={Boolean(editing)} class="h-10 rounded-lg border border-input bg-background px-3 text-sm disabled:opacity-60">
						{#each workflows.data ?? [] as workflow (workflow.id)}
							<option value={workflow.id}>{workflow.name}{workflow.active ? '' : ' (not activated)'}</option>
						{/each}
					</select>
				</div>
				<div class="grid gap-2">
					<label for="schedule-cron" class="text-sm font-medium">Cron expression</label>
					<Input id="schedule-cron" bind:value={cron} spellcheck={false} class="font-mono" />
					<p class="text-xs leading-5 text-muted-foreground">Five fields, UTC. For example <code class="font-mono">0 9 * * 1-5</code> is weekdays at 09:00.</p>
				</div>
				<label class="flex min-h-10 items-center gap-2 rounded-lg border border-input px-3 text-sm">
					<input type="checkbox" bind:checked={active} />
					<span>Active</span>
				</label>
				{#if formError}<p role="alert" class="text-sm text-destructive">{formError}</p>{/if}
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => (editorOpen = false)} disabled={saving}>Cancel</Button>
					<Button type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save schedule'}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>
</section>
