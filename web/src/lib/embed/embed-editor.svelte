<script lang="ts">
	import { message } from '$lib/api/http';
	import { createListCredentials } from '$lib/api/generated/credentials/credentials';
	import { createListNodeTypes } from '$lib/api/generated/nodes/nodes';
	import { getExecution } from '$lib/api/generated/executions/executions';
	import { runWorkflow } from '$lib/api/generated/workflow-lifecycle/workflow-lifecycle';
	import { createGetWorkflow, updateWorkflow } from '$lib/api/generated/workflows/workflows';
	import type { CredentialResource, Definition, WorkflowDocumentInput, WorkflowResource } from '$lib/api/generated/models';
	import WorkflowEditor, { type WorkflowHistoryHost } from '$lib/components/workflow-editor/workflow-editor.svelte';
	import { SCOPE_PUBLISH, scopeAllows, type EmbedSession } from './session.svelte';
	import { validationIssuesFromApiError, type CanvasValidationIssue } from '$lib/workflow-editor/validation';

	// This component is mounted only after the host handshake has completed, so
	// every query it creates is created with the embed token already attached.
	// Gating the queries with `enabled` instead left them permanently idle.
	let { session }: { session: EmbedSession } = $props();

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
				if (response.status !== 200) throw new Error('Unexpected workflow response');
				return response.data;
			}
		}
	}));
	const nodeTypes = createListNodeTypes<Definition[]>(() => ({
		query: { select: (response) => (response.status === 200 ? (response.data ?? []) : []) }
	}));
	const credentials = createListCredentials<CredentialResource[]>(() => ({
		query: { select: (response) => (response.status === 200 ? (response.data ?? []) : []) }
	}));

	let currentWorkflow = $state<WorkflowResource | null>(null);
	let saving = $state(false);
	let running = $state(false);
	let saveError = $state<string | null>(null);
	let saveIssues = $state<CanvasValidationIssue[]>([]);
	let runError = $state<string | null>(null);
	let runMessage = $state<string | null>(null);
	let lastExecutionId = $state<string | null>(null);
	let pollingRun = 0;

	$effect(() => {
		if (workflow.data) currentWorkflow = workflow.data;
	});

	/** Tells the host what happened, always to its exact origin, never '*'. */
	function notifyHost(type: string, detail: Record<string, unknown> = {}) {
		window.parent.postMessage({ type: `kilasflow:${type}`, workflowId: session.workflowId, ...detail }, session.origin);
	}

	async function save(document: WorkflowDocumentInput) {
		if (!currentWorkflow || !canWrite) return;
		saving = true;
		saveError = null;
		saveIssues = [];
		try {
			const response = await updateWorkflow(currentWorkflow.id, document);
			if (response.status !== 200) throw new Error('Unexpected workflow-save response');
			currentWorkflow = response.data;
			notifyHost('workflow-saved', { revision: response.data.latestVersion.revision });
		} catch (error) {
			saveError = message(error);
			saveIssues = validationIssuesFromApiError(error);
		} finally {
			saving = false;
		}
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
						currentWorkflow = workflow;
						notifyHost('workflow-saved', { revision: workflow.latestVersion.revision });
					},
					onPublished: (workflow, version) => {
						currentWorkflow = workflow;
						notifyHost('workflow-published', { versionId: version.id, revision: version.revision });
					},
					onUnpublished: (workflow) => {
						currentWorkflow = workflow;
					}
				}
			: null
	);

	async function run() {
		if (!currentWorkflow || !canRun) return;
		running = true;
		runError = null;
		runMessage = null;
		const token = ++pollingRun;
		try {
			const queued = await runWorkflow(currentWorkflow.id);
			if (queued.status !== 202) throw new Error('Unexpected workflow-run response');
			lastExecutionId = queued.data.id;
			runMessage = 'Run queued…';
			notifyHost('execution-started', { executionId: queued.data.id });
			for (let attempt = 0; attempt < 80 && token === pollingRun; attempt += 1) {
				await new Promise((resolve) => setTimeout(resolve, 250));
				const execution = await getExecution(queued.data.id);
				if (execution.status !== 200) throw new Error('Unexpected execution response');
				const status = execution.data.status;
				runMessage = status === 'succeeded' ? 'Run succeeded.' : `Run ${status}…`;
				if (['succeeded', 'failed', 'cancelled'].includes(status)) {
					if (status !== 'succeeded') runError = `Execution ${status}.`;
					notifyHost('execution-finished', { executionId: queued.data.id, status });
					return;
				}
			}
			if (token === pollingRun) runError = 'The execution did not finish in time.';
		} catch (error) {
			runError = message(error);
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
			<span class="ml-auto rounded-full border border-border bg-muted px-2 py-0.5 text-xs text-muted-foreground">Read only</span>
		{/if}
	</div>
{/if}

{#if workflow.isError || nodeTypes.isError}
	<div class="grid flex-1 place-items-center p-6">
		<div role="alert" class="max-w-md rounded-xl border border-destructive/25 bg-destructive/5 p-5 text-center">
			<h1 class="font-semibold">This workflow could not be loaded</h1>
			<p class="mt-1 text-sm leading-6 text-muted-foreground">{message(workflow.isError ? workflow.error : nodeTypes.error)}</p>
		</div>
	</div>
{:else if workflow.isPending || nodeTypes.isPending}
	<div aria-live="polite" class="grid flex-1 place-items-center text-sm text-muted-foreground">Loading workflow…</div>
{:else if currentWorkflow}
	{#key currentWorkflow.latestVersion.id}
		<WorkflowEditor
			document={currentWorkflow.latestVersion.document}
			definitions={nodeTypes.data}
			credentials={credentials.data ?? []}
			readOnly={!canWrite}
			hideRun={!canRun || branding.hideRun === true}
			hideSave={branding.hideSave === true}
			{saving}
			{running}
			{saveError}
			{saveIssues}
			{runError}
			{runMessage}
			{lastExecutionId}
			{history}
			active={currentWorkflow.active}
			onSave={save}
			onRun={run}
		/>
	{/key}
{/if}
