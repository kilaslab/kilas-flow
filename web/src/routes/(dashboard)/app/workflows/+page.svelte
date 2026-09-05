<script lang="ts">
	import { goto } from '$app/navigation';
	import FilePlus2 from '@lucide/svelte/icons/file-plus-2';

	import { message } from '$lib/api/http';
	import { activateWorkflow, deactivateWorkflow } from '$lib/api/generated/workflow-lifecycle/workflow-lifecycle';
	import { createListWorkflows, createWorkflow } from '$lib/api/generated/workflows/workflows';
	import type { WorkflowDocumentInput, WorkflowSummary } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import ActivationNotices from '$lib/components/workflow-editor/activation-notices.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { activationFailure, activationNotices, dismissNotice, type ActivationNoticeView } from '$lib/workflow-editor/activation';

	const workflows = createListWorkflows<WorkflowSummary[]>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected workflow-list response');
				return response.data ?? [];
			}
		}
	}));

	let createOpen = $state(false);
	let name = $state('');
	let createError = $state<string | null>(null);
	let creating = $state(false);
	let togglingID = $state<string | null>(null);
	let notices = $state<ActivationNoticeView[]>([]);
	// Names the workflow the notices below belong to. On a list, "the trigger
	// node" is not enough to find the thing that needs configuring.
	let noticesFor = $state<string | null>(null);
	let activationError = $state<string | null>(null);

	// Read once here rather than through the query object in the markup: the
	// rows are used inside a snippet, where the `!isPending && !isError`
	// narrowing that made `.data` non-optional no longer reaches.
	const rows = $derived(workflows.data ?? []);

	function resetCreateDialog() {
		name = '';
		createError = null;
	}

	async function createNewWorkflow() {
		const trimmedName = name.trim();
		if (!trimmedName) {
			createError = 'Give this workflow a name to continue.';
			return;
		}

		creating = true;
		createError = null;
		try {
			const document: WorkflowDocumentInput = {
				schemaVersion: 1,
				name: trimmedName,
				nodes: [],
				connections: [],
				settings: {}
			};
			const response = await createWorkflow(document);
			if (response.status !== 201) throw new Error('Unexpected workflow-create response');
			await workflows.refetch();
			createOpen = false;
			resetCreateDialog();
			await goto(`/app/workflows/${response.data.id}`);
		} catch (error) {
			createError = message(error);
		} finally {
			creating = false;
		}
	}

	async function toggleActivation(workflow: WorkflowSummary) {
		if (togglingID) return;
		togglingID = workflow.id;
		activationError = null;
		// The previous workflow's notices are about a workflow the user has
		// stopped looking at; carrying them under a new heading would attribute
		// them to the wrong trigger.
		notices = [];
		noticesFor = null;
		try {
			if (workflow.active) {
				const response = await deactivateWorkflow(workflow.id);
				if (response.status !== 200) throw new Error('Unexpected workflow-deactivate response');
			} else {
				const response = await activateWorkflow(workflow.id);
				if (response.status !== 200) throw new Error('Unexpected workflow-activate response');
				notices = activationNotices(response.data.notices, response.data.latestVersion.document.nodes);
				noticesFor = response.data.name;
			}
		} catch (error) {
			activationError = `${workflow.name} — ${activationFailure(error)}`;
		} finally {
			togglingID = null;
			// The row's dot is the only thing on this page that says whether a
			// workflow is live, so it is re-read from the server either way: a
			// failed activation the server rolled back must not leave the row
			// claiming otherwise.
			await workflows.refetch();
		}
	}

	function formatUpdatedAt(value: string): string {
		const date = new Date(value);
		return Number.isNaN(date.getTime()) ? '—' : date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
	}
</script>

<svelte:head>
	<title>Workflows · KilasFlow</title>
</svelte:head>

