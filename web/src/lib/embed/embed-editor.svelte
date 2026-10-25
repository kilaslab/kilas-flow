<script lang="ts">
	import { onMount } from 'svelte';

	import { useQueryClient } from '@tanstack/svelte-query';

	import { message, setEmbedUnauthorizedListener } from '$lib/api/http';
	import * as m from '$lib/paraglide/messages.js';
	import { listCredentials } from '$lib/api/generated/credentials/credentials';
	import { createListNodeTypes } from '$lib/api/generated/nodes/nodes';
	import { getExecution } from '$lib/api/generated/executions/executions';
	import { activateWorkflow, deactivateWorkflow, runWorkflow } from '$lib/api/generated/workflow-lifecycle/workflow-lifecycle';
	import { createGetWorkflow, getWorkflow, updateWorkflow } from '$lib/api/generated/workflows/workflows';
	import type { CredentialResource, Definition, ExecutionResource, WorkflowDocumentInput, WorkflowResource } from '$lib/api/generated/models';
	import { Button } from '$lib/components/ui/button';
	import WorkflowEditor, { type RunSelection, type WorkflowHistoryHost } from '$lib/components/workflow-editor/workflow-editor.svelte';
	import { embedControls, type EmbedSession } from './session.svelte';
	import { parseHostCommand, type CommandReply, type ParsedCommand } from './commands';
	import { DRAIN_PAGE_LIMIT, drainPages, headerCursor, readPage } from '$lib/dashboard/cursor-page';
	import { activationFailure, activationNotices, dismissNotice, type ActivationNoticeView } from '$lib/workflow-editor/activation';
	import { cacheWorkflow } from '$lib/workflow-editor/workflow-cache';
	import { validationIssuesFromApiError, withNodeNames, type CanvasValidationIssue } from '$lib/workflow-editor/validation';

	// This component is mounted only after the host handshake has completed, so
	// every query it creates is created with the embed token already attached.
	// Gating the queries with `enabled` instead left them permanently idle.
	let { session }: { session: EmbedSession } = $props();

	const queryClient = useQueryClient();

	// Each control is shown on its own scope. Publishing and activation make a
	// revision live for the whole deployment, so a host opts its customers into
	// each with a scope of its own (workflow:publish, workflow:activate); an
	// editor minted with read, write and run shows neither.
	const controls = $derived(embedControls(session));
	const canWrite = $derived(controls.canWrite);
	const canRun = $derived(controls.canRun);
	const canRead = $derived(controls.canRead);
	const canPublish = $derived(controls.canPublish);
	const canActivate = $derived(controls.canActivate);
	const branding = $derived(session.branding);

	const workflow = createGetWorkflow<WorkflowResource>(() => session.workflowId, () => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error(m.workflows_unexpected_workflow());
				return response.data;
			}
		}
	}));
	const nodeTypes = createListNodeTypes<Definition[]>(() => ({
		query: { select: (response) => (response.status === 200 ? (response.data ?? []) : []) }
	}));
	// The listing is paged by the server, so it is read to the end: a credential
	// past the first page is still one this workflow may select. A failure —
	// credential storage is optional, and an embedded session may not be allowed
	// to read them — leaves the picker with nothing to offer rather than
	// blocking the editor.
	let credentials = $state<CredentialResource[]>([]);
	// False until the listing has settled, so an empty list is not read as "no credentials".
	let credentialsLoaded = $state(false);
	onMount(async () => {
		try {
			credentials = await drainPages(async (cursor) => {
				const response = await listCredentials({ limit: DRAIN_PAGE_LIMIT, cursor: cursor || undefined });
				if (response.status !== 200) throw new Error(m.workflows_unexpected_credential_list());
				return readPage({ items: response.data, nextCursor: headerCursor(response.headers) });
			});
		} catch {
			credentials = [];
		} finally {
			credentialsLoaded = true;
		}
	});

	let currentWorkflow = $state<WorkflowResource | null>(null);
	let saving = $state(false);
	let running = $state(false);
	let saveError = $state<string | null>(null);
	let saveIssues = $state<CanvasValidationIssue[]>([]);
	let saveConflict = $state(false);
	let runError = $state<string | null>(null);
	let runIssues = $state<CanvasValidationIssue[]>([]);
	let runMessage = $state<string | null>(null);
	let lastExecutionId = $state<string | null>(null);
	let editorDirty = $state(false);
	let activating = $state(false);
	let activationError = $state<string | null>(null);
	let activationIssues = $state<CanvasValidationIssue[]>([]);
	let notices = $state<ActivationNoticeView[]>([]);
	let newerRevision = $state<WorkflowResource | null>(null);
	/** The draft the last save carried, kept for the overwrite answer. */
	let pendingSave = $state<WorkflowDocumentInput | null>(null);
	let pollingRun = 0;

	/**
	 * The revision the canvas was built from.
	 *
	 * It is the editor's remount key and it moves on load and on restore, never
	 * on a save — a host that saves and keeps editing must not lose the
	 * selection, the open inspector or the viewport on every save.
	 */
	let canvasFrom = $state<string | null>(null);

	$effect(() => {
		const incoming = workflow.data;
		if (!incoming) return;
		if (!currentWorkflow || incoming.id !== currentWorkflow.id) {
			currentWorkflow = incoming;
			canvasFrom = `${incoming.id}:${incoming.latestVersion.id}`;
			newerRevision = null;
			editorDirty = false;
			return;
		}
		if (incoming.latestVersion.id === currentWorkflow.latestVersion.id) return;
		if (editorDirty) {
			// Adopting a revision this draft did not come from would make the
			// host's next save revert whoever wrote it.
			newerRevision = incoming;
			return;
		}
		currentWorkflow = incoming;
		canvasFrom = `${incoming.id}:${incoming.latestVersion.id}`;
	});

	// A workflow that cannot load at all is the one failure the canvas never
	// gets to show: the host's `error` event is where it lands, so the host
	// can tell its user the embed is not coming up. Fires on the transition —
	// the reads below are the query's own settled state.
	$effect(() => {
		if ((workflow.isError || nodeTypes.isError) && !currentWorkflow) {
			notifyHost('error', { code: 'load-failed', message: message(workflow.isError ? workflow.error : nodeTypes.error) });
		}
	});

	/** Tells the host what happened, always to its exact origin, never '*'. */
	function notifyHost(type: string, detail: Record<string, unknown> = {}) {
		window.parent.postMessage({ type: `kilasflow:${type}`, workflowId: session.workflowId, ...detail }, session.origin);
	}

	// The canvas itself, for the host's protocol-v2 commands. Commands act on
	// the editor's own draft (save), its Execute semantics (run) and its
	// selection (focusNode), which only the mounted component can do.
	let editorInstance = $state<ReturnType<typeof WorkflowEditor>>();

	// The 401 latch: every request the frame makes shares one token, so the
	// first refusal says everything later ones would repeat. A refreshed
	// session (a new token over the same channel) re-arms it.
	let unauthorizedAnnounced = false;
	$effect(() => {
		void session.token;
		unauthorizedAnnounced = false;
	});

	onMount(() => {
		setEmbedUnauthorizedListener(() => {
			if (unauthorizedAnnounced) return;
			unauthorizedAnnounced = true;
			notifyHost('unauthorized');
		});

		// Host commands, protocol v2. Both checks are load-bearing: the origin
		// is the same browser-verified parent the session came from, and
		// `window.parent` is the only window whose commands this frame takes.
		function onMessage(event: MessageEvent) {
			if (event.origin !== session.origin || event.source !== window.parent) return;
			const command = parseHostCommand(event.data);
			if (!command) return;
			void answerHostCommand(command);
		}
		window.addEventListener('message', onMessage);

		return () => {
			setEmbedUnauthorizedListener(null);
			window.removeEventListener('message', onMessage);
		};
	});

	function replyHostCommand(reply: CommandReply) {
		notifyHost('command-result', { ...reply });
	}

	/** Runs one host command and answers it, whatever happened. */
	async function answerHostCommand(command: ParsedCommand): Promise<void> {
		if (command.command === 'unknown') {
			replyHostCommand({ requestId: command.requestId, ok: false, code: 'unknown-command', message: `The editor carries no '${command.name}' command.` });
			return;
		}
		if (!editorInstance) {
			replyHostCommand({ requestId: command.requestId, ok: false, code: 'not-ready', message: 'The editor is still loading.' });
			return;
		}
		switch (command.command) {
			case 'save': {
				// A read-only session's host has no business saving, and the
				// editor would only drop the call; answering 'forbidden' tells
				// the host which end the refusal came from.
				if (!canWrite) {
					replyHostCommand({ requestId: command.requestId, ok: false, code: 'forbidden', message: 'This session carries no workflow:write scope.' });
					return;
				}
				await editorInstance.saveDraft();
				replyHostCommand(
					saveError || saveConflict
						? {
								requestId: command.requestId,
								ok: false,
								code: saveConflict ? 'conflict' : 'save-failed',
								message: saveError ?? undefined,
								dirty: editorDirty
							}
						: { requestId: command.requestId, ok: true, saved: !editorDirty, dirty: editorDirty, revision: currentWorkflow?.latestVersion.revision }
				);
				return;
			}
			case 'run': {
				if (!canRun) {
					replyHostCommand({ requestId: command.requestId, ok: false, code: 'forbidden', message: 'This session carries no workflow:run scope.' });
					return;
				}
				// Exactly the toolbar's Execute: a dirty canvas is saved first,
				// and the reply settles at the run's terminal state. Hosts that
				// want progress while it runs already have execution-started
				// and execution-finished.
				const execution = await editorInstance.runDraft();
				if (editorDirty) {
					replyHostCommand({ requestId: command.requestId, ok: false, code: 'save-failed', message: saveError ?? undefined, dirty: true });
					return;
				}
				if (runError) {
					replyHostCommand({ requestId: command.requestId, ok: false, code: 'run-failed', message: runError, executionId: lastExecutionId });
					return;
				}
				replyHostCommand({ requestId: command.requestId, ok: true, executionId: execution?.id ?? null, status: execution?.status });
				return;
			}
			case 'focusNode': {
				const focused = editorInstance.focusNode(command.nodeId);
				replyHostCommand(
					focused
						? { requestId: command.requestId, ok: true }
						: { requestId: command.requestId, ok: false, code: 'unknown-node', message: `No node '${command.nodeId}' on the canvas.` }
				);
				return;
			}
		}
	}

	function adoptFromServer(workflow: WorkflowResource) {
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
			if (response.status !== 200) throw new Error(m.workflows_unexpected_workflow());
			adoptFromServer(response.data);
		} catch (error) {
			saveError = message(error);
		} finally {
			saving = false;
		}
	}

	async function overwriteTheirs() {
		if (!pendingSave) return;
		saveConflict = false;
		saveError = null;
		await save(pendingSave, { force: true });
	}

	async function save(document: WorkflowDocumentInput, options: { force?: boolean } = {}) {
		if (!currentWorkflow || !canWrite) return;
		saving = true;
		saveError = null;
		saveIssues = [];
		saveConflict = false;
		runIssues = [];
		pendingSave = document;
		try {
			const response = await updateWorkflow(currentWorkflow.id, {
				...document,
				...(options.force ? {} : { baseVersionId: currentWorkflow.latestVersion.id })
			});
			if (response.status !== 200) throw new Error(m.workflows_unexpected_workflow_save());
			currentWorkflow = response.data;
			cacheWorkflow(queryClient, response.data);
			notifyHost('workflow-saved', { revision: response.data.latestVersion.revision });
		} catch (error) {
			saveError = message(error);
			saveIssues = withNodeNames(validationIssuesFromApiError(error), currentWorkflow.latestVersion.document.nodes);
			saveConflict = isConflictError(error);
			// The host's own save button (protocol v2's `save` command aside)
			// deserves the same failure it would get from the canvas's.
			notifyHost('error', { code: saveConflict ? 'conflict' : 'save-failed', message: saveError });
		} finally {
			saving = false;
		}
	}

	function isConflictError(error: unknown): boolean {
		return typeof error === 'object' && error !== null && 'status' in error && (error as { status?: unknown }).status === 409;
	}

	/**
	 * The canvas became clean or dirty, and the host cares as much as this
	 * page does: protocol v2's `dirty-changed` is what lets a host warn before
	 * navigating its own page away from a user's unsaved work. The editor
	 * reports the settled state, so the host also hears `dirty: false` after a
	 * save — the signal its guard can come down.
	 */
	function onDirtyChanged(value: boolean) {
		editorDirty = value;
		notifyHost('dirty-changed', { dirty: value });
	}

	/**
	 * The version panel's wiring, matching the dashboard's shape exactly.
	 *
	 * Browsing and diffing history is a read, restoring is a write, publishing
	 * needs the publish scope, and unpublishing — which is deactivation — needs
	 * the activate scope. The server's embed middleware draws the same lines;
	 * these flags only decide which controls are worth showing, and are never
	 * what stops a request.
	 */
	const history = $derived<WorkflowHistoryHost | null>(
		currentWorkflow && canRead
			? {
					workflowID: currentWorkflow.id,
					latestVersionID: currentWorkflow.latestVersion.id,
					canRestore: canWrite,
					canPublish,
					canUnpublish: canActivate,
					onRestored: (workflow) => {
						adoptFromServer(workflow);
						notifyHost('workflow-saved', { revision: workflow.latestVersion.revision });
					},
					onPublished: (workflow, version) => {
						currentWorkflow = workflow;
						cacheWorkflow(queryClient, workflow);
						notifyHost('workflow-published', { versionId: version.id, revision: version.revision });
					},
					onUnpublished: (workflow) => {
						currentWorkflow = workflow;
						cacheWorkflow(queryClient, workflow);
					}
				}
			: null
	);

	/**
	 * Turns the workflow on, for a session minted with workflow:activate.
	 *
	 * The request reaches the same handler the dashboard's does, so a refusal
	 * arrives as the same compile problem and is rendered the same way: the
	 * activation error above the canvas and each blocking node in the issue
	 * list.
	 */
	async function activate() {
		if (!currentWorkflow || !canActivate) return;
		activating = true;
		activationError = null;
		activationIssues = [];
		notices = [];
		try {
			const response = await activateWorkflow(currentWorkflow.id);
			if (response.status !== 200) throw new Error(m.workflows_unexpected_workflow_activate());
			currentWorkflow = response.data;
			cacheWorkflow(queryClient, response.data);
			notices = activationNotices(response.data.notices, response.data.latestVersion.document.nodes);
		} catch (error) {
			activationError = activationFailure(error);
			activationIssues = withNodeNames(validationIssuesFromApiError(error), currentWorkflow.latestVersion.document.nodes);
			// Only the server knows whether a failed activation rolled back.
			await workflow.refetch();
		} finally {
			activating = false;
		}
	}

	async function deactivate() {
		if (!currentWorkflow || !canActivate) return;
		activating = true;
		activationError = null;
		try {
			const response = await deactivateWorkflow(currentWorkflow.id);
			if (response.status !== 200) throw new Error(m.workflows_unexpected_workflow_deactivate());
			currentWorkflow = response.data;
			cacheWorkflow(queryClient, response.data);
			notices = [];
		} catch (error) {
			activationError = activationFailure(error);
			await workflow.refetch();
		} finally {
			activating = false;
		}
	}

	async function run(selection?: RunSelection): Promise<ExecutionResource | undefined> {
		if (!currentWorkflow || !canRun) return;
		running = true;
		runError = null;
		runIssues = [];
		runMessage = null;
		const token = ++pollingRun;
		try {
			// Same rule as the dashboard: a named trigger runs that subgraph only.
			// Chat sends the message payload on the same endpoint.
			const body =
				selection?.triggerNodeId || selection?.input !== undefined
					? {
							...(selection.triggerNodeId ? { triggerNodeId: selection.triggerNodeId } : {}),
							...(selection.input !== undefined ? { input: selection.input } : {})
						}
					: undefined;
			const queued = await runWorkflow(currentWorkflow.id, body);
			if (queued.status !== 202) throw new Error(m.workflows_unexpected_workflow_run());
			lastExecutionId = queued.data.id;
			runMessage = m.workflows_run_queued();
			notifyHost('execution-started', { executionId: queued.data.id });
			// The host decides how long its user waits; the editor only stops
			// watching a run that is still going after half an hour, and says so
			// rather than calling a healthy run a failure.
			const deadline = Date.now() + 30 * 60 * 1000;
			while (token === pollingRun && Date.now() < deadline) {
				await new Promise((resolve) => setTimeout(resolve, 1000));
				const execution = await getExecution(queued.data.id);
				if (execution.status !== 200) throw new Error(m.workflows_unexpected_execution());
				const status = execution.data.status;
				runMessage = status === 'succeeded' ? m.workflows_run_succeeded() : m.workflows_run_status({ status });
				if (['succeeded', 'failed', 'cancelled'].includes(status)) {
					if (status !== 'succeeded') runError = m.workflows_execution_status({ status });
					notifyHost('execution-finished', { executionId: queued.data.id, status });
					return execution.data;
				}
			}
			if (token === pollingRun) {
				runError = m.embed_run_abandoned();
				throw new Error(runError);
			}
		} catch (error) {
			runError = message(error);
			runIssues = withNodeNames(validationIssuesFromApiError(error), currentWorkflow.latestVersion.document.nodes);
			notifyHost('error', { code: 'run-failed', message: runError });
			throw error;
		} finally {
			if (token === pollingRun) running = false;
		}
	}
