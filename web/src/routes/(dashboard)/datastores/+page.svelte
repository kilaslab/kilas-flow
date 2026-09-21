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
	import * as m from '$lib/paraglide/messages.js';

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
		if (response.status !== 200) throw new Error(m.datastores_error_list_response());
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
			formError = m.datastores_error_name_required();
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
				throw new Error(m.datastores_error_save_response());
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
	<title>{m.datastores_page_title()}</title>
</svelte:head>

<section class="mx-auto w-full max-w-4xl">
	<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
		<div class="max-w-xl">
			<h1 class="text-base font-semibold tracking-tight">{m.nav_datastores()}</h1>
			<p class="text-xs text-muted-foreground">{m.datastores_subtitle()}</p>
		</div>
		<Button onclick={openCreate} class="w-full sm:w-auto sm:shrink-0">
			<Database aria-hidden="true" />
			{m.datastores_new()}
		</Button>
	</div>

	<div class="mt-4">
		<ListStates
			label={m.nav_datastores()}
			loading={loading}
			failed={listFailure !== null}
			error={listFailure}
			count={rows.length}
			rows={2}
			onRetry={() => void loadDatastores()}
			emptyIcon={Database}
			emptyTitle={m.datastores_empty_title()}
			emptyBody={m.datastores_empty_body()}
		>
			<ul aria-label={m.nav_datastores()} class="divide-y divide-border overflow-hidden rounded-lg border border-border">
				{#each rows as datastore (datastore.id)}
					{@const columnCount = (datastore.columns ?? []).length}
					<li class="flex h-11 items-center gap-3 px-3">
						<a
							href={`/datastores/${datastore.id}`}
							class="min-w-0 flex-1 truncate rounded text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2"
						>
							{datastore.name}
						</a>
						<span class="shrink-0 text-xs text-muted-foreground">
							{m.datastores_column_count({ count: columnCount })}
						</span>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							class="size-7 shrink-0"
							aria-label={m.datastores_rename_aria({ name: datastore.name })}
							onclick={() => openRename(datastore)}
						>
							<Pencil aria-hidden="true" class="size-3.5" />
						</Button>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							class="size-7 shrink-0 text-muted-foreground hover:text-destructive"
							aria-label={m.datastores_delete_aria({ name: datastore.name })}
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
				<Dialog.Title>{editing ? m.datastores_rename_title() : m.datastores_new()}</Dialog.Title>
				<Dialog.Description id="datastore-form-description">
					{editing
						? m.datastores_rename_description()
						: m.datastores_create_description()}
				</Dialog.Description>
			</Dialog.Header>
			<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void save(); }}>
				<div class="grid gap-2">
					<label for="datastore-name" class="text-sm font-medium">{m.datastores_name_label()}</label>
					<Input id="datastore-name" bind:value={name} placeholder={m.datastores_name_placeholder()} />
				</div>
				{#if formError}<p role="alert" class="text-sm text-destructive">{formError}</p>{/if}
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => (editorOpen = false)} disabled={saving}>{m.datastores_cancel()}</Button>
					<Button type="submit" disabled={saving}>{saving ? m.datastores_saving() : editing ? m.datastores_rename_title() : m.datastores_create_submit()}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root open={deleting !== null} onOpenChange={(open) => { if (!open) deleting = null; }}>
		<Dialog.Content aria-describedby="datastore-delete-description">
			<Dialog.Header>
				<Dialog.Title>{m.datastores_delete_title({ name: deleting?.name ?? m.datastores_noun() })}</Dialog.Title>
				<Dialog.Description id="datastore-delete-description">
					{m.datastores_delete_description()}
				</Dialog.Description>
			</Dialog.Header>
			{#if formError}<p role="alert" class="text-sm text-destructive">{formError}</p>{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (deleting = null)} disabled={removing}>{m.datastores_cancel()}</Button>
				<Button type="button" variant="destructive" onclick={() => void remove()} disabled={removing}>
					{removing ? m.datastores_deleting() : m.datastores_delete_submit()}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
</section>
