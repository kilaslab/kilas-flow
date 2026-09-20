<script lang="ts">
	import { onDestroy } from 'svelte';

	import { useQueryClient } from '@tanstack/svelte-query';

	import { beforeNavigate } from '$app/navigation';
	import { page } from '$app/state';
	import ArrowLeft from '@lucide/svelte/icons/arrow-left';

	import { message } from '$lib/api/http';
	import { createListCredentials } from '$lib/api/generated/credentials/credentials';
	import { createGetExpressionGrammar, createListNodeTypes } from '$lib/api/generated/nodes/nodes';
	import { getExecution } from '$lib/api/generated/executions/executions';
	import { activateWorkflow, deactivateWorkflow, runWorkflow } from '$lib/api/generated/workflow-lifecycle/workflow-lifecycle';
	import { createGetWorkflow, getWorkflow, updateWorkflow } from '$lib/api/generated/workflows/workflows';
	import type { CredentialResource, Definition, ExpressionGrammar, WorkflowDocumentInput, WorkflowResource } from '$lib/api/generated/models';
	import { activationFailure, activationNotices, dismissNotice, type ActivationNoticeView } from '$lib/workflow-editor/activation';
	import { cacheWorkflow } from '$lib/workflow-editor/workflow-cache';
	import { setExpressionGrammar } from '$lib/workflow-editor/expression-grammar';
	import WorkflowEditor, { type WorkflowHistoryHost } from '$lib/components/workflow-editor/workflow-editor.svelte';
	import ExportDialog from './export-dialog.svelte';
	import { Button } from '$lib/components/ui/button';
	import { validationIssuesFromApiError, withNodeNames, type CanvasValidationIssue } from '$lib/workflow-editor/validation';

	const queryClient = useQueryClient();

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
	const credentials = createListCredentials<CredentialResource[]>(undefined, () => ({
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
	let runIssues = $state<CanvasValidationIssue[]>([]);
	let activationIssues = $state<CanvasValidationIssue[]>([]);
	let runMessage = $state<string | null>(null);
	let lastExecutionId = $state<string | null>(null);
	let activating = $state(false);
	let notices = $state<ActivationNoticeView[]>([]);
	let activationError = $state<string | null>(null);
	/** Whether the canvas holds edits the server has not seen. */
	let editorDirty = $state(false);
	/**
	 * A newer revision the server reported while the canvas was dirty.
	 *
	 * It is held rather than adopted: taking the document a save did not come
	 * from would make the next save silently revert the other writer. Until the
	 * user picks, the canvas keeps the revision it was loaded from.
	 */
	let newerRevision = $state<WorkflowResource | null>(null);
	/**
	 * What the canvas was built from, as `id:revision`.
	 *
	 * It is the editor's remount key, and it moves on load and on restore —
	 * never on a save. A save that remounted the editor dropped the selection,
	 * the open inspector and the viewport on every save, which is what this
	 * page used to do by keying on `latestVersion.id` and then assigning the
	 * saved workflow to it.
	 */
	let canvasFrom = $state<string | null>(null);
	/** The draft the last save carried, kept for the overwrite answer. */
	let pendingSave = $state<WorkflowDocumentInput | null>(null);
	let pollingRun = 0;

	/**
	 * What the canvas currently shows, kept deliberately outside `$state`.
	 *
	 * An effect that reads the state it writes re-runs on its own output, and
	 * this one both compares against the revision it adopted and assigns it.
	 * Plain variables are the honest type for it: nothing renders from them.
	 */
	let canvasRevision: string | null = null;
	let canvasWorkflowID: string | null = null;

	$effect(() => {
		const incoming = workflow.data;
		if (!incoming) return;
		const revision = `${incoming.id}:${incoming.latestVersion.id}`;
		if (revision === canvasRevision) return;

		// A dirty canvas keeps the revision it was loaded from. Adopting the
		// server's newer one would make the next save revert whoever wrote it,
		// so it is offered instead — Reload theirs / Keep mine.
		if (canvasWorkflowID === incoming.id && editorDirty) {
			newerRevision = incoming;
			return;
		}

		if (canvasWorkflowID !== incoming.id) {
			// A different workflow on the same route: the canvas has nothing in
			// common with it, so it is rebuilt from scratch.
			editorDirty = false;
			newerRevision = null;
		}
		canvasRevision = revision;
		canvasWorkflowID = incoming.id;
		currentWorkflow = incoming;
		canvasFrom = revision;
	});

	onDestroy(() => {
		pollingRun += 1;
	});

	/**
	 * The draft lives only in this tab, so leaving has to be a decision.
	 *
	 * `beforeunload` is the only thing that can interrupt a reload or a tab
	 * close, and it is deliberately not used for in-app navigation: the browser's
	 * prompt is not a UI this page can put a "save" button in.
	 */
	beforeNavigate((navigation) => {
		if (!editorDirty) return;
		// A full-page teardown is the browser's prompt to own; see below.
		if (navigation.type === 'leave') return;
		if (!window.confirm('This workflow has unsaved changes. Leave and discard them?')) navigation.cancel();
	});

	$effect(() => {
		if (!editorDirty) return;
		const onBeforeUnload = (event: BeforeUnloadEvent) => {
			event.preventDefault();
			// Safari only honours the deprecated assignment.
			event.returnValue = '';
		};
		window.addEventListener('beforeunload', onBeforeUnload);
		return () => window.removeEventListener('beforeunload', onBeforeUnload);
	});

	/**
	 * Rebuilds the canvas from whatever the server holds now.
	 *
	 * Only the two conflict answers call this: the user has either asked for
	 * the other writer's revision or has just been told theirs lost.
	 */
	function noteCanvasRevision(workflow: WorkflowResource) {
		canvasRevision = `${workflow.id}:${workflow.latestVersion.id}`;
		canvasWorkflowID = workflow.id;
	}

	function adoptFromServer(workflow: WorkflowResource) {
		noteCanvasRevision(workflow);
		currentWorkflow = workflow;
		canvasFrom = `${workflow.id}:${workflow.latestVersion.id}`;
		newerRevision = null;
		saveConflict = false;
		saveError = null;
		saveIssues = [];
		editorDirty = false;
		cacheWorkflow(queryClient, workflow);
	}

	async function reloadTheirs() {
		if (!currentWorkflow) return;
		saving = true;
		try {
			const response = await getWorkflow(currentWorkflow.id);
			if (response.status !== 200) throw new Error('Unexpected workflow response');
			adoptFromServer(response.data);
		} catch (error) {
			saveError = message(error);
		} finally {
			saving = false;
		}
	}

	/** Re-sends the draft without a base revision, which is what overwriting means. */
	async function overwriteTheirs() {
		if (!pendingSave) return;
		saveConflict = false;
		saveError = null;
		await save(pendingSave, { force: true });
	}

	async function save(document: WorkflowDocumentInput, options: { force?: boolean } = {}) {
		if (!currentWorkflow) return;
		saving = true;
		saveError = null;
		saveIssues = [];
		saveConflict = false;
		runIssues = [];
		activationIssues = [];
		pendingSave = document;
		try {
			const response = await updateWorkflow(currentWorkflow.id, {
				...document,
				// A forced overwrite names no base revision, which is the only
				// way to say "I know, write mine anyway" to the API.
				...(options.force ? {} : { baseVersionId: currentWorkflow.latestVersion.id })
			});
			if (response.status !== 200) throw new Error('Unexpected workflow-save response');
			// Adopted in place: the canvas keeps its viewport, its selection and
			// the open inspector, and the returned document becomes the new
			// baseline the editor compares its draft against. Recording the
			// revision is what stops the adoption effect below from treating the
			// save's own answer as a revision to rebuild from.
			noteCanvasRevision(response.data);
			currentWorkflow = response.data;
			cacheWorkflow(queryClient, response.data);
		} catch (error) {
			saveError = message(error);
			saveIssues = withNodeNames(validationIssuesFromApiError(error), currentWorkflow.latestVersion.document.nodes);
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
			noteCanvasRevision(response.data);
			currentWorkflow = response.data;
			cacheWorkflow(queryClient, response.data);
			notices = activationNotices(response.data.notices, response.data.latestVersion.document.nodes);
		} catch (error) {
			activationError = activationFailure(error);
			// The 422 names each node that blocked activation. Throwing that away
			// left the user with a bare "workflow validation failed" and no way to
			// find the node at fault.
			activationIssues = withNodeNames(validationIssuesFromApiError(error), currentWorkflow.latestVersion.document.nodes);
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
			noteCanvasRevision(response.data);
			currentWorkflow = response.data;
			cacheWorkflow(queryClient, response.data);
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
	 * A restore is the one answer that rebuilds the canvas: the draft has to be
	 * replaced by the revision that was restored. Moving `canvasFrom` is what
	 * says so — the panel refuses to restore while the canvas is dirty, so
	 * there is nothing left to discard.
	 */
	function applyRestore(workflow: WorkflowResource) {
		adoptFromServer(workflow);
		activationError = null;
		notices = [];
	}

	/**
	 * Takes the workflow a publish or unpublish returned.
	 *
	 * Publishing an older revision must not disturb the canvas. It cannot change
	 * `latestVersion`, so `canvasFrom` stays where it was and the editor keeps
	 * its state — including any unsaved edits.
	 */
	function applyPublish(workflow: WorkflowResource) {
		noteCanvasRevision(workflow);
		currentWorkflow = workflow;
		cacheWorkflow(queryClient, workflow);
		activationError = null;
	}

	async function run() {
		if (!currentWorkflow) return;
		running = true;
		runError = null;
		runIssues = [];
		runMessage = null;
		// Keep prior lastExecutionId until the new run is queued so the link stays useful mid-flight.
		const token = ++pollingRun;
		try {
			const queued = await runWorkflow(currentWorkflow.id);
			if (queued.status !== 202) throw new Error('Unexpected workflow-run response');
			lastExecutionId = queued.data.id;
			runMessage = 'Run queued…';
			// The editor used to give up after eighty quarter-second polls, so a
			// healthy agent or LLM step was reported as a failure at twenty
			// seconds. A run's own timeout is the runner's business, not the
			// toolbar's: the ceiling here is only a stop for a poll that is
			// never coming back.
			const deadline = Date.now() + 30 * 60 * 1000;
			while (token === pollingRun && Date.now() < deadline) {
				await new Promise((resolve) => setTimeout(resolve, 1000));
				const execution = await getExecution(queued.data.id);
				if (execution.status !== 200) throw new Error('Unexpected execution response');
				const status = execution.data.status;
				runMessage = status === 'succeeded' ? 'Run succeeded.' : `Run ${status}…`;
				if (['succeeded', 'failed', 'cancelled'].includes(status)) {
					if (status !== 'succeeded') runError = typeof execution.data.error === 'object' && execution.data.error && 'message' in execution.data.error ? String(execution.data.error.message) : `Execution ${status}.`;
					return;
				}
			}
			if (token === pollingRun) runError = 'This editor stopped watching a run that is still going. Check the execution history for its final state.';
		} catch (error) {
			runError = message(error);
			// A 422 from the run endpoint names the nodes that blocked it.
			runIssues = withNodeNames(validationIssuesFromApiError(error), currentWorkflow.latestVersion.document.nodes);
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
	{#if nodeTypes.isPending || (workflow.isPending && !currentWorkflow)}
		<div aria-live="polite" class="grid flex-1 place-items-center text-sm text-muted-foreground">Loading workflow editor…</div>
	{:else if (workflow.isError || nodeTypes.isError) && !currentWorkflow}
		<div class="grid flex-1 place-items-center p-6">
			<div class="max-w-lg rounded-lg border border-destructive/25 bg-destructive/5 p-3">
				<h1 class="font-semibold">Workflow editor could not be loaded</h1>
				<p class="mt-0.5 text-xs leading-5 text-muted-foreground">{message(workflow.isError ? workflow.error : nodeTypes.error)}</p>
				<Button class="mt-4" variant="outline" onclick={() => { void workflow.refetch(); void nodeTypes.refetch(); }}>Try again</Button>
			</div>
		</div>
	{:else if currentWorkflow}
		<!-- A refetch that fails while the canvas is already open is a refresh
		     failure, not a page failure. This branch used to win over
		     `currentWorkflow` and replace the editor — with the draft and every
		     unsaved edit inside it — over one 5xx on a window-focus refetch. -->
		{#if workflow.isError}
			<div role="alert" class="flex flex-wrap items-center gap-2 border-b border-destructive/25 bg-destructive/5 px-3 py-1.5">
				<p class="min-w-0 flex-1 text-xs leading-5 text-destructive">This editor may be out of date — the last refresh failed: {message(workflow.error)}</p>
				<Button variant="outline" size="sm" onclick={() => void workflow.refetch()}>Refresh</Button>
			</div>
		{/if}
		<!-- The server moved while this canvas was dirty. Adopting it silently
		     would make the next save revert the other writer; ignoring it would
		     hide that somebody else is editing. So it is offered. -->
		{#if newerRevision}
			<div role="status" class="flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning/10 px-3 py-1.5">
				<p class="min-w-0 flex-1 text-xs leading-5">A newer revision ({newerRevision.latestVersion.revision}) was saved elsewhere while you were editing.</p>
				<Button variant="outline" size="sm" onclick={() => void reloadTheirs()}>Reload theirs</Button>
				<Button variant="ghost" size="sm" onclick={() => (newerRevision = null)}>Keep mine</Button>
			</div>
		{/if}
		{#key canvasFrom}
			<WorkflowEditor
				header={breadcrumb}
				document={currentWorkflow.latestVersion.document}
				definitions={nodeTypes.data ?? []}
				credentials={credentials.data ?? []}
				{saving}
				{running}
				{saveError}
				{saveIssues}
				{saveConflict}
				runError={runError}
				runMessage={runMessage}
				lastExecutionId={lastExecutionId}
				active={currentWorkflow.active}
				{activating}
				{notices}
				{activationError}
				{history}
				hostIssues={[...runIssues, ...activationIssues]}
				onDirtyChange={(value) => (editorDirty = value)}
				onReloadConflict={() => void reloadTheirs()}
				onOverwriteConflict={() => void overwriteTheirs()}
				onSave={save}
				onRun={run}
				onActivate={activate}
				onDeactivate={deactivate}
				onDismissNotice={(key) => (notices = dismissNotice(notices, key))}
			/>
		{/key}
	{/if}
</section>