</script>

{#if branding.name || branding.logoUrl}
	<div class="flex h-10 shrink-0 items-center gap-2 border-b border-border px-2">
		{#if branding.logoUrl}
			<img src={branding.logoUrl} alt="" class="h-5 w-auto" />
		{/if}
		{#if branding.name}
			<span class="truncate text-[0.8125rem] font-semibold tracking-tight">{branding.name}</span>
		{/if}
		{#if !canWrite}
			<span class="ml-auto rounded-full border border-border bg-muted px-2 py-0.5 text-xs text-muted-foreground">{m.editor_state_read_only()}</span>
		{/if}
	</div>
{/if}

{#if (workflow.isError || nodeTypes.isError) && !currentWorkflow}
	<div class="grid flex-1 place-items-center p-6">
		<div role="alert" class="max-w-md rounded-xl border border-destructive/25 bg-destructive/5 p-5 text-center">
			<h1 class="font-semibold">{m.embed_workflow_load_failed()}</h1>
			<p class="mt-1 text-sm leading-6 text-muted-foreground">{message(workflow.isError ? workflow.error : nodeTypes.error)}</p>
		</div>
	</div>
{:else if nodeTypes.isPending || (workflow.isPending && !currentWorkflow)}
	<div aria-live="polite" class="grid flex-1 place-items-center text-sm text-muted-foreground">{m.embed_loading_workflow()}</div>
{:else if currentWorkflow}
	<!-- An embed token that expires turns the next background refetch into a
	     401. That is a refresh failure, not a reason to unmount the editor the
	     host's user is working in. -->
	{#if workflow.isError}
		<div role="alert" class="flex flex-wrap items-center gap-2 border-b border-destructive/25 bg-destructive/5 px-3 py-1.5">
			<p class="min-w-0 flex-1 text-xs leading-5 text-destructive">{m.workflows_editor_stale({ message: message(workflow.error) })}</p>
			<Button variant="outline" size="sm" onclick={() => void workflow.refetch()}>{m.workflows_refresh()}</Button>
		</div>
	{/if}
	{#if newerRevision}
		<div role="status" class="flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning/10 px-3 py-1.5">
			<p class="min-w-0 flex-1 text-xs leading-5">{m.workflows_newer_revision({ revision: newerRevision.latestVersion.revision })}</p>
			<Button variant="outline" size="sm" onclick={() => void reloadTheirs()}>{m.workflows_reload_theirs()}</Button>
			<Button variant="ghost" size="sm" onclick={() => (newerRevision = null)}>{m.workflows_keep_mine()}</Button>
		</div>
	{/if}
	{#key canvasFrom}
		<WorkflowEditor
			bind:this={editorInstance}
			document={currentWorkflow.latestVersion.document}
			definitions={nodeTypes.data ?? []}
			credentials={credentials}
			{credentialsLoaded}
			readOnly={!canWrite}
			hideRun={!canRun || branding.hideRun === true}
			hideSave={branding.hideSave === true}
			{saving}
			{running}
			{saveError}
			{saveIssues}
			{saveConflict}
			{runError}
			{runMessage}
			{lastExecutionId}
			{history}
			hostIssues={[...runIssues, ...activationIssues]}
			onDirtyChange={onDirtyChanged}
			onReloadConflict={() => void reloadTheirs()}
			onOverwriteConflict={() => void overwriteTheirs()}
			active={currentWorkflow.active}
			{activating}
			{notices}
			{activationError}
			onSave={save}
			onRun={run}
			onActivate={canActivate ? activate : undefined}
			onDeactivate={canActivate ? deactivate : undefined}
			onDismissNotice={(key) => (notices = dismissNotice(notices, key))}
		/>
	{/key}
{/if}
