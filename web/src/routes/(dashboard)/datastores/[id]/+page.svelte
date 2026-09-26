<script lang="ts">
	import { untrack } from 'svelte';

	import { page } from '$app/state';
	import ArrowLeft from '@lucide/svelte/icons/arrow-left';
	import Download from '@lucide/svelte/icons/download';
	import Pencil from '@lucide/svelte/icons/pencil';
	import Plus from '@lucide/svelte/icons/plus';
	import Trash2 from '@lucide/svelte/icons/trash-2';
	import Upload from '@lucide/svelte/icons/upload';

	import { message } from '$lib/api/http';
	import { getDatastore } from '$lib/api/generated/datastores/datastores';
	import {
		addDatastoreColumn,
		deleteDatastoreColumn,
		renameDatastoreColumn
	} from '$lib/api/generated/datastore-columns/datastore-columns';
	import {
		deleteDatastoreRows,
		insertDatastoreRow,
		listDatastoreRows,
		updateDatastoreRows
	} from '$lib/api/generated/datastore-rows/datastore-rows';
	import type {
		DatastoreColumnResource,
		DatastoreResource,
		RowListOutputBodyItemsItem
	} from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import SyncedCheckbox from '$lib/components/dashboard/synced-checkbox.svelte';
	import { Input } from '$lib/components/ui/input';
	import * as Table from '$lib/components/ui/table';
	import {
		appendPage,
		canLoadMore,
		emptyPage,
		readPage,
		type CursorPage
	} from '$lib/dashboard/cursor-page';
	import { failedBesideRows } from '$lib/dashboard/list-state';
	import { RequestGuard } from '$lib/dashboard/request-guard';
	import {
		COLUMN_TYPES,
		cellButtonLabel,
		coerceValue,
		displayType,
		gridColumns,
		isSystemColumn,
		wireType,
		type GridColumn
	} from '$lib/datastore/columns';
	import { formatTimestamp } from '$lib/workflow-editor/execution';
	import {
		downloadExport,
		severityLabel,
		summarizeReport,
		uploadImport,
		type CSVImportReport
	} from '$lib/datastore/transfer';
	import * as m from '$lib/paraglide/messages.js';

	const PAGE_SIZE = 20;

	const id = $derived(page.params.id ?? '');

	let detail = $state<DatastoreResource | null>(null);
	let detailFailure = $state<unknown>(null);
	let detailLoading = $state(true);

	let rows = $state<CursorPage<RowListOutputBodyItemsItem>>(emptyPage());
	let loading = $state(true);
	let loadingMore = $state(false);
	let failure = $state<unknown>(null);

	// Row selection is by id: the system column every datastore carries. A
	// record rather than a Set, because the checkbox primitive declares its
	// state `bindable` and a row's membership has no lvalue in a Set.
	let checkState = $state<Record<string, boolean>>({});
	let allChecked = $state(false);
	const pageIDs = $derived(rows.items.map((row) => String(row.id)));
	const selectedIDs = $derived(pageIDs.filter((rowID) => checkState[rowID] === true));

	let rowDialogOpen = $state(false);
	let draft = $state<Record<string, string>>({});
	let boolDraft = $state<Record<string, boolean>>({});
	let rowError = $state<string | null>(null);
	let savingRow = $state(false);

	let columnDialogOpen = $state(false);
	let columnName = $state('');
	let columnType = $state<string>(COLUMN_TYPES[0]);
	let columnError = $state<string | null>(null);
	let savingColumn = $state(false);

	let renaming = $state<string | null>(null);
	let renameValue = $state('');
	const headers = $derived<GridColumn[]>(gridColumns(detail?.columns ?? []));
	const userColumns = $derived<DatastoreColumnResource[]>(
		(detail?.columns ?? []).filter((column) => !isSystemColumn(column.name))
	);
	let search = $state('');
	let sortColumn = $state('');
	let sortDirection = $state<'asc' | 'desc'>('asc');
	let editingCell = $state<{ rowID: string; column: string } | null>(null);
	let cellDraft = $state('');
	let cellError = $state<string | null>(null);
	let savingCell = $state(false);
	const guard = new RequestGuard();

	const pagingFailure = $derived(
		failedBesideRows({ loading, failed: failure !== null, count: rows.items.length })
	);

	let headerError = $state<string | null>(null);
	let deletingColumn = $state<string | null>(null);
	let removingColumn = $state(false);
	let confirmingRows = $state(false);
	let removingRows = $state(false);
	let transfer = $state<'import' | 'export' | null>(null);
	let transferFile = $state<File | null>(null);
	let transferBusy = $state(false);
	let transferError = $state<string | null>(null);
	let transferReport = $state<CSVImportReport | null>(null);
	let includeSystem = $state(false);
	let savedFilename = $state<string | null>(null);
	// Rows drive the header box: when every row on the page is checked the
	// header follows, and when one is cleared it follows that too. The other
	// direction — header driving the rows — is a user gesture handled in
	// toggleAll, not here, so the two never chase each other.
	$effect(() => {
		allChecked = pageIDs.length > 0 && pageIDs.every((rowID) => checkState[rowID] === true);
	});

	// Keyed on `id` alone. `loadDetail` and `load` read `detail`, `search` and
	// `userColumns` themselves, and `load`'s own dependency (`listParams`)
	// only shows up once a search is typed — so tracking those reads here
	// turned every `loadDetail` response into a re-run of this same effect,
	// which called `load` again, forever (BUG-rytwy7). `untrack` keeps their
	// reads out of this effect's dependency set; this is the pattern at
	// executions/[id]/+page.svelte:119-127.
	//
	// SvelteKit reuses this component instance across a param-only
	// navigation (there is no `{#key id}` wrapper), so an `id` change runs
	// this effect again on the *same* `detail`/`search` state the previous
	// table left behind. Without resetting them, `detailLoading` was already
	// false and `detail` still held the old resource, so the previous
	// table's name and columns stayed on screen — skeleton skipped — until
	// the new detail response landed, and a leftover search term could go on
	// filtering a table it was never typed against. Resetting both here
	// makes an `id` change look like a fresh visit.
	$effect(() => {
		const current = id;
		detail = null;
		search = '';
		untrack(() => {
			void loadDetail(current);
			void load(current);
		});
	});

	// Debounced search: settles 250 ms after the last change to `search`
	// before firing one row query. Tracks the id the debounce last saw
	// rather than a one-shot "have I ever run" flag, because the effect
	// above reuses this component across an `id` change and already issues
	// its own immediate `load` for the new id — a one-shot flag would only
	// skip the very first mount, so every later `id` change queued this
	// effect's 250 ms timer as a second, redundant fetch on top of that
	// immediate one.
	let lastSearchID: string | null = null;
	$effect(() => {
		const current = id;
		void search;
		if (lastSearchID !== current) {
			lastSearchID = current;
			return;
		}
		const timer = setTimeout(() => {
			untrack(() => void load(current));
		}, 250);
		return () => clearTimeout(timer);
	});

	async function loadDetail(datastoreID: string) {
		// A refetch (column add/rename/delete, "Try again") never flips this:
		// only the first load, while `detail` is still null, shows the
		// skeleton. Flipping it unconditionally used to unmount the whole
		// body — search box included — on every background refresh.
		if (detail === null) detailLoading = true;
		detailFailure = null;
		try {
			const response = await getDatastore(datastoreID);
			if (response.status !== 200) throw new Error(m.datastores_error_datastore_response());
			detail = response.data;
		} catch (cause) {
			detailFailure = cause;
			detail = null;
		} finally {
			detailLoading = false;
		}
	}
	async function load(datastoreID: string) {
		const token = guard.start();
		loading = true;
		failure = null;
		checkState = {};
		allChecked = false;
		try {
			const response = await listDatastoreRows(datastoreID, listParams());
			if (response.status !== 200) throw new Error(m.datastores_error_row_list_response());
			if (!guard.holds(token)) return;
			rows = readPage(response.data);
		} catch (cause) {
			if (!guard.holds(token)) return;
			failure = cause;
			rows = emptyPage();
		} finally {
			if (guard.holds(token)) loading = false;
		}
	}

	/** Server-side text filter plus client-side column sort. The list API
	 * carries one filter triple per user column, so a search fans out as an
	 * OR across every text column; sorting stays client-side because the
	 * list endpoint has no order parameter. */
	function listParams(): { limit: number; match?: string; columnName?: string[]; condition?: string[]; value?: string[] } {
		const trimmed = search.trim();
		if (!trimmed) return { limit: PAGE_SIZE };
		const textColumns = userColumns.filter((column) => column.type === 'text' || column.type === 'string');
		const targets = textColumns.length > 0 ? textColumns : userColumns;
		if (targets.length === 0) return { limit: PAGE_SIZE };
		// The store speaks ilike, not contains: a wrapped %pattern% keeps the
		// search a substring match on both drivers.
		return {
			limit: PAGE_SIZE,
			match: 'any',
			columnName: targets.map((column) => column.name),
			condition: targets.map(() => 'ilike'),
			value: targets.map(() => JSON.stringify(`%${trimmed}%`))
		};
	}

	const visibleItems = $derived.by(() => {
		const items = [...rows.items];
		if (!sortColumn) return items;
		const direction = sortDirection === 'asc' ? 1 : -1;
		return items.sort((a, b) => {
			const left = a[sortColumn];
			const right = b[sortColumn];
			if (left === null || left === undefined) return 1;
			if (right === null || right === undefined) return -1;
			if (typeof left === 'number' && typeof right === 'number') return (left - right) * direction;
			return String(left).localeCompare(String(right)) * direction;
		});
	});

	function toggleSort(column: string) {
		if (sortColumn !== column) {
			sortColumn = column;
			sortDirection = 'asc';
			return;
		}
		sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
	}

	function startCellEdit(rowID: string, column: GridColumn, current: unknown) {
		if (column.system || isSystemColumn(column.name)) return;
		editingCell = { rowID, column: column.name };
		cellDraft = current === null || current === undefined ? '' : String(current);
		cellError = null;
	}

	function cancelCellEdit() {
		editingCell = null;
		cellDraft = '';
		cellError = null;
	}

	async function commitCellEdit(column: GridColumn) {
		const targetCell = editingCell;
		if (!targetCell || savingCell) return;
		const target = rows.items.find((row) => String(row.id) === targetCell.rowID);
		if (!target) {
			cancelCellEdit();
			return;
		}
		const declaration = userColumns.find((candidate) => candidate.name === column.name);
		let next: unknown = cellDraft;
		if (declaration && declaration.type === 'boolean') {
			next = cellDraft.trim().toLowerCase() === 'true';
		} else if (declaration) {
			if (cellDraft.trim() === '') next = null;
			else {
				const coerced = coerceValue(declaration.type, declaration.name, cellDraft);
				if (!coerced.ok) {
					cellError = coerced.error;
					return;
				}
				next = coerced.value;
			}
		}
		savingCell = true;
		cellError = null;
	try {
		const response = await updateDatastoreRows(id, {
			filter: { type: 'and', filters: [{ columnName: 'id', condition: 'eq', value: Number(targetCell.rowID) }] },
			values: { [column.name]: next }
		});
			if (response.status !== 200) throw new Error(m.datastores_error_row_update_response());
			cancelCellEdit();
			await load(id);
		} catch (error) {
			cellError = message(error);
		} finally {
			savingCell = false;
		}
	}

	async function loadMore() {
		if (!canLoadMore(rows) || loadingMore) return;
		// Joins the request already in flight rather than starting a new one:
		// starting would supersede that request, which would then discard its
		// own answer and leave the page pinned in its loading state.
		const token = guard.current;
		loadingMore = true;
		failure = null;
		try {
			const response = await listDatastoreRows(id, { ...listParams(), cursor: rows.nextCursor });
			if (response.status !== 200) throw new Error(m.datastores_error_row_list_response());
			if (!guard.holds(token)) return;
			rows = appendPage(rows, response.data);
		} catch (cause) {
			if (guard.holds(token)) failure = cause;
		} finally {
			loadingMore = false;
		}
	}

	async function refresh() {
		await loadDetail(id);
		await load(id);
	}

	function openRowDialog() {
		draft = Object.fromEntries(userColumns.map((column) => [column.name, '']));
		boolDraft = Object.fromEntries(
			userColumns.filter((column) => column.type === 'boolean').map((column) => [column.name, false])
		);
		rowError = null;
		rowDialogOpen = true;
	}

	async function saveRow() {
		const values: Record<string, unknown> = {};
		for (const column of userColumns) {
			if (column.type === 'boolean') {
				values[column.name] = boolDraft[column.name] ?? false;
				continue;
			}
		const coerced = coerceValue(column.type, column.name, draft[column.name] ?? '');
		// An empty string means "leave it Null" rather than "store a
		// wrongly-typed zero": the grid shows Null for missing cells.
		if ((draft[column.name] ?? '').trim() === '') continue;
		if (!coerced.ok) {
			rowError = coerced.error;
			return;
		}
		values[column.name] = coerced.value;
		}
		savingRow = true;
		rowError = null;
		try {
			const response = await insertDatastoreRow(id, { values });
			if (response.status !== 201) throw new Error(m.datastores_error_row_insert_response());
			rowDialogOpen = false;
			await load(id);
		} catch (error) {
			rowError = message(error);
		} finally {
			savingRow = false;
		}
	}

	async function saveColumn() {
		if (!columnName.trim()) {
			columnError = m.datastores_error_column_name_required();
			return;
		}
		savingColumn = true;
		columnError = null;
		try {
			const response = await addDatastoreColumn(id, {
				name: columnName.trim(),
				type: wireType(columnType)
			});
			if (response.status !== 200) throw new Error(m.datastores_error_column_add_response());
			columnDialogOpen = false;
			columnName = '';
			columnType = COLUMN_TYPES[0];
			await refresh();
		} catch (error) {
			columnError = message(error);
		} finally {
			savingColumn = false;
		}
	}

	function startRename(column: GridColumn) {
		renaming = column.name;
		renameValue = column.name;
		headerError = null;
	}

	function cancelRename() {
		renaming = null;
		renameValue = '';
	}

	async function commitRename(column: GridColumn) {
		const next = renameValue.trim();
		if (!next || next === column.name) {
			cancelRename();
			return;
		}
		try {
			const response = await renameDatastoreColumn(id, column.name, { name: next });
			if (response.status !== 200) throw new Error(m.datastores_error_column_rename_response());
			cancelRename();
			await refresh();
		} catch (error) {
			headerError = message(error);
		}
	}

	async function removeColumn() {
		if (!deletingColumn) return;
		removingColumn = true;
		headerError = null;
		try {
			const response = await deleteDatastoreColumn(id, deletingColumn);
			if (response.status !== 204) throw new Error(m.datastores_error_column_delete_response());
			deletingColumn = null;
			await refresh();
		} catch (error) {
			headerError = message(error);
		} finally {
			removingColumn = false;
		}
	}

	async function removeRows() {
		const targets = selectedIDs;
		if (targets.length === 0) return;
		removingRows = true;
		try {
			const response = await deleteDatastoreRows(id, {
				filter: {
					type: 'or',
					filters: targets.map((rowID) => ({
						columnName: 'id',
						condition: 'eq',
						value: Number(rowID)
					}))
				}
			});
			if (response.status !== 200) throw new Error(m.datastores_error_row_delete_response());
			confirmingRows = false;
			await load(id);
		} catch (error) {
			headerError = message(error);
		} finally {
			removingRows = false;
		}
	}

	// The transfer dialog opens fresh every time: a stale report or filename
	// beside a new file picks the wrong story to tell.
	function openTransfer(mode: 'import' | 'export') {
		transfer = mode;
		transferFile = null;
		transferBusy = false;
		transferError = null;
		transferReport = null;
		includeSystem = false;
		savedFilename = null;
	}

	async function runExport() {
		transferBusy = true;
		transferError = null;
		savedFilename = null;
		try {
			const saved = await downloadExport(id, includeSystem);
			savedFilename = saved.filename;
		} catch (error) {
			transferError = message(error);
		} finally {
			transferBusy = false;
		}
	}

	async function runImport() {
		if (!transferFile) {
			transferError = m.datastores_error_choose_file();
			return;
		}
		transferBusy = true;
		transferError = null;
		transferReport = null;
		try {
			const report = await uploadImport(id, transferFile);
			transferReport = report;
			// A refused file imports nothing, so only a clean report
			// refreshes the grid underneath the dialog.
			if (report.failed.length === 0) await load(id);
		} catch (error) {
			transferError = message(error);
		} finally {
			transferBusy = false;
		}
	}

	// A header-box gesture: the rows follow the box the user just set, and
	// the sync effect above leaves the box alone once they agree.
	function toggleAll(checked: boolean) {
		if (checked) {
			checkState = Object.fromEntries(pageIDs.map((rowID) => [rowID, true]));
			allChecked = true;
		} else {
			checkState = {};
			allChecked = false;
		}
	}

	function cellText(row: RowListOutputBodyItemsItem, column: GridColumn): string {
		const value = row[column.name];
		if (value === null || value === undefined) return '';
		if (column.name === 'createdAt' || column.name === 'updatedAt') {
			return typeof value === 'string' ? formatTimestamp(value) : String(value);
		}
		return String(value);
	}

	function isNullCell(row: RowListOutputBodyItemsItem, column: GridColumn): boolean {
		return row[column.name] === null || row[column.name] === undefined;
	}
