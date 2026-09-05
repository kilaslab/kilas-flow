<script lang="ts">
	import Check from '@lucide/svelte/icons/check';
	import History from '@lucide/svelte/icons/history';
	import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
	import Upload from '@lucide/svelte/icons/upload';

	import { getWorkflowVersion, listWorkflowVersions } from '$lib/api/generated/workflows/workflows';
	import { deactivateWorkflow, listWorkflowPublishEvents, publishWorkflowVersion, restoreWorkflowVersion } from '$lib/api/generated/workflow-lifecycle/workflow-lifecycle';
	import type { Document, WorkflowPublishEventResource, WorkflowResource, WorkflowVersionSummaryResource } from '$lib/api/generated/models';
	import { message } from '$lib/api/http';
	import { appendPage, canLoadMore, emptyPage, readPage, type CursorPage } from '$lib/dashboard/cursor-page';
	import * as Sheet from '$lib/components/ui/sheet';
	import * as Tabs from '$lib/components/ui/tabs';
	import { diffChangeCount, diffWorkflowDocuments, describeNodeChange } from '$lib/workflow-editor/history-diff';
	import { validationIssuesFromApiError, type CanvasValidationIssue } from '$lib/workflow-editor/validation';
	import {
		eventRevisionLabel,
		publishConfirmation,
		publishEventSentence,
		publishEventTone,
		publishRefusal,
		relativeTime,
		restoreConfirmation,
		restoreRefusal,
		versionAuthor,
		versionRoles,
		versionTitle
	} from '$lib/workflow-editor/version-history';

	let {
		open = $bindable(false),
		preview = $bindable(null),
		workflowID,
		draft,
		dirty,
		latestVersionID,
		active,
		canRestore = false,
		canPublish = false,
		onRestored,
		onPublished,
		onUnpublished,
		onIssues
	}: {
		open?: boolean;
		/**
		 * The revision on the canvas, shared with the editor.
		 *
		 * Bound rather than mirrored: the editor's preview banner can clear it
		 * too, and a panel holding its own copy of the selection would keep
		 * showing a diff for a revision the canvas had already stopped drawing.
		 */
		preview?: { versionID: string; revision: number; document: Document } | null;
		workflowID: string;
		/** The document on the canvas. Every diff is taken against this. */
		draft: Document;
		/** Whether the canvas has unsaved changes. Restore is refused while it does. */
		dirty: boolean;
		/** The revision the canvas was loaded from, so the newest row can be marked. */
		latestVersionID: string;
		/** Whether the workflow is serving traffic at all right now. */
		active: boolean;
		canRestore?: boolean;
		canPublish?: boolean;
		onRestored: (workflow: WorkflowResource) => void;
		onPublished: (workflow: WorkflowResource, version: WorkflowVersionSummaryResource) => void;
		onUnpublished: (workflow: WorkflowResource) => void;
		/**
		 * Hands a compile failure back to the editor, so a refused publish is
		 * rendered by the same validation-issue list a refused save uses rather
		 * than by a second, slightly different one inside this panel.
		 */
		onIssues: (issues: CanvasValidationIssue[]) => void;
	} = $props();

	type Confirmation = { action: 'restore' | 'publish'; summary: WorkflowVersionSummaryResource };

	let loaded = $state<CursorPage<WorkflowVersionSummaryResource>>(emptyPage());
	let loading = $state(false);
	let loadingMore = $state(false);
	let listError = $state<string | null>(null);
	let loadingDocument = $state(false);
	let documentError = $state<string | null>(null);
	let confirming = $state<Confirmation | null>(null);
	let reason = $state('');
	let busy = $state(false);
	let actionError = $state<string | null>(null);
	let tab = $state('versions');
	let events = $state<WorkflowPublishEventResource[]>([]);
	let eventsError = $state<string | null>(null);
	let loadingEvents = $state(false);
	let listRegion = $state<HTMLDivElement>();
	// Documents are immutable once stored, so a revision fetched once never has
	// to be fetched again — which is what makes flicking between two versions to
	// compare them feel like reading rather than like loading.
	const documents = new Map<string, Document>();
	// Guards against a slow response for a revision the user has already moved
	// off overwriting the one they are looking at now.
	let documentRequest = 0;
	// Deliberately not reactive: the timeline is fetched once per opening, and
	// an effect that read a flag it also writes would re-fire on its own answer
	// — forever, for a workflow whose publish history is empty.
	let eventsRequested = false;
	/** The row that was clicked, kept only as a fallback for the derivation below. */
	let selectedSummary = $state<WorkflowVersionSummaryResource | null>(null);

	// Preferring the freshly listed row keeps the Draft and Published badges
	// honest after a publish; falling back to the row that was clicked keeps a
	// revision selected when a refresh drops it off the first page.
	const selected = $derived(preview ? loaded.items.find((summary) => summary.id === preview?.versionID) ?? selectedSummary : null);
	const selectedDocument = $derived(preview?.document ?? null);
	const diff = $derived(selectedDocument ? diffWorkflowDocuments(selectedDocument, draft) : null);
	const changeCount = $derived(diff ? diffChangeCount(diff) : 0);
	const restoreBlocked = $derived(selected ? restoreRefusal({ canRestore, dirty, isDraft: selected.id === latestVersionID }) : 'Select a revision first.');
	const publishBlocked = $derived(selected ? publishRefusal({ canPublish, isPublished: Boolean(selected.published) && active }) : 'Select a revision first.');
	const servingSummary = $derived(loaded.items.find((summary) => summary.published) ?? null);

	$effect(() => {
		if (!open) return;
		// Reopening after a save must not show the history as it was before it.
		void refresh();
	});

	$effect(() => {
		if (!open || tab !== 'timeline' || eventsRequested) return;
		eventsRequested = true;
		void loadEvents();
	});

	async function refresh() {
		loading = true;
		listError = null;
		try {
			const response = await listWorkflowVersions(workflowID, { limit: 25 });
			if (response.status !== 200) throw new Error('Unexpected workflow-versions response');
			loaded = readPage(response.data);
		} catch (error) {
			listError = message(error);
			loaded = emptyPage();
		} finally {
			loading = false;
		}
	}

	async function loadMore() {
		if (loadingMore || !canLoadMore(loaded)) return;
		loadingMore = true;
		listError = null;
		try {
			const response = await listWorkflowVersions(workflowID, { limit: 25, cursor: loaded.nextCursor });
			if (response.status !== 200) throw new Error('Unexpected workflow-versions response');
			loaded = appendPage(loaded, response.data);
		} catch (error) {
			listError = message(error);
		} finally {
			loadingMore = false;
		}
	}

	async function loadEvents() {
		loadingEvents = true;
		eventsError = null;
		try {
			const response = await listWorkflowPublishEvents(workflowID);
			if (response.status !== 200) throw new Error('Unexpected publish-events response');
			events = response.data ?? [];
		} catch (error) {
			eventsError = message(error);
		} finally {
			loadingEvents = false;
		}
	}

	/** Loads the next page once the list is within a screenful of its end. */
	function onListScroll() {
		if (!listRegion || !canLoadMore(loaded) || loadingMore) return;
		if (listRegion.scrollTop + listRegion.clientHeight >= listRegion.scrollHeight - 240) void loadMore();
	}

	async function select(summary: WorkflowVersionSummaryResource) {
		actionError = null;
		documentError = null;
		selectedSummary = summary;
		const cached = documents.get(summary.id);
		if (cached) {
			preview = { versionID: summary.id, revision: summary.revision, document: cached };
			return;
		}

		const token = ++documentRequest;
		loadingDocument = true;
		try {
			const response = await getWorkflowVersion(workflowID, summary.id);
			if (response.status !== 200) throw new Error('Unexpected workflow-version response');
			// A revision the user has already clicked away from must not land on
			// the canvas behind them.
			if (token !== documentRequest) return;
			documents.set(summary.id, response.data.document);
			preview = { versionID: summary.id, revision: summary.revision, document: response.data.document };
		} catch (error) {
			if (token === documentRequest) documentError = message(error);
		} finally {
			if (token === documentRequest) loadingDocument = false;
		}
	}

	function backToDraft() {
		documentRequest += 1;
		documentError = null;
		loadingDocument = false;
		preview = null;
	}

	function ask(action: Confirmation['action']) {
		if (!selected) return;
		reason = '';
		actionError = null;
		confirming = { action, summary: selected };
	}

	async function confirm() {
		const pending = confirming;
		if (!pending || busy) return;
		busy = true;
		actionError = null;
		onIssues([]);
		const body = reason.trim() ? { reason: reason.trim() } : undefined;
		try {
			if (pending.action === 'restore') {
				const response = await restoreWorkflowVersion(workflowID, pending.summary.id, body);
				if (response.status !== 200) throw new Error('Unexpected workflow-restore response');
				confirming = null;
				// The canvas is about to be rebuilt from the appended revision, so
				// the preview has to be surrendered before the host swaps it out.
				backToDraft();
				open = false;
				onRestored(response.data);
				return;
			}

			const response = await publishWorkflowVersion(workflowID, pending.summary.id, body);
			if (response.status !== 200) throw new Error('Unexpected workflow-publish response');
			confirming = null;
			onPublished(response.data, pending.summary);
			await refresh();
			// The publish that just happened is itself an audit row, so a timeline
			// the user has already looked at would otherwise be missing the entry
			// they came back to check.
			if (eventsRequested) await loadEvents();
		} catch (error) {
			actionError = message(error);
			// A publish is refused with the same structured compiler problem a save
			// is, so the editor renders it in the same list rather than losing the
			// node each issue points at.
			onIssues(validationIssuesFromApiError(error));
		} finally {
			busy = false;
		}
	}

	async function unpublish() {
		if (busy) return;
		busy = true;
		actionError = null;
		onIssues([]);
		try {
			const response = await deactivateWorkflow(workflowID);
			if (response.status !== 200) throw new Error('Unexpected workflow-deactivate response');
			onUnpublished(response.data);
			await refresh();
			if (eventsRequested) await loadEvents();
		} catch (error) {
			actionError = message(error);
		} finally {
			busy = false;
		}
	}
