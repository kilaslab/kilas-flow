<script lang="ts">
	import { onDestroy } from 'svelte';

	import { page } from '$app/state';
	import ArrowLeft from '@lucide/svelte/icons/arrow-left';

	import { ApiError } from '$lib/api/http';
	import { createListCredentials } from '$lib/api/generated/credentials/credentials';
	import { createGetExpressionGrammar, createListNodeTypes } from '$lib/api/generated/nodes/nodes';
	import { getExecution } from '$lib/api/generated/executions/executions';
	import { runWorkflow } from '$lib/api/generated/workflow-lifecycle/workflow-lifecycle';
	import { createGetWorkflow, updateWorkflow } from '$lib/api/generated/workflows/workflows';
	import type { CredentialResource, Definition, ExpressionGrammar, WorkflowDocumentInput, WorkflowResource } from '$lib/api/generated/models';
	import { setExpressionGrammar } from '$lib/workflow-editor/expression-grammar';
	import WorkflowEditor from '$lib/components/workflow-editor/workflow-editor.svelte';
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
	let runError = $state<string | null>(null);
	let runMessage = $state<string | null>(null);
	let pollingRun = 0;

	$effect(() => {
		if (workflow.data) currentWorkflow = workflow.data;
	});

	onDestroy(() => {
		pollingRun += 1;
	});

	function message(error: unknown): string {
		if (error instanceof ApiError) return error.message;
		return error instanceof Error ? error.message : 'The request could not be completed.';
	}

	async function save(document: WorkflowDocumentInput) {
		if (!currentWorkflow) return;
		saving = true;
		saveError = null;
		saveIssues = [];
		try {
			const response = await updateWorkflow(currentWorkflow.id, document);
			if (response.status !== 200) throw new Error('Unexpected workflow-save response');
			currentWorkflow = response.data;
		} catch (error) {
			saveError = message(error);
			saveIssues = validationIssuesFromApiError(error);
		} finally {
			saving = false;
		}
	}

	async function run() {
		if (!currentWorkflow) return;
		running = true;
		runError = null;
		runMessage = null;
		const token = ++pollingRun;
		try {
			const queued = await runWorkflow(currentWorkflow.id);
			if (queued.status !== 202) throw new Error('Unexpected workflow-run response');
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
	<a href="/app/workflows" class="grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring" aria-label="All workflows">
		<ArrowLeft aria-hidden="true" class="size-3.5" />
	</a>
	<p class="min-w-0 max-w-24 truncate text-[0.8125rem] font-medium sm:max-w-56">{currentWorkflow?.name ?? 'Loading…'}</p>
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
			<WorkflowEditor header={breadcrumb} document={currentWorkflow.latestVersion.document} definitions={nodeTypes.data} credentials={credentials.data ?? []} {saving} {running} {saveError} {saveIssues} {runError} {runMessage} onSave={save} onRun={run} />
		{/key}
	{/if}
</section>
