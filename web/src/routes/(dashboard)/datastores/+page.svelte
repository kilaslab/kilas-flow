<script lang="ts">
	import { onMount } from 'svelte';
	import Database from '@lucide/svelte/icons/database';
	import Pencil from '@lucide/svelte/icons/pencil';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import { message } from '$lib/api/http';
	import {
		createDatastore,
		deleteDatastore,
		listDatastores,
		renameDatastore
	} from '$lib/api/generated/datastores/datastores';
	import type { DatastoreResource } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { DRAIN_PAGE_LIMIT, drainPages, headerCursor, readPage, type CursorPage } from '$lib/dashboard/cursor-page';
	import { RequestGuard } from '$lib/dashboard/request-guard';

	// The listing is paged by the server, so one request is only its first page:
	// the rows are read to the end before they are shown, or a datastore past it
	// would be unreachable from this page entirely.
	let rows = $state<DatastoreResource[]>([]);
	let loading = $state(true);
	let listFailure = $state<unknown>(null);
	const listGuard = new RequestGuard();

	/** One page of the datastore listing: rows from the body, cursor from the header. */
	async function fetchDatastorePage(cursor: string): Promise<CursorPage<DatastoreResource>> {
		const response = await listDatastores({ limit: DRAIN_PAGE_LIMIT, cursor: cursor || undefined });
		if (response.status !== 200) throw new Error('Unexpected datastore-list response');
		return readPage({ items: response.data.items, nextCursor: headerCursor(response.headers) });
	}

	async function loadDatastores() {
		const token = listGuard.start();
		listFailure = null;
		loading = rows.length === 0;
		try {
			const items = await drainPages(fetchDatastorePage);
			if (!listGuard.holds(token)) return;
			rows = items;
		} catch (cause) {
			if (!listGuard.holds(token)) return;
			listFailure = cause;
		} finally {
			if (listGuard.holds(token)) loading = false;
		}
	}

	onMount(() => void loadDatastores());

	let editorOpen = $state(false);
	let editing = $state<DatastoreResource | null>(null);
	let name = $state('');
	let saving = $state(false);
	let formError = $state<string | null>(null);
	let deleting = $state<DatastoreResource | null>(null);
	let removing = $state(false);


	function openCreate() {
		editing = null;
		name = '';
		formError = null;
		editorOpen = true;
	}

	function openRename(datastore: DatastoreResource) {
		editing = datastore;
		name = datastore.name;
		formError = null;
		editorOpen = true;
	}

	async function save() {
		if (!name.trim()) {
			formError = 'Give this datastore a name.';
			return;
		}
		saving = true;
		formError = null;
		try {
			const body = { name: name.trim() };
			const response = editing
				? await renameDatastore(editing.id, body)
				: await createDatastore(body);
			if (response.status !== 200 && response.status !== 201)
				throw new Error('Unexpected datastore-save response');
			await loadDatastores();
			editorOpen = false;
		} catch (error) {
			formError = message(error);
		} finally {
			saving = false;
		}
	}

	async function remove() {
		if (!deleting) return;
		removing = true;
		formError = null;
		try {
			await deleteDatastore(deleting.id);
			deleting = null;
			await loadDatastores();
		} catch (error) {
			formError = message(error);
		} finally {
			removing = false;
		}
	}
</script>

<svelte:head>
	<title>Datastores · KilasFlow</title>
</svelte:head>

<section class="mx-auto w-full max-w-4xl">
	<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
		<div class="max-w-xl">
			<h1 class="text-base font-semibold tracking-tight">Datastores</h1>
			<p class="text-xs text-muted-foreground">
				Structured tables your workflows can read and write, beside the workflows and
				credentials that use them.
			</p>
		</div>
		<Button onclick={openCreate} class="w-full sm:w-auto sm:shrink-0">
			<Database aria-hidden="true" />
			New datastore
		</Button>
	</div>

	<div class="mt-4">
		<ListStates
			label="Datastores"
			loading={loading}
			failed={listFailure !== null}
			error={listFailure}
			count={rows.length}
			rows={2}
			onRetry={() => void loadDatastores()}
			emptyIcon={Database}
			emptyTitle="No datastores yet"
			emptyBody="Use datastores to persist execution results, share data between workflows, and track metrics for evaluation."
		>
			<ul aria-label="Datastores" class="divide-y divide-border overflow-hidden rounded-lg border border-border">
				{#each rows as datastore (datastore.id)}
					<li class="flex h-11 items-center gap-3 px-3">
						<a
							href={`/datastores/${datastore.id}`}
							class="min-w-0 flex-1 truncate rounded text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2"
						>
							{datastore.name}
						</a>
						<span class="shrink-0 text-xs text-muted-foreground">
							{(datastore.columns ?? []).length} column{(datastore.columns ?? []).length === 1 ? '' : 's'}
						</span>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							class="size-7 shrink-0"
							aria-label={`Rename ${datastore.name}`}
							onclick={() => openRename(datastore)}
						>
							<Pencil aria-hidden="true" class="size-3.5" />
						</Button>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							class="size-7 shrink-0 text-muted-foreground hover:text-destructive"
							aria-label={`Delete ${datastore.name}`}
							onclick={() => {
								formError = null;
								deleting = datastore;
							}}
						>
							<Trash2 aria-hidden="true" class="size-3.5" />
						</Button>
					</li>
				{/each}
			</ul>
		</ListStates>
	</div>

	<Dialog.Root bind:open={editorOpen}>
		<Dialog.Content aria-describedby="datastore-form-description">
			<Dialog.Header>
				<Dialog.Title>{editing ? 'Rename datastore' : 'New datastore'}</Dialog.Title>
				<Dialog.Description id="datastore-form-description">
					{editing
						? 'Columns keep their names; only the table is renamed.'
						: 'A name is all it takes — columns are added afterwards.'}
				</Dialog.Description>
			</Dialog.Header>
			<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void save(); }}>
				<div class="grid gap-2">
					<label for="datastore-name" class="text-sm font-medium">Name</label>
					<Input id="datastore-name" bind:value={name} placeholder="e.g. Evaluation scores" />
				</div>
				{#if formError}<p role="alert" class="text-sm text-destructive">{formError}</p>{/if}
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => (editorOpen = false)} disabled={saving}>Cancel</Button>
					<Button type="submit" disabled={saving}>{saving ? 'Saving…' : editing ? 'Rename datastore' : 'Create datastore'}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root open={deleting !== null} onOpenChange={(open) => { if (!open) deleting = null; }}>
		<Dialog.Content aria-describedby="datastore-delete-description">
			<Dialog.Header>
				<Dialog.Title>Delete {deleting?.name ?? 'datastore'}?</Dialog.Title>
				<Dialog.Description id="datastore-delete-description">
					This removes the table and every row it holds. Cancelling sends no request
					and leaves the datastore untouched.
				</Dialog.Description>
			</Dialog.Header>
			{#if formError}<p role="alert" class="text-sm text-destructive">{formError}</p>{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (deleting = null)} disabled={removing}>Cancel</Button>
				<Button type="button" variant="destructive" onclick={() => void remove()} disabled={removing}>
					{removing ? 'Deleting…' : 'Delete datastore'}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
</section>