</script>

<Sheet.Root bind:open onOpenChange={(next) => !next && backToDraft()}>
	<Sheet.Content side="right" class="flex w-[min(26rem,92vw)] flex-col gap-0 p-0 sm:max-w-none" aria-label="Version history">
		<Sheet.Header class="shrink-0 gap-1 border-b border-border px-4 py-3 pr-12">
			<Sheet.Title class="flex items-center gap-2 text-sm">
				<History aria-hidden="true" class="size-4 text-muted-foreground" />Version history
			</Sheet.Title>
			<!-- Which revision is live is the fact the whole panel is organised
			     around, so it is stated once here rather than left to be read off
			     the badges further down. -->
			<Sheet.Description class="text-xs">
				{#if active && servingSummary}
					{versionTitle(servingSummary)} is serving traffic.
				{:else if active}
					This workflow is active.
				{:else if servingSummary}
					Nothing is serving — {versionTitle(servingSummary)} was the last published revision.
				{:else}
					Nothing has been published yet.
				{/if}
			</Sheet.Description>
		</Sheet.Header>

		<Tabs.Root bind:value={tab} class="flex min-h-0 flex-1 flex-col">
			<Tabs.List variant="line" class="shrink-0 gap-3 border-b border-border px-4 py-1.5">
				<Tabs.Trigger value="versions">Versions</Tabs.Trigger>
				<Tabs.Trigger value="timeline">Publish timeline</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content value="versions" class="flex min-h-0 flex-1 flex-col">
				<div bind:this={listRegion} onscroll={onListScroll} class="min-h-0 flex-1 overflow-y-auto p-2">
					<!-- The draft heads the list because "what is on my canvas now" is
					     the thing every other row is compared against. -->
					<button
						type="button"
						class="flex w-full items-start gap-2 rounded-lg border px-2.5 py-2 text-left transition-colors {preview === null ? 'border-border bg-muted/60' : 'border-transparent hover:bg-muted/40'}"
						aria-current={preview === null ? 'true' : undefined}
						onclick={backToDraft}
					>
						<span aria-hidden="true" class="mt-1.5 size-1.5 shrink-0 rounded-full {dirty ? 'bg-warning' : 'bg-primary'}"></span>
						<span class="min-w-0 flex-1">
							<span class="block text-xs font-medium">{dirty ? 'Current changes' : 'Current draft'}</span>
							<span class="block truncate text-[0.6875rem] text-muted-foreground">{dirty ? 'Unsaved edits on the canvas' : 'Matches the newest saved revision'}</span>
						</span>
					</button>

					{#if loading}
						<p class="sr-only" aria-live="polite">Loading version history…</p>
						{#each Array(4) as _, index (index)}
							<div class="mt-1 h-12 animate-pulse rounded-lg bg-muted/50" aria-hidden="true"></div>
						{/each}
					{:else if listError && loaded.items.length === 0}
						<div role="alert" class="mt-2 rounded-lg border border-destructive/25 bg-destructive/5 p-3">
							<p class="text-xs text-destructive">{listError}</p>
							<button type="button" class="mt-2 rounded-md border border-border px-2 py-1 text-xs font-medium hover:bg-muted" onclick={() => void refresh()}>Try again</button>
						</div>
					{:else if loaded.items.length === 0}
						<p class="mt-3 px-2 text-xs text-muted-foreground">This workflow has no saved revisions yet.</p>
					{:else}
						<ul class="mt-1 space-y-0.5" aria-label="Saved revisions">
							{#each loaded.items as summary (summary.id)}
								{@const roles = versionRoles(summary)}
								{@const author = versionAuthor(summary)}
								<li>
									<button
										type="button"
										class="flex w-full items-start gap-2 rounded-lg border px-2.5 py-2 text-left transition-colors {selected?.id === summary.id ? 'border-border bg-muted/60' : 'border-transparent hover:bg-muted/40'}"
										aria-current={selected?.id === summary.id ? 'true' : undefined}
										onclick={() => void select(summary)}
									>
										<span aria-hidden="true" class="mt-1.5 size-1.5 shrink-0 rounded-full {summary.published ? 'bg-success' : 'bg-muted-foreground/40'}"></span>
										<span class="min-w-0 flex-1">
											<span class="flex flex-wrap items-center gap-1">
												<span class="text-xs font-medium">{versionTitle(summary)}</span>
												{#each roles as role (role.label)}
													<span class="inline-flex items-center rounded-full border px-1.5 py-px text-[0.625rem] font-medium {role.tone}">{role.label}</span>
												{/each}
											</span>
											<span class="block truncate text-[0.6875rem] text-muted-foreground">
												{relativeTime(summary.createdAt)}{#if author} · {author}{/if}{#if summary.label} · revision {summary.revision}{/if}
											</span>
										</span>
									</button>
								</li>
							{/each}
						</ul>

						{#if canLoadMore(loaded)}
							<button type="button" class="mt-2 w-full rounded-md border border-border px-2 py-1.5 text-xs font-medium hover:bg-muted disabled:opacity-40" disabled={loadingMore} onclick={() => void loadMore()}>
								{loadingMore ? 'Loading…' : 'Load older revisions'}
							</button>
						{/if}
						{#if listError && loaded.items.length > 0}
							<p role="alert" class="mt-2 px-2 text-xs text-destructive">{listError}</p>
						{/if}
					{/if}
				</div>

				{#if selected}
					<div class="max-h-[55%] shrink-0 overflow-y-auto border-t border-border bg-card">
						<div class="px-4 py-3">
							<h3 class="text-xs font-semibold">Compared with the canvas</h3>
							{#if loadingDocument}
								<p class="mt-1 text-xs text-muted-foreground" aria-live="polite">Loading this revision…</p>
							{:else if documentError}
								<p role="alert" class="mt-1 text-xs text-destructive">{documentError}</p>
							{:else if diff}
								{#if diff.identical}
									<p class="mt-1 text-xs text-muted-foreground">Identical to what is on the canvas.</p>
								{:else}
									<p class="mt-1 text-[0.6875rem] text-muted-foreground">
										{changeCount === 1 ? '1 difference' : `${changeCount} differences`}{#if diff.cosmeticOnly}, all of them cosmetic — only where nodes sit{/if}.
									</p>
									<ul class="mt-2 space-y-1 text-xs">
										{#if diff.name}
											<li class="flex gap-1.5"><span class="text-muted-foreground">Workflow</span><span>renamed to “{diff.name.after}”</span></li>
										{/if}
										{#each diff.nodes as change (change.nodeID)}
											<li class="flex gap-1.5">
												<span class="shrink-0 font-medium">{change.name}</span>
												<span class="text-muted-foreground">{describeNodeChange(change)}</span>
											</li>
										{/each}
										{#each diff.connections as change (`${change.status}-${change.connectionID}-${change.sourcePort}-${change.targetPort}`)}
											<li class="flex gap-1.5">
												<span class="shrink-0 text-muted-foreground">Connection</span>
												<span>{change.status === 'added' ? 'added' : 'removed'} — {change.sourceName} → {change.targetName}</span>
											</li>
										{/each}
										{#each diff.settings as change (change.key)}
											<li class="flex gap-1.5">
												<span class="shrink-0 text-muted-foreground">Setting</span>
												<span>{change.key} {change.status}</span>
											</li>
										{/each}
									</ul>
								{/if}
							{/if}

							{#if actionError}
								<p role="alert" class="mt-2 rounded-md border border-destructive/25 bg-destructive/5 px-2 py-1.5 text-xs text-destructive">{actionError}</p>
							{/if}

							<div class="mt-3 flex flex-wrap gap-1.5">
								<button
									type="button"
									class="inline-flex h-7 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs font-medium transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
									disabled={Boolean(restoreBlocked) || busy}
									title={restoreBlocked ?? undefined}
									onclick={() => ask('restore')}
								>
									<RotateCcw aria-hidden="true" class="size-3.5" />Restore
								</button>
								{#if canPublish}
									<button
										type="button"
										class="inline-flex h-7 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs font-medium transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
										disabled={Boolean(publishBlocked) || busy}
										title={publishBlocked ?? undefined}
										onclick={() => ask('publish')}
									>
										<Upload aria-hidden="true" class="size-3.5" />Publish
									</button>
									{#if selected.published && active}
										<button type="button" class="inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium text-warning transition-colors hover:bg-warning/10 disabled:opacity-40" disabled={busy} onclick={() => void unpublish()}>
											Unpublish
										</button>
									{/if}
								{/if}
								<button type="button" class="inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted" onclick={backToDraft}>
									Back to draft
								</button>
							</div>
							{#if restoreBlocked && selected.id !== latestVersionID}
								<p class="mt-1.5 text-[0.6875rem] text-muted-foreground">{restoreBlocked}</p>
							{/if}
						</div>
					</div>
				{/if}
			</Tabs.Content>

			<Tabs.Content value="timeline" class="min-h-0 flex-1 overflow-y-auto p-4">
				{#if loadingEvents}
					<p class="text-xs text-muted-foreground" aria-live="polite">Loading publish history…</p>
				{:else if eventsError}
					<p role="alert" class="text-xs text-destructive">{eventsError}</p>
				{:else if events.length === 0}
					<p class="text-xs text-muted-foreground">Nothing has been published, unpublished or restored yet.</p>
				{:else}
					<ol class="space-y-2.5" aria-label="Publish history">
						{#each events as event, index (`${event.createdAt}-${event.versionId}-${index}`)}
							<li class="flex gap-2">
								<span class="mt-0.5 inline-flex h-4 shrink-0 items-center rounded-full border px-1.5 text-[0.625rem] font-medium {publishEventTone(event)}">{event.action}</span>
								<span class="min-w-0">
									<span class="block text-xs">{publishEventSentence(event, eventRevisionLabel(event.versionId, loaded.items))}</span>
									<span class="block text-[0.6875rem] text-muted-foreground">
										{relativeTime(event.createdAt)}{#if event.actor} · {event.actor}{/if}{#if event.reason} · {event.reason}{/if}
									</span>
								</span>
							</li>
						{/each}
					</ol>
				{/if}
			</Tabs.Content>
		</Tabs.Root>

		{#if confirming}
			{@const pending = confirming}
			<!-- A confirmation inside the sheet rather than a dialog over it: the
			     sheet already traps focus, and stacking a second modal on it is
			     what makes Escape ambiguous. -->
			<div role="alertdialog" aria-modal="true" aria-label={pending.action === 'restore' ? 'Confirm restore' : 'Confirm publish'} class="shrink-0 border-t border-border bg-card px-4 py-3">
				<h3 class="text-xs font-semibold">{pending.action === 'restore' ? 'Restore this revision?' : 'Publish this revision?'}</h3>
				<p class="mt-1 text-[0.6875rem] leading-4 text-muted-foreground">
					{pending.action === 'restore' ? restoreConfirmation(pending.summary) : publishConfirmation(pending.summary)}
				</p>
				<label class="mt-2 block">
					<span class="text-[0.6875rem] font-medium text-muted-foreground">Reason (optional, kept in the audit trail)</span>
					<input bind:value={reason} maxlength="255" class="mt-1 h-7 w-full rounded-md border border-border bg-background px-2 text-xs focus-visible:outline-2 focus-visible:outline-offset-1" placeholder="Rolling back the pricing change" />
				</label>
				<div class="mt-2.5 flex justify-end gap-1.5">
					<button type="button" class="inline-flex h-7 items-center rounded-md px-2.5 text-xs font-medium text-muted-foreground hover:bg-muted" disabled={busy} onclick={() => (confirming = null)}>Cancel</button>
					<button type="button" class="inline-flex h-7 items-center gap-1.5 rounded-md bg-primary px-2.5 text-xs font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:opacity-40" disabled={busy} onclick={() => void confirm()}>
						<Check aria-hidden="true" class="size-3.5" />
						{busy ? 'Working…' : pending.action === 'restore' ? 'Restore' : 'Publish'}
					</button>
				</div>
			</div>
		{/if}
	</Sheet.Content>
</Sheet.Root>
