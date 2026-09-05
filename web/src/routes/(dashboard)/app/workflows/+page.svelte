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
				{#if !workflows.isPending && !workflows.isError}{workflows.data.length} in this workspace{:else}Automation flows your product exposes{/if}
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

	<div class="mt-4">
		{#if workflows.isPending}
			<div aria-live="polite" class="overflow-hidden rounded-lg border border-border">
				<p class="sr-only">Loading workflows…</p>
				{#each Array(4) as _}
					<div class="h-11 animate-pulse border-b border-border bg-muted/50 last:border-0" aria-hidden="true"></div>
				{/each}
			</div>
		{:else if workflows.isError}
			<div class="max-w-lg rounded-lg border border-destructive/25 bg-destructive/5 p-3">
				<h2 class="text-sm font-medium">Workflows could not be loaded</h2>
				<p class="mt-0.5 text-xs leading-5 text-muted-foreground">{message(workflows.error)}</p>
				<Button class="mt-2.5" size="sm" variant="outline" onclick={() => void workflows.refetch()}>
					<RefreshCw aria-hidden="true" />
					Try again
				</Button>
			</div>
		{:else if workflows.data.length === 0}
			<div class="grid min-h-56 place-items-center rounded-lg border border-dashed border-border px-6 py-10 text-center">
				<div class="max-w-xs">
					<div aria-hidden="true" class="mx-auto grid size-8 place-items-center rounded-lg bg-accent text-accent-foreground"><FilePlus2 class="size-4" /></div>
					<h2 class="mt-3 text-sm font-semibold tracking-tight">Build your first flow</h2>
					<p class="mt-1 text-xs leading-5 text-muted-foreground">A workflow starts as a private draft, then grows into the automation your product needs.</p>
					<Button class="mt-3" size="sm" onclick={() => (createOpen = true)}>
						<FilePlus2 aria-hidden="true" />
						New workflow
					</Button>
				</div>
			</div>
		{:else}
			<div class="overflow-hidden rounded-lg border border-border">
				<ul aria-label="Workflows" class="divide-y divide-border">
					{#each workflows.data as workflow (workflow.id)}
						<li>
							<a href={`/app/workflows/${workflow.id}`} class="group flex h-11 items-center gap-3 px-3 transition-colors hover:bg-muted/50 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring">
								<span aria-hidden="true" class="size-1.5 shrink-0 rounded-full {workflow.active ? 'bg-success' : 'bg-muted-foreground/40'}"></span>
								<span class="min-w-0 flex-1 truncate text-[0.8125rem] font-medium">{workflow.name}</span>
								<span class="hidden shrink-0 text-[0.6875rem] text-muted-foreground sm:inline">{workflow.active ? 'Active' : 'Draft'}</span>
								<span class="hidden shrink-0 font-mono text-[0.6875rem] text-muted-foreground md:inline">r{workflow.latestRevision}</span>
								<span class="hidden shrink-0 text-[0.6875rem] text-muted-foreground lg:inline">{formatUpdatedAt(workflow.updatedAt)}</span>
								<MoreHorizontal aria-hidden="true" class="size-3.5 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
							</a>
						</li>
					{/each}
				</ul>
			</div>
		{/if}
	</div>
</section>
