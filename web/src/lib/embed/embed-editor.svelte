<script lang="ts">
	import { onMount } from 'svelte';

	import { useQueryClient } from '@tanstack/svelte-query';

	import { message } from '$lib/api/http';
	import * as m from '$lib/paraglide/messages.js';
	import { listCredentials } from '$lib/api/generated/credentials/credentials';
	import { createListNodeTypes } from '$lib/api/generated/nodes/nodes';
	import { getExecution } from '$lib/api/generated/executions/executions';
	import { runWorkflow } from '$lib/api/generated/workflow-lifecycle/workflow-lifecycle';
	import { createGetWorkflow, getWorkflow, updateWorkflow } from '$lib/api/generated/workflows/workflows';
	import type { CredentialResource, Definition, WorkflowDocumentInput, WorkflowResource } from '$lib/api/generated/models';
	import { Button } from '$lib/components/ui/button';
	import WorkflowEditor, { type RunSelection, type WorkflowHistoryHost } from '$lib/components/workflow-editor/workflow-editor.svelte';
	import { SCOPE_PUBLISH, scopeAllows, type EmbedSession } from './session.svelte';
	import { DRAIN_PAGE_LIMIT, drainPages, headerCursor, readPage } from '$lib/dashboard/cursor-page';
	import { cacheWorkflow } from '$lib/workflow-editor/workflow-cache';
	import { validationIssuesFromApiError, withNodeNames, type CanvasValidationIssue } from '$lib/workflow-editor/validation';

	// This component is mounted only after the host handshake has completed, so
	// every query it creates is created with the embed token already attached.
	// Gating the queries with `enabled` instead left them permanently idle.
	let { session }: { session: EmbedSession } = $props();

	const queryClient = useQueryClient();

	const canWrite = $derived(scopeAllows(session, 'workflow:write'));
	const canRun = $derived(scopeAllows(session, 'workflow:run'));
	const canRead = $derived(scopeAllows(session, 'workflow:read'));
	// Publishing an embedded workflow makes its webhook live for the whole
	// deployment, so it needs a scope of its own that the server does not mint
	// by default. Every session in existence today evaluates this to false, and
	// the publish controls stay hidden — which is the intended default, not an
	// oversight.
	const canPublish = $derived(scopeAllows(session, SCOPE_PUBLISH));
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
	onMount(async () => {
		try {
			credentials = await drainPages(async (cursor) => {
				const response = await listCredentials({ limit: DRAIN_PAGE_LIMIT, cursor: cursor || undefined });
				if (response.status !== 200) throw new Error(m.workflows_unexpected_credential_list());
				return readPage({ items: response.data, nextCursor: headerCursor(response.headers) });
			});
		} catch {
			credentials = [];
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

	/** Tells the host what happened, always to its exact origin, never '*'. */
	function notifyHost(type: string, detail: Record<string, unknown> = {}) {
		window.parent.postMessage({ type: `kilasflow:${type}`, workflowId: session.workflowId, ...detail }, session.origin);
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
		} finally {
			saving = false;
		}
	}

	function isConflictError(error: unknown): boolean {
		return typeof error === 'object' && error !== null && 'status' in error && (error as { status?: unknown }).status === 409;
	}

	/**
	 * The version panel's wiring, matching the dashboard's shape exactly.
	 *
	 * Browsing and diffing history is a read, restoring is a write, and
	 * publishing needs the separate publish scope. The server's embed
	 * middleware draws the same three lines; these flags only decide which
	 * controls are worth showing, and are never what stops a request.
	 */
	const history = $derived<WorkflowHistoryHost | null>(
		currentWorkflow && canRead
			? {
					workflowID: currentWorkflow.id,
					latestVersionID: currentWorkflow.latestVersion.id,
					canRestore: canWrite,
					canPublish,
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

	async function run(selection?: RunSelection) {
		if (!currentWorkflow || !canRun) return;
		running = true;
		runError = null;
		runIssues = [];
		runMessage = null;
		const token = ++pollingRun;
		try {
			// Same rule as the dashboard: a named trigger runs that subgraph only.
			const queued = await runWorkflow(
				currentWorkflow.id,
				selection?.triggerNodeId ? { triggerNodeId: selection.triggerNodeId } : undefined
			);
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
					return;
				}
			}
			if (token === pollingRun) runError = m.embed_run_abandoned();
		} catch (error) {
			runError = message(error);
			runIssues = withNodeNames(validationIssuesFromApiError(error), currentWorkflow.latestVersion.document.nodes);
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
			document={currentWorkflow.latestVersion.document}
			definitions={nodeTypes.data ?? []}
			credentials={credentials}
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
			hostIssues={runIssues}
			onDirtyChange={(value) => (editorDirty = value)}
			onReloadConflict={() => void reloadTheirs()}
			onOverwriteConflict={() => void overwriteTheirs()}
			active={currentWorkflow.active}
			onSave={save}
			onRun={run}
		/>
	{/key}
{/if}