</script>

<svelte:head>
	<title>{m.datastores_detail_page_title({ name: detail?.name ?? m.datastores_noun() })}</title>
</svelte:head>

<section class="mx-auto w-full max-w-5xl">
	<a
		href="/datastores"
		class="inline-flex items-center gap-1.5 rounded text-xs text-muted-foreground underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2"
	>
		<ArrowLeft aria-hidden="true" class="size-3.5" />{m.nav_datastores()}
	</a>

	{#if detailLoading}
		<div aria-live="polite" class="mt-3 overflow-hidden rounded-lg border border-border">
			<p class="sr-only">{m.common_loading({ label: m.datastores_noun() })}</p>
			<div class="h-11 animate-pulse bg-muted/50" aria-hidden="true"></div>
		</div>
	{:else if detailFailure || !detail}
		<div class="mt-3 max-w-lg rounded-lg border border-destructive/25 bg-destructive/5 p-3">
			<p class="text-xs font-medium text-destructive">{m.datastores_load_failed()}</p>
			<p class="mt-0.5 text-xs leading-5 text-muted-foreground">{message(detailFailure)}</p>
			<Button class="mt-2.5" size="sm" variant="outline" onclick={() => void loadDetail(id)}>
				{m.common_try_again()}
			</Button>
		</div>
	{:else}
		<div class="mt-2 flex flex-wrap items-center justify-between gap-3">
			<div class="max-w-xl">
				<h1 class="text-base font-semibold tracking-tight">{detail.name}</h1>
				<p class="text-xs text-muted-foreground">
					{m.datastores_columns_summary({ count: userColumns.length })}
				</p>
			</div>
			<div class="flex flex-wrap gap-2">
				<Button variant="outline" size="sm" onclick={() => openTransfer('import')}>
					<Upload aria-hidden="true" />{m.datastores_import()}
				</Button>
				<Button variant="outline" size="sm" onclick={() => openTransfer('export')}>
					<Download aria-hidden="true" />{m.datastores_export()}
				</Button>
				<Button size="sm" onclick={openRowDialog}>
					<Plus aria-hidden="true" />{m.datastores_add_row()}
				</Button>
				<Button
					size="sm"
					variant="outline"
					onclick={() => {
						columnName = '';
						columnType = COLUMN_TYPES[0];
						columnError = null;
						columnDialogOpen = true;
					}}
				>
					<Plus aria-hidden="true" />{m.datastores_add_column()}
				</Button>
			</div>
		</div>

		{#if selectedIDs.length > 0}
			<div class="mt-3 flex flex-wrap items-center gap-3 rounded-lg border border-border bg-muted/40 px-3 py-2">
				<p class="text-xs text-muted-foreground" role="status">
					{m.datastores_rows_selected({ count: selectedIDs.length })}
				</p>
				<Button type="button" size="sm" variant="outline" onclick={() => (confirmingRows = true)}>
					<Trash2 aria-hidden="true" />{m.datastores_delete_selected()}
				</Button>
				<Button type="button" size="sm" variant="ghost" onclick={() => { checkState = {}; allChecked = false; }}>
					{m.datastores_clear_selection()}
				</Button>
			</div>
		{/if}

		{#if headerError}
			<p role="alert" class="mt-3 text-xs text-destructive">{headerError}</p>
		{/if}

		<div class="mt-3">
			<div class="mb-3 flex flex-wrap items-center gap-2">
				<label for="datastore-search" class="sr-only">{m.datastores_search_rows()}</label>
				<Input
					id="datastore-search"
					value={search}
					placeholder={m.datastores_search_placeholder()}
					class="h-7 max-w-64 text-xs"
					oninput={(event) => (search = event.currentTarget.value)}
				/>
				<p class="text-xs text-muted-foreground" role="status">
					{m.datastores_rows_on_page({ count: rows.items.length })}
				</p>
				{#if cellError}<p role="alert" class="text-xs text-destructive">{cellError}</p>{/if}
			</div>
			<ListStates
				label={m.datastores_rows()}
				{loading}
				failed={failure !== null}
				error={failure}
				count={rows.items.length}
				onRetry={() => void load(id)}
				onRetryMore={() => void loadMore()}
				emptyIcon={Plus}
				emptyTitle={m.datastores_empty_rows_title()}
				emptyBody={m.datastores_empty_rows_body()}
			>
				<div class="overflow-hidden rounded-xl border border-border bg-card">
					<Table.Root class="min-w-[44rem]">
						<Table.Caption class="sr-only">{m.datastores_rows_caption({ name: detail.name })}</Table.Caption>
						<Table.Header class="[&_th]:h-7 [&_th]:px-3 [&_th]:text-xs [&_th]:uppercase [&_th]:tracking-wide [&_th]:text-muted-foreground">
							<Table.Row>
								<Table.Head scope="col" class="w-8">
									<SyncedCheckbox
										label={m.datastores_select_all_rows()}
										checked={allChecked}
										onToggle={(next) => toggleAll(next)}
									/>
								</Table.Head>
								{#each headers as column (column.name)}
									<Table.Head scope="col">
										<span class="inline-flex items-center gap-1.5 normal-case">
											<button
												type="button"
												class="rounded font-medium hover:underline focus-visible:outline-2 focus-visible:outline-offset-1"
												aria-label={sortColumn === column.name && sortDirection === 'asc'
													? m.datastores_sort_by_descending({ column: column.name })
													: m.datastores_sort_by_ascending({ column: column.name })}
												title={m.datastores_sort_by({ column: column.name })}
												onclick={() => toggleSort(column.name)}
											>
												{column.name}{sortColumn === column.name ? (sortDirection === 'asc' ? ' ▲' : ' ▼') : ''}
											</button>
											{#if renaming === column.name}
												<Input
													class="h-6 w-32 text-xs normal-case"
													value={renameValue}
													aria-label={m.datastores_rename_column_aria({ column: column.name })}
													oninput={(event) => (renameValue = event.currentTarget.value)}
													onkeydown={(event) => {
														if (event.key === 'Enter') void commitRename(column);
														if (event.key === 'Escape') cancelRename();
													}}
													onblur={() => {
														if (renaming === column.name) void commitRename(column);
													}}
												/>
											{:else}
												<span class="font-medium">{column.name}</span>
												{#if !column.system}
													<span class="font-normal text-muted-foreground">· {displayType(column.type)}</span>
													<Button
														type="button"
														variant="ghost"
														size="icon"
														class="size-5"
														aria-label={m.datastores_rename_column_aria({ column: column.name })}
														onclick={() => startRename(column)}
													>
														<Pencil aria-hidden="true" class="size-3" />
													</Button>
													<Button
														type="button"
														variant="ghost"
														size="icon"
														class="size-5 text-muted-foreground hover:text-destructive"
														aria-label={m.datastores_delete_column_aria({ column: column.name })}
														onclick={() => {
															headerError = null;
															deletingColumn = column.name;
														}}
													>
														<Trash2 aria-hidden="true" class="size-3" />
													</Button>
												{/if}
											{/if}
										</span>
									</Table.Head>
								{/each}
							</Table.Row>
						</Table.Header>
						<Table.Body>
							{#each visibleItems as row (row.id)}
								{@const rowID = String(row.id)}
								<Table.Row>
									<Table.Cell class="px-3 py-2">
										<SyncedCheckbox
											label={m.datastores_select_row_aria({ row: rowID })}
											checked={checkState[rowID] === true}
											onToggle={(next) => (checkState = { ...checkState, [rowID]: next })}
										/>
									</Table.Cell>
									{#each headers as column (column.name)}
										{@const editing = editingCell?.rowID === rowID && editingCell?.column === column.name}
										<Table.Cell class="max-w-56 truncate px-3 py-2 tabular-nums">
											{#if editing}
												<Input
													value={cellDraft}
													aria-label={m.datastores_edit_cell_aria({ column: column.name, row: rowID })}
													class="h-6 text-xs"
													disabled={savingCell}
													oninput={(event) => (cellDraft = event.currentTarget.value)}
													onkeydown={(event) => {
														if (event.key === 'Enter') void commitCellEdit(column);
														if (event.key === 'Escape') cancelCellEdit();
													}}
													onblur={() => {
														if (editingCell?.rowID === rowID) void commitCellEdit(column);
													}}
												/>
											{:else if isNullCell(row, column)}
												<button type="button" title={column.system ? m.datastores_cell_readonly_title() : m.datastores_cell_empty_title()} aria-label={cellButtonLabel({ column: column.name, system: column.system, rowID, text: cellText(row, column), empty: isNullCell(row, column) })} class="text-muted-foreground {column.system ? '' : 'hover:underline'}" onclick={() => startCellEdit(rowID, column, row[column.name])} disabled={column.system}>
													<span>{m.datastores_null()}</span>
												</button>
											{:else}
												<button type="button" title={column.system ? cellText(row, column) : m.datastores_cell_value_title({ value: cellText(row, column) })} aria-label={cellButtonLabel({ column: column.name, system: column.system, rowID, text: cellText(row, column), empty: isNullCell(row, column) })} class="max-w-full truncate {column.system ? 'cursor-default' : 'hover:underline'}" onclick={() => startCellEdit(rowID, column, row[column.name])} disabled={column.system}>
													{cellText(row, column)}
												</button>
											{/if}
										</Table.Cell>
									{/each}
								</Table.Row>
							{/each}
						</Table.Body>
					</Table.Root>
				</div>
				{#if canLoadMore(rows) && !pagingFailure}
					<div class="mt-4 flex justify-center">
						<Button variant="outline" onclick={() => void loadMore()} disabled={loadingMore}>
							{loadingMore ? m.datastores_loading_more() : m.datastores_load_more()}
						</Button>
					</div>
				{/if}
			</ListStates>
		</div>
	{/if}

	<Dialog.Root bind:open={rowDialogOpen}>
		<Dialog.Content aria-describedby="datastore-row-description">
			<Dialog.Header>
				<Dialog.Title>{m.datastores_add_row()}</Dialog.Title>
				<Dialog.Description id="datastore-row-description">
					{m.datastores_add_row_description()}
				</Dialog.Description>
			</Dialog.Header>
			<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void saveRow(); }}>
				{#each userColumns as column (column.name)}
					<div class="grid gap-2">
						<label for={column.type === 'boolean' ? undefined : `row-field-${column.name}`} class="text-sm font-medium">
							{column.name}
							<span class="ml-1 font-normal text-muted-foreground">· {displayType(column.type)}</span>
						</label>
						{#if column.type === 'boolean'}
							<div class="flex items-center gap-2">
								<SyncedCheckbox
									label={column.name}
									checked={boolDraft[column.name] ?? false}
									onToggle={(next) => (boolDraft = { ...boolDraft, [column.name]: next })}
								/>
								<span class="text-xs text-muted-foreground">{m.datastores_checked_means_true()}</span>
							</div>
						{:else if column.type === 'date' || column.type === 'datetime'}
							<Input
								id={`row-field-${column.name}`}
								type="datetime-local"
								value={draft[column.name] ?? ''}
								oninput={(event) => (draft = { ...draft, [column.name]: event.currentTarget.value })}
							/>
						{:else if column.type === 'number'}
							<Input
								id={`row-field-${column.name}`}
								type="number"
								value={draft[column.name] ?? ''}
								placeholder="0"
								oninput={(event) => (draft = { ...draft, [column.name]: event.currentTarget.value })}
							/>
						{:else}
							<Input
								id={`row-field-${column.name}`}
								value={draft[column.name] ?? ''}
								oninput={(event) => (draft = { ...draft, [column.name]: event.currentTarget.value })}
							/>
						{/if}
					</div>
				{/each}
				{#if rowError}<p role="alert" class="text-sm text-destructive">{rowError}</p>{/if}
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => (rowDialogOpen = false)} disabled={savingRow}>{m.datastores_cancel()}</Button>
					<Button type="submit" disabled={savingRow}>{savingRow ? m.datastores_adding() : m.datastores_add_row()}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root bind:open={columnDialogOpen}>
		<Dialog.Content aria-describedby="datastore-column-description">
			<Dialog.Header>
				<Dialog.Title>{m.datastores_add_column()}</Dialog.Title>
				<Dialog.Description id="datastore-column-description">
					{m.datastores_add_column_description()}
				</Dialog.Description>
			</Dialog.Header>
			<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void saveColumn(); }}>
				<div class="grid gap-2">
					<label for="column-name" class="text-sm font-medium">{m.datastores_name_label()}</label>
					<Input id="column-name" bind:value={columnName} placeholder={m.datastores_column_name_placeholder()} />
				</div>
				<div class="grid gap-2">
					<label for="column-type" class="text-sm font-medium">{m.datastores_type_label()}</label>
					<select
						id="column-type"
						bind:value={columnType}
						class="h-7 rounded-md border border-input bg-background px-2 text-xs"
					>
						{#each COLUMN_TYPES as option (option)}
							<option value={option}>{option}</option>
						{/each}
					</select>
				</div>
				{#if columnError}<p role="alert" class="text-sm text-destructive">{columnError}</p>{/if}
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => (columnDialogOpen = false)} disabled={savingColumn}>{m.datastores_cancel()}</Button>
					<Button type="submit" disabled={savingColumn}>{savingColumn ? m.datastores_adding() : m.datastores_add_column()}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root open={deletingColumn !== null} onOpenChange={(open) => { if (!open) deletingColumn = null; }}>
		<Dialog.Content aria-describedby="column-delete-description">
			<Dialog.Header>
				<Dialog.Title>{m.datastores_delete_column_title({ column: deletingColumn ?? '' })}</Dialog.Title>
				<Dialog.Description id="column-delete-description">
					{m.datastores_delete_column_description()}
				</Dialog.Description>
			</Dialog.Header>
			{#if headerError}<p role="alert" class="text-sm text-destructive">{headerError}</p>{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (deletingColumn = null)} disabled={removingColumn}>{m.datastores_cancel()}</Button>
				<Button type="button" variant="destructive" onclick={() => void removeColumn()} disabled={removingColumn}>
					{removingColumn ? m.datastores_deleting() : m.datastores_delete_column_submit()}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root bind:open={confirmingRows}>
		<Dialog.Content aria-describedby="rows-delete-description">
			<Dialog.Header>
				<Dialog.Title>{m.datastores_delete_rows_title({ count: selectedIDs.length })}</Dialog.Title>
				<Dialog.Description id="rows-delete-description">
					{m.datastores_delete_rows_description()}
				</Dialog.Description>
			</Dialog.Header>
			{#if headerError}<p role="alert" class="text-sm text-destructive">{headerError}</p>{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (confirmingRows = false)} disabled={removingRows}>{m.datastores_cancel()}</Button>
				<Button type="button" variant="destructive" onclick={() => void removeRows()} disabled={removingRows}>
					{removingRows ? m.datastores_deleting() : m.datastores_delete_rows_submit()}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root open={transfer !== null} onOpenChange={(open) => { if (!open) transfer = null; }}>
		<Dialog.Content aria-describedby="transfer-description">
			<Dialog.Header>
				<Dialog.Title>{transfer === 'import' ? m.datastores_import_rows_title() : m.datastores_export_rows_title()}</Dialog.Title>
				<Dialog.Description id="transfer-description">
					{#if transfer === 'import'}
						{m.datastores_import_description()}
					{:else}
						{m.datastores_export_description()}
					{/if}
				</Dialog.Description>
			</Dialog.Header>

			{#if transfer === 'export'}
				<label class="flex cursor-pointer items-start gap-2 text-xs leading-5">
					<input type="checkbox" bind:checked={includeSystem} class="mt-1" />
					<span>
						{m.datastores_include_system_columns()}
						<span class="block text-muted-foreground">
							{m.datastores_system_columns_note()}
						</span>
					</span>
				</label>
			{:else}
				<div class="grid gap-2">
					<label for="transfer-file" class="text-xs font-medium">{m.datastores_csv_file_label()}</label>
					<input
						id="transfer-file"
						type="file"
						accept=".csv,text/csv"
						class="text-xs"
						onchange={(event) => {
							transferFile = event.currentTarget.files?.[0] ?? null;
							transferError = null;
						}}
					/>
					<p class="text-xs leading-5 text-muted-foreground">
						{m.datastores_import_header_note()}
					</p>
				</div>
			{/if}

			{#if transferError}
				<p role="alert" class="text-xs leading-5 text-destructive">{transferError}</p>
			{/if}
			{#if savedFilename}
				<p role="status" class="text-xs leading-5 text-muted-foreground">{m.datastores_saved({ filename: savedFilename })}</p>
			{/if}
			{#if transferReport}
				{#if transferReport.failed.length === 0}
					<p role="status" class="text-xs leading-5 text-muted-foreground">{summarizeReport(transferReport)}</p>
				{:else}
					<section aria-label={m.datastores_blocking_diagnostics_aria()} class="grid gap-1.5">
						<div class="flex flex-wrap items-baseline gap-x-2">
							<h3 class="text-xs font-semibold">
								{m.datastores_blocking_heading()} <span class="font-normal text-muted-foreground">· {transferReport.failed.length}</span>
							</h3>
							<p class="w-full text-xs leading-5 text-muted-foreground">
								{m.datastores_import_refused()}
							</p>
						</div>
						<ul class="grid gap-1.5">
							{#each transferReport.failed as issue (`${issue.line}-${issue.column}`)}
								<li class="flex flex-wrap items-baseline gap-x-2 rounded-lg border border-border px-3 py-2 text-xs leading-5">
									<span class="inline-flex items-center rounded-full border border-destructive/30 bg-destructive/10 px-2 py-0.5 text-[0.6875rem] font-medium text-destructive">{severityLabel(issue.severity)}</span>
									<span class="font-mono text-[0.6875rem] text-muted-foreground">{m.datastores_issue_line({ line: issue.line, column: issue.column })}</span>
									<span class="w-full text-muted-foreground">{issue.reason}</span>
								</li>
							{/each}
						</ul>
					</section>
				{/if}
			{/if}

			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (transfer = null)}>{m.datastores_close()}</Button>
				{#if transfer === 'import'}
					<Button type="button" onclick={() => void runImport()} disabled={transferBusy || !transferFile}>
						{transferBusy ? m.datastores_uploading() : m.datastores_upload()}
					</Button>
				{:else}
					<Button type="button" onclick={() => void runExport()} disabled={transferBusy}>
						{transferBusy ? m.datastores_downloading() : m.datastores_download()}
					</Button>
				{/if}
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
</section>