<section class="mx-auto w-full max-w-5xl">
	<div class="flex items-center justify-between gap-4">
		<div class="min-w-0">
			<h1 class="text-base font-semibold tracking-tight">Workflows</h1>
			<p class="text-xs text-muted-foreground">
				{#if !workflows.isPending && !workflows.isError}{rows.length} in this workspace{:else}Automation flows your product exposes{/if}
			</p>
		</div>
		<Dialog.Root bind:open={createOpen} onOpenChange={(open) => !open && resetCreateDialog()}>
			<Dialog.Trigger>
				{#snippet child({ props })}
					<Button {...props} size="sm">
						<FilePlus2 aria-hidden="true" />
						New workflow
					</Button>
				{/snippet}
			</Dialog.Trigger>
			<Dialog.Content aria-describedby="new-workflow-description">
				<Dialog.Header>
					<Dialog.Title>Start a workflow</Dialog.Title>
					<Dialog.Description id="new-workflow-description">Name the draft now. You can build its first step in the workspace next.</Dialog.Description>
				</Dialog.Header>
				<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void createNewWorkflow(); }}>
					<div class="grid gap-2">
						<label for="workflow-name" class="text-sm font-medium">Workflow name</label>
						<Input id="workflow-name" bind:value={name} placeholder="e.g. Qualify a new lead" aria-invalid={Boolean(createError)} autofocus />
						{#if createError}
							<p role="alert" class="text-sm text-destructive">{createError}</p>
						{/if}
					</div>
					<Dialog.Footer>
						<Button type="button" variant="outline" onclick={() => (createOpen = false)} disabled={creating}>Cancel</Button>
						<Button type="submit" disabled={creating}>{creating ? 'Creating…' : 'Create workflow'}</Button>
					</Dialog.Footer>
				</form>
			</Dialog.Content>
		</Dialog.Root>
	</div>

	{#if activationError}
		<p role="alert" class="mt-4 rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">Activation failed: {activationError}</p>
	{/if}
	<ActivationNotices
		{notices}
		heading={noticesFor ? `${noticesFor} is active, with something still to do` : undefined}
		class="mt-4 overflow-hidden rounded-lg border border-warning/30"
		onDismiss={(key) => (notices = dismissNotice(notices, key))}
	/>

	<div class="mt-4">
		<ListStates
			label="Workflows"
			loading={workflows.isPending}
			failed={workflows.isError}
			error={workflows.error}
			count={rows.length}
			rows={4}
			onRetry={() => void workflows.refetch()}
			emptyIcon={FilePlus2}
			emptyTitle="Build your first flow"
			emptyBody="A workflow starts as a private draft, then grows into the automation your product needs."
		>
			{#snippet emptyAction()}
				<Button class="mt-3" size="sm" onclick={() => (createOpen = true)}>
					<FilePlus2 aria-hidden="true" />
					New workflow
				</Button>
			{/snippet}
			<div class="overflow-hidden rounded-lg border border-border">
				<ul aria-label="Workflows" class="divide-y divide-border">
					{#each rows as workflow (workflow.id)}
						<!-- The activation control is a sibling of the link, not a child
						     of it: a button nested inside an anchor is invalid markup and
						     the row navigates before the click ever reaches it. -->
						<li class="flex items-center transition-colors hover:bg-muted/50">
							<a href={`/app/workflows/${workflow.id}`} class="flex h-11 min-w-0 flex-1 items-center gap-3 px-3 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring">
								<span aria-hidden="true" class="size-1.5 shrink-0 rounded-full {workflow.active ? 'bg-success' : 'bg-muted-foreground/40'}"></span>
								<span class="min-w-0 flex-1 truncate text-[0.8125rem] font-medium">{workflow.name}</span>
								<span class="hidden shrink-0 text-[0.6875rem] text-muted-foreground sm:inline">{workflow.active ? 'Active' : 'Draft'}</span>
								<!-- The dot is the only cue below `sm`, and colour alone is not a cue. -->
								<span class="sr-only sm:hidden">{workflow.active ? 'Active' : 'Draft'}</span>
								<span class="hidden shrink-0 font-mono text-[0.6875rem] text-muted-foreground md:inline">
									<span class="sr-only">Revision </span>r{workflow.latestRevision}
								</span>
								<span class="hidden shrink-0 text-[0.6875rem] text-muted-foreground lg:inline">
									<span class="sr-only">Updated </span>{formatUpdatedAt(workflow.updatedAt)}
								</span>
							</a>
							<button
								type="button"
								class="mr-2 inline-flex h-7 shrink-0 items-center whitespace-nowrap rounded-md border border-border px-2 text-[0.6875rem] font-medium transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-40"
								disabled={togglingID !== null}
								onclick={() => void toggleActivation(workflow)}
							>
								{#if togglingID === workflow.id}
									{workflow.active ? 'Deactivating…' : 'Activating…'}
								{:else}
									{workflow.active ? 'Deactivate' : 'Activate'}
								{/if}
								<span class="sr-only"> {workflow.name}</span>
							</button>
						</li>
					{/each}
				</ul>
			</div>
		</ListStates>
	</div>
</section>
