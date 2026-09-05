<script lang="ts">
	import { goto } from '$app/navigation';
	import FilePlus2 from '@lucide/svelte/icons/file-plus-2';
	import MoreHorizontal from '@lucide/svelte/icons/more-horizontal';
	import RefreshCw from '@lucide/svelte/icons/refresh-cw';

	import { ApiError } from '$lib/api/http';
	import { createListWorkflows, createWorkflow } from '$lib/api/generated/workflows/workflows';
	import type { WorkflowDocumentInput, WorkflowSummary } from '$lib/api/generated/models';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';

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

	function message(error: unknown): string {
		if (error instanceof ApiError) return `${error.status} — ${error.message}`;
		return error instanceof Error ? error.message : 'The request could not be completed.';
	}

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

	function formatUpdatedAt(value: string): string {
		const date = new Date(value);
		return Number.isNaN(date.getTime()) ? 'Recently updated' : `Updated ${date.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}`;
	}
</script>

<svelte:head>
	<title>Workflows · KilasFlow</title>
</svelte:head>

<section class="mx-auto w-full max-w-6xl">
	<div class="flex flex-col gap-5 sm:flex-row sm:items-end sm:justify-between">
		<div class="max-w-xl">
			<h1 class="text-balance text-2xl font-semibold tracking-tight sm:text-3xl">Workflows</h1>
			<p class="mt-2 text-pretty text-sm leading-6 text-muted-foreground">Create and maintain the automation flows your product exposes.</p>
		</div>
		<Dialog.Root bind:open={createOpen} onOpenChange={(open) => !open && resetCreateDialog()}>
			<Dialog.Trigger>
				{#snippet child({ props })}
					<Button {...props} class="w-full sm:w-auto">
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

	<div class="mt-8">
		{#if workflows.isPending}
			<div aria-live="polite" class="grid gap-3">
				<p class="text-sm text-muted-foreground">Loading workflows…</p>
				{#each Array(3) as _}
					<div class="h-20 animate-pulse rounded-xl bg-muted" aria-hidden="true"></div>
				{/each}
			</div>
		{:else if workflows.isError}
			<div class="max-w-xl rounded-xl border border-destructive/25 bg-destructive/5 p-5">
				<h2 class="font-medium">Workflows could not be loaded</h2>
				<p class="mt-1 text-sm leading-6 text-muted-foreground">{message(workflows.error)}</p>
				<Button class="mt-4" variant="outline" onclick={() => void workflows.refetch()}>
					<RefreshCw aria-hidden="true" />
					Try again
				</Button>
			</div>
		{:else if workflows.data.length === 0}
			<div class="grid min-h-72 place-items-center rounded-2xl border border-dashed border-border bg-card px-6 py-12 text-center">
				<div class="max-w-sm">
					<div aria-hidden="true" class="mx-auto grid size-11 place-items-center rounded-xl bg-accent text-accent-foreground"><FilePlus2 class="size-5" /></div>
					<h2 class="mt-5 text-lg font-semibold tracking-tight">Build your first flow</h2>
					<p class="mt-2 text-sm leading-6 text-muted-foreground">A workflow starts as a private draft, then grows into the automation your product needs.</p>
					<Button class="mt-5" onclick={() => (createOpen = true)}>
						<FilePlus2 aria-hidden="true" />
						New workflow
					</Button>
				</div>
			</div>
		{:else}
			<div class="overflow-hidden rounded-xl border border-border bg-card">
				<ul aria-label="Workflows" class="divide-y divide-border">
					{#each workflows.data as workflow (workflow.id)}
						<li>
							<a href={`/app/workflows/${workflow.id}`} class="group flex min-h-20 items-center gap-4 px-4 py-3 transition-colors hover:bg-muted/60 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring sm:px-5">
								<div class="grid size-9 shrink-0 place-items-center rounded-lg bg-accent text-xs font-semibold text-accent-foreground">{workflow.name.slice(0, 1).toUpperCase()}</div>
								<div class="min-w-0 flex-1">
									<p class="truncate text-sm font-medium">{workflow.name}</p>
									<p class="mt-1 text-xs text-muted-foreground">{formatUpdatedAt(workflow.updatedAt)} · Revision {workflow.latestRevision}</p>
								</div>
								<span class="hidden rounded-full bg-muted px-2 py-1 text-xs font-medium text-muted-foreground sm:inline">{workflow.active ? 'Active' : 'Draft'}</span>
								<MoreHorizontal aria-hidden="true" class="size-4 text-muted-foreground" />
							</a>
						</li>
					{/each}
				</ul>
			</div>
		{/if}
	</div>
</section>
