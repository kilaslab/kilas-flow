<script lang="ts">
	import { untrack } from 'svelte';

	import { listWorkflows } from '$lib/api/generated/workflows/workflows';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { DRAIN_PAGE_LIMIT, drainPages, headerCursor, readPage, type CursorPage } from '$lib/dashboard/cursor-page';
	import * as m from '$lib/paraglide/messages.js';
	import {
		errorWorkflowOptions,
		timezoneOptions,
		type SettingsProblem,
		type WorkflowSettingsForm
	} from '$lib/workflow-editor/workflow-settings';

	/**
	 * The workflow's settings the engine honours: its own run budget, the error
	 * workflow and the timezone. Applying hands the form to the editor, which
	 * writes it into the draft's `settings`; nothing is sent to the server until
	 * the workflow is saved, the same as any other edit on the canvas.
	 */
	let {
		open = $bindable(false),
		workflowId,
		initial,
		onApply
	}: {
		open: boolean;
		/** The workflow being edited, which an error workflow cannot name. */
		workflowId: string;
		/** The form as the draft's settings read now; re-read each time the dialog opens. */
		initial: WorkflowSettingsForm;
		/** Writes the form into the draft; answers what is wrong with it, or null once it took. */
		onApply: (form: WorkflowSettingsForm) => SettingsProblem | null;
	} = $props();

	let form = $state<WorkflowSettingsForm>({ timeoutSeconds: '', noTimeout: false, errorWorkflow: '', timezone: '' });
	let problem = $state<SettingsProblem | null>(null);
	let workflows = $state<{ id: string; name: string }[]>([]);
	let loadingWorkflows = $state(false);
	let listFailed = $state(false);

	// The runtime's own zone list; absent on a runtime without
	// Intl.supportedValuesOf, where the select still offers UTC and the stored zone.
	const supportedZones: string[] = (() => {
		try {
			return Intl.supportedValuesOf('timeZone');
		} catch {
			return [];
		}
	})();
	const zones = $derived(timezoneOptions(supportedZones, form.timezone));
	const errorOptions = $derived(errorWorkflowOptions(workflows, workflowId, form.errorWorkflow));

	async function fetchPage(cursor: string): Promise<CursorPage<{ id: string; name: string }>> {
		const response = await listWorkflows({ limit: DRAIN_PAGE_LIMIT, cursor: cursor || undefined });
		if (response.status !== 200) throw new Error(m.editor_settings_unexpected_list());
		return readPage({ items: response.data, nextCursor: headerCursor(response.headers) });
	}

	async function loadWorkflows() {
		loadingWorkflows = true;
		listFailed = false;
		try {
			workflows = await drainPages(fetchPage);
		} catch {
			listFailed = true;
		} finally {
			loadingWorkflows = false;
		}
	}

	// Each opening starts from what the draft holds now, so a cancelled edit
	// leaves nothing behind in the form.
	$effect(() => {
		if (!open) return;
		untrack(() => {
			form = { ...initial };
			problem = null;
			void loadWorkflows();
		});
	});

	function apply(event: SubmitEvent) {
		event.preventDefault();
		problem = onApply({ ...form });
		if (problem === null) open = false;
	}

	const selectClass =
		'h-7 w-full min-w-0 rounded-lg border border-input bg-transparent px-2 text-sm transition-colors hover:border-(--kf-border-hover) focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50';
</script>

<Dialog.Root bind:open>
	<Dialog.Content aria-describedby="workflow-settings-description" data-testid="workflow-settings-dialog">
		<Dialog.Header>
			<Dialog.Title>{m.editor_settings_title()}</Dialog.Title>
			<Dialog.Description id="workflow-settings-description">{m.editor_settings_description()}</Dialog.Description>
		</Dialog.Header>
		<form class="grid gap-4" onsubmit={apply}>
			<div class="grid gap-2">
				<label for="workflow-setting-timeout" class="text-sm font-medium">{m.editor_settings_timeout_label()}</label>
				<Input
					id="workflow-setting-timeout"
					type="text"
					inputmode="decimal"
					bind:value={form.timeoutSeconds}
					disabled={form.noTimeout}
					placeholder={m.editor_settings_timeout_placeholder()}
					aria-describedby="workflow-setting-timeout-hint"
					aria-invalid={problem === 'timeout'}
				/>
				<label class="flex items-center gap-2 text-sm">
					<input type="checkbox" bind:checked={form.noTimeout} class="size-4 shrink-0 accent-primary focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring" />
					{m.editor_settings_no_timeout()}
				</label>
				<p id="workflow-setting-timeout-hint" class="text-xs text-muted-foreground">{m.editor_settings_timeout_hint()}</p>
				{#if problem === 'timeout'}<p role="alert" class="text-sm text-destructive">{m.editor_settings_timeout_invalid()}</p>{/if}
			</div>

			<div class="grid gap-2">
				<label for="workflow-setting-error-workflow" class="text-sm font-medium">{m.editor_settings_error_workflow_label()}</label>
				<select id="workflow-setting-error-workflow" bind:value={form.errorWorkflow} aria-describedby="workflow-setting-error-workflow-hint" class={selectClass}>
					<option value="">{loadingWorkflows ? m.editor_settings_error_workflow_loading() : m.editor_settings_error_workflow_none()}</option>
					{#each errorOptions as option (option.id)}
						<option value={option.id}>{option.missing ? m.editor_settings_error_workflow_missing({ id: option.id }) : option.name}</option>
					{/each}
				</select>
				<p id="workflow-setting-error-workflow-hint" class="text-xs text-muted-foreground">{m.editor_settings_error_workflow_hint()}</p>
				{#if listFailed}<p role="alert" class="text-sm text-destructive">{m.editor_settings_error_workflow_failed()}</p>{/if}
			</div>

			<div class="grid gap-2">
				<label for="workflow-setting-timezone" class="text-sm font-medium">{m.editor_settings_timezone_label()}</label>
				<select id="workflow-setting-timezone" bind:value={form.timezone} aria-describedby="workflow-setting-timezone-hint" class={selectClass}>
					<option value="">{m.editor_settings_timezone_default()}</option>
					{#each zones as zone (zone)}
						<option value={zone}>{zone}</option>
					{/each}
				</select>
				<p id="workflow-setting-timezone-hint" class="text-xs text-muted-foreground">{m.editor_settings_timezone_hint()}</p>
			</div>

			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (open = false)}>{m.workflows_cancel()}</Button>
				<Button type="submit">{m.editor_settings_apply()}</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>
