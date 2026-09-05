<script lang="ts">
	import CalendarClock from '@lucide/svelte/icons/calendar-clock';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import { message } from '$lib/api/http';
	import { createListSchedules, createSchedule, deleteSchedule, updateSchedule } from '$lib/api/generated/schedules/schedules';
	import { createListWorkflows } from '$lib/api/generated/workflows/workflows';
	import type { ScheduleResource, WorkflowSummary } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
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
	// Read once here rather than through the query object in the markup: the
	// rows are used inside a snippet, where the `!isPending && !isError`
	// narrowing that made `.data` non-optional no longer reaches.
	const rows = $derived(schedules.data ?? []);

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
	<div class="flex items-center justify-between gap-4">
		<div class="max-w-xl">
			<h1 class="text-base font-semibold tracking-tight">Schedules</h1>
			<p class="text-xs text-muted-foreground">
				Run an active workflow on a cron expression. Times are evaluated in UTC.
			</p>
		</div>
		<Button onclick={openCreate} disabled={workflows.isPending} class="w-full sm:w-auto">
			<CalendarClock aria-hidden="true" />
			New schedule
		</Button>
	</div>

	<div class="mt-4">
		<ListStates
			label="Schedules"
			loading={schedules.isPending}
			failed={schedules.isError}
			error={schedules.error}
			count={rows.length}
			rows={2}
			onRetry={() => void schedules.refetch()}
			emptyIcon={CalendarClock}
			emptyTitle="No schedules yet"
			emptyBody="Add one to run an activated workflow on a recurring cadence."
		>
			<ul aria-label="Schedules" class="divide-y divide-border overflow-hidden rounded-lg border border-border">
				{#each rows as schedule (schedule.id)}
					<li class="flex h-11 items-center gap-3 px-3">
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
		</ListStates>
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
					<select id="schedule-workflow" bind:value={workflowID} disabled={Boolean(editing)} class="h-7 rounded-md border border-input bg-background px-2 text-xs disabled:opacity-60">
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
				<label class="flex h-7 items-center gap-2 rounded-md border border-input px-2 text-xs">
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
