<script lang="ts">
	import { onDestroy } from 'svelte';

	import { page } from '$app/state';
	import ArrowLeft from '@lucide/svelte/icons/arrow-left';

	import { message } from '$lib/api/http';
	import { createListCredentials } from '$lib/api/generated/credentials/credentials';
	import { createGetExpressionGrammar, createListNodeTypes } from '$lib/api/generated/nodes/nodes';
	import { getExecution } from '$lib/api/generated/executions/executions';
	import { activateWorkflow, deactivateWorkflow, runWorkflow } from '$lib/api/generated/workflow-lifecycle/workflow-lifecycle';
	import { createGetWorkflow, updateWorkflow } from '$lib/api/generated/workflows/workflows';
	import type { CredentialResource, Definition, ExpressionGrammar, WorkflowDocumentInput, WorkflowResource } from '$lib/api/generated/models';
	import { activationFailure, activationNotices, dismissNotice, type ActivationNoticeView } from '$lib/workflow-editor/activation';
	import { setExpressionGrammar } from '$lib/workflow-editor/expression-grammar';
	import WorkflowEditor, { type WorkflowHistoryHost } from '$lib/components/workflow-editor/workflow-editor.svelte';
	import ExportDialog from './export-dialog.svelte';
	import { Button } from '$lib/components/ui/button';
	import { validationIssuesFromApiError, type CanvasValidationIssue } from '$lib/workflow-editor/validation';

	const workflow = createGetWorkflow<WorkflowResource>(() => page.params.id ?? '', () => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected workflow response');
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

	// The expression grammar is served rather than duplicated in the editor, so
	// a root added in Go needs no client change. A failure here is not fatal:
	// the hint simply stops validating roots rather than validating against a
	// stale local list, which is what it used to do.
	const grammar = createGetExpressionGrammar<ExpressionGrammar | undefined>(() => ({
		query: {
			staleTime: Infinity,
			select: (response) => (response.status === 200 ? response.data : undefined)
		}
	}));
	$effect(() => setExpressionGrammar(grammar.data));

	// Credential storage is optional, so a failure here must not block the
	// editor: the picker simply offers nothing to select.
	const credentials = createListCredentials<CredentialResource[]>(() => ({
		query: {
			select: (response) => (response.status === 200 ? (response.data ?? []) : [])
		}
	}));

	let currentWorkflow = $state<WorkflowResource | null>(null);
	let saving = $state(false);
	let running = $state(false);
	let saveError = $state<string | null>(null);
	let saveIssues = $state<CanvasValidationIssue[]>([]);
	let saveConflict = $state(false);
	let runError = $state<string | null>(null);
	let runMessage = $state<string | null>(null);
	let lastExecutionId = $state<string | null>(null);
	let activating = $state(false);
	let notices = $state<ActivationNoticeView[]>([]);
	let activationError = $state<string | null>(null);
	let pollingRun = 0;

	$effect(() => {
		if (workflow.data) currentWorkflow = workflow.data;
	});

	onDestroy(() => {
		pollingRun += 1;
	});

	async function save(document: WorkflowDocumentInput) {
		if (!currentWorkflow) return;
		saving = true;
		saveError = null;
		saveIssues = [];
		saveConflict = false;
		try {
			const response = await updateWorkflow(currentWorkflow.id, {
				...document,
				baseVersionId: currentWorkflow.latestVersion.id
			});
			if (response.status !== 200) throw new Error('Unexpected workflow-save response');
			currentWorkflow = response.data;
		} catch (error) {
			saveError = message(error);
			saveIssues = validationIssuesFromApiError(error);
			saveConflict = isConflictError(error);
		} finally {
			saving = false;
		}
	}

	function isConflictError(error: unknown): boolean {
		return (
			typeof error === 'object' &&
			error !== null &&
			'status' in error &&
			(error as { status?: unknown }).status === 409
		);
	}

	async function activate() {
		if (!currentWorkflow) return;
		activating = true;
		activationError = null;
		// A fresh activation answer replaces the last one whole. A notice the
		// user dismissed last time is not a thing they have already done — the
		// server has just said it is still outstanding.
		notices = [];
		try {
			const response = await activateWorkflow(currentWorkflow.id);
			if (response.status !== 200) throw new Error('Unexpected workflow-activate response');
			currentWorkflow = response.data;
			notices = activationNotices(response.data.notices, response.data.latestVersion.document.nodes);
		} catch (error) {
			activationError = activationFailure(error);
			// Only the server knows whether the rollback landed — a 502 rolls the
			// workflow back, a 500 may have left it active with a trigger that
			// registered nothing. Guessing either way reproduces the exact
			// mismatch the notices exist to prevent, so ask.
			await workflow.refetch();
		} finally {
			activating = false;
		}
	}

	async function deactivate() {
		if (!currentWorkflow) return;
		activating = true;
		activationError = null;
		try {
			const response = await deactivateWorkflow(currentWorkflow.id);
			if (response.status !== 200) throw new Error('Unexpected workflow-deactivate response');
			currentWorkflow = response.data;
			// Nothing is outstanding once the triggers are unregistered.
			notices = [];
		} catch (error) {
			activationError = activationFailure(error);
			await workflow.refetch();
		} finally {
			activating = false;
		}
	}

	/**
	 * What the version panel needs, rebuilt whenever the workflow changes.
	 *
	 * The dashboard is the owner's own surface, so it can restore and publish
	 * without further ceremony — the embed shell is the one that has to check
	 * a scope first.
	 */
	const history = $derived<WorkflowHistoryHost | null>(
		currentWorkflow
			? {
					workflowID: currentWorkflow.id,
					latestVersionID: currentWorkflow.latestVersion.id,
					canRestore: true,
					canPublish: true,
					onRestored: applyRestore,
					onPublished: applyPublish,
					onUnpublished: applyPublish
				}
			: null
	);

	/**
	 * Takes the workflow a restore returned.
	 *
	 * Assigning it moves `latestVersion.id`, which is the editor's remount key,
	 * so the canvas is rebuilt from the appended revision. That is the intended
	 * effect here and only here: the panel refuses to restore while the canvas
	 * is dirty, so there is nothing left to discard.
	 */
	function applyRestore(workflow: WorkflowResource) {
		currentWorkflow = workflow;
		activationError = null;
		notices = [];
	}

	/**
	 * Takes the workflow a publish or unpublish returned.
	 *
	 * Publishing an older revision must not disturb the canvas. It cannot change
	 * `latestVersion`, so reassigning `currentWorkflow` leaves the remount key
	 * where it was and the editor keeps its state — including any unsaved edits.
	 */
	function applyPublish(workflow: WorkflowResource) {
		currentWorkflow = workflow;
		activationError = null;
	}

	async function run() {
		if (!currentWorkflow) return;
		running = true;
		runError = null;
		runMessage = null;
		// Keep prior lastExecutionId until the new run is queued so the link stays useful mid-flight.
		const token = ++pollingRun;
		try {
			const queued = await runWorkflow(currentWorkflow.id);
			if (queued.status !== 202) throw new Error('Unexpected workflow-run response');
			lastExecutionId = queued.data.id;
				runMessage = 'Run queued…';
				for (let attempt = 0; attempt < 80 && token === pollingRun; attempt += 1) {
					await new Promise((resolve) => setTimeout(resolve, 250));
					const execution = await getExecution(queued.data.id);
				if (execution.status !== 200) throw new Error('Unexpected execution response');
				const status = execution.data.status;
				runMessage = status === 'succeeded' ? 'Run succeeded.' : `Run ${status}…`;
				if (['succeeded', 'failed', 'cancelled'].includes(status)) {
					if (status !== 'succeeded') runError = typeof execution.data.error === 'object' && execution.data.error && 'message' in execution.data.error ? String(execution.data.error.message) : `Execution ${status}.`;
					return;
				}
			}
			if (token === pollingRun) runError = 'The execution did not finish in time. Check the execution history for its final state.';
		} catch (error) {
			runError = message(error);
		} finally {
			if (token === pollingRun) running = false;
		}
	}
</script>

<svelte:head>
	<title>{workflow.data?.name ?? 'Workflow'} · KilasFlow</title>
</svelte:head>

{#snippet breadcrumb()}
	<a href="/app/workflows" class="grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring" aria-label="All workflows">
		<ArrowLeft aria-hidden="true" class="size-3.5" />
	</a>
	<p class="min-w-0 max-w-32 flex-1 truncate text-xs font-medium sm:max-w-44">{currentWorkflow?.name ?? 'Loading…'}</p>
	{#if currentWorkflow}
		<span class="ml-auto shrink-0"><ExportDialog workflowID={currentWorkflow.id} workflowName={currentWorkflow.name} /></span>
	{/if}
{/snippet}

<section class="flex h-full min-h-0 flex-col">
	{#if workflow.isPending || nodeTypes.isPending}
		<div aria-live="polite" class="grid flex-1 place-items-center text-sm text-muted-foreground">Loading workflow editor…</div>
	{:else if workflow.isError || nodeTypes.isError}
		<div class="grid flex-1 place-items-center p-6">
			<div class="max-w-lg rounded-lg border border-destructive/25 bg-destructive/5 p-3">
				<h1 class="font-semibold">Workflow editor could not be loaded</h1>
				<p class="mt-0.5 text-xs leading-5 text-muted-foreground">{message(workflow.isError ? workflow.error : nodeTypes.error)}</p>
				<Button class="mt-4" variant="outline" onclick={() => { void workflow.refetch(); void nodeTypes.refetch(); }}>Try again</Button>
			</div>
		</div>
	{:else if currentWorkflow}
		{#key currentWorkflow.latestVersion.id}
			<WorkflowEditor header={breadcrumb} document={currentWorkflow.latestVersion.document} definitions={nodeTypes.data} credentials={credentials.data ?? []} {saving} {running} {saveError} {saveIssues} {runError} {runMessage} {lastExecutionId} active={currentWorkflow.active} {activating} {notices} {activationError} {history} onSave={save} onRun={run} onActivate={activate} onDeactivate={deactivate} onDismissNotice={(key) => (notices = dismissNotice(notices, key))} />
		{/key}
	{/if}
</section>
