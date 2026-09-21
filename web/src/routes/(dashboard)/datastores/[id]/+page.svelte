<script lang="ts">
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

	$effect(() => {
		if (id) {
			void loadDetail(id);
			void load(id);
		}
	});

	async function loadDetail(datastoreID: string) {
		detailLoading = true;
		detailFailure = null;
		try {
			const response = await getDatastore(datastoreID);
			if (response.status !== 200) throw new Error('Unexpected datastore response');
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
			if (response.status !== 200) throw new Error('Unexpected row-list response');
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
			if (response.status !== 200) throw new Error('Unexpected row-update response');
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
			if (response.status !== 200) throw new Error('Unexpected row-list response');
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
			if (response.status !== 201) throw new Error('Unexpected row-insert response');
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
			columnError = 'Give this column a name.';
			return;
		}
		savingColumn = true;
		columnError = null;
		try {
			const response = await addDatastoreColumn(id, {
				name: columnName.trim(),
				type: wireType(columnType)
			});
			if (response.status !== 200) throw new Error('Unexpected column-add response');
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
			if (response.status !== 200) throw new Error('Unexpected column-rename response');
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
			if (response.status !== 204) throw new Error('Unexpected column-delete response');
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
			if (response.status !== 200) throw new Error('Unexpected row-delete response');
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
			transferError = 'Choose a CSV file first.';
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
	<title>{detail?.name ?? 'Datastore'} · KilasFlow</title>
</svelte:head>

<section class="mx-auto w-full max-w-5xl">
	<a
		href="/datastores"
		class="inline-flex items-center gap-1.5 rounded text-xs text-muted-foreground underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2"
	>
		<ArrowLeft aria-hidden="true" class="size-3.5" />Datastores
	</a>

	{#if detailLoading}
		<div aria-live="polite" class="mt-3 overflow-hidden rounded-lg border border-border">
			<p class="sr-only">Loading datastore…</p>
			<div class="h-11 animate-pulse bg-muted/50" aria-hidden="true"></div>
		</div>
	{:else if detailFailure || !detail}
		<div class="mt-3 max-w-lg rounded-lg border border-destructive/25 bg-destructive/5 p-3">
			<p class="text-xs font-medium text-destructive">This datastore could not be loaded.</p>
			<p class="mt-0.5 text-xs leading-5 text-muted-foreground">{message(detailFailure)}</p>
			<Button class="mt-2.5" size="sm" variant="outline" onclick={() => void loadDetail(id)}>
				Try again
			</Button>
		</div>
	{:else}
		<div class="mt-2 flex flex-wrap items-center justify-between gap-3">
			<div class="max-w-xl">
				<h1 class="text-base font-semibold tracking-tight">{detail.name}</h1>
				<p class="text-xs text-muted-foreground">
					{userColumns.length} user column{userColumns.length === 1 ? '' : 's'} · 20 rows per page
				</p>
			</div>
			<div class="flex flex-wrap gap-2">
				<Button variant="outline" size="sm" onclick={() => openTransfer('import')}>
					<Upload aria-hidden="true" />Import
				</Button>
				<Button variant="outline" size="sm" onclick={() => openTransfer('export')}>
					<Download aria-hidden="true" />Export
				</Button>
				<Button size="sm" onclick={openRowDialog}>
					<Plus aria-hidden="true" />Add row
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
					<Plus aria-hidden="true" />Add column
				</Button>
			</div>
		</div>

		{#if selectedIDs.length > 0}
			<div class="mt-3 flex flex-wrap items-center gap-3 rounded-lg border border-border bg-muted/40 px-3 py-2">
				<p class="text-xs text-muted-foreground" role="status">
					{selectedIDs.length} row{selectedIDs.length === 1 ? '' : 's'} selected
				</p>
				<Button type="button" size="sm" variant="outline" onclick={() => (confirmingRows = true)}>
					<Trash2 aria-hidden="true" />Delete selected
				</Button>
				<Button type="button" size="sm" variant="ghost" onclick={() => { checkState = {}; allChecked = false; }}>
					Clear selection
				</Button>
			</div>
		{/if}

		{#if headerError}
			<p role="alert" class="mt-3 text-xs text-destructive">{headerError}</p>
		{/if}

		<div class="mt-3">
			<div class="mb-3 flex flex-wrap items-center gap-2">
				<label for="datastore-search" class="sr-only">Search rows</label>
				<Input
					id="datastore-search"
					value={search}
					placeholder="Search text columns…"
					class="h-7 max-w-64 text-xs"
					oninput={(event) => {
						search = event.currentTarget.value;
						void load(id);
					}}
				/>
				<p class="text-xs text-muted-foreground" role="status">
					{rows.items.length} row{rows.items.length === 1 ? '' : 's'} on this page
				</p>
				{#if cellError}<p role="alert" class="text-xs text-destructive">{cellError}</p>{/if}
			</div>
			<ListStates
				label="Rows"
				{loading}
				failed={failure !== null}
				error={failure}
				count={rows.items.length}
				onRetry={() => void load(id)}
				onRetryMore={() => void loadMore()}
				emptyIcon={Plus}
				emptyTitle="No rows yet"
				emptyBody="Add the first row to start persisting data in this table."
			>
				<div class="overflow-hidden rounded-xl border border-border bg-card">
					<Table.Root class="min-w-[44rem]">
						<Table.Caption class="sr-only">Rows in {detail.name}, id order</Table.Caption>
						<Table.Header class="[&_th]:h-7 [&_th]:px-3 [&_th]:text-xs [&_th]:uppercase [&_th]:tracking-wide [&_th]:text-muted-foreground">
							<Table.Row>
								<Table.Head scope="col" class="w-8">
									<SyncedCheckbox
										label="Select all rows on this page"
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
												aria-label={`Sort by ${column.name} ${sortColumn === column.name && sortDirection === 'asc' ? 'descending' : 'ascending'}`}
												title={`Sort by ${column.name}`}
												onclick={() => toggleSort(column.name)}
											>
												{column.name}{sortColumn === column.name ? (sortDirection === 'asc' ? ' ▲' : ' ▼') : ''}
											</button>
											{#if renaming === column.name}
												<Input
													class="h-6 w-32 text-xs normal-case"
													value={renameValue}
													aria-label={`Rename column ${column.name}`}
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
													<span class="font-normal text-muted-foreground/70">· {displayType(column.type)}</span>
													<Button
														type="button"
														variant="ghost"
														size="icon"
														class="size-5"
														aria-label={`Rename column ${column.name}`}
														onclick={() => startRename(column)}
													>
														<Pencil aria-hidden="true" class="size-3" />
													</Button>
													<Button
														type="button"
														variant="ghost"
														size="icon"
														class="size-5 text-muted-foreground hover:text-destructive"
														aria-label={`Delete column ${column.name}`}
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
											label={`Select row ${rowID}`}
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
													aria-label={`Edit ${column.name} in row ${rowID}`}
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
												<button type="button" title={column.system ? 'System column — read-only' : 'Empty — click to edit'} aria-label={cellButtonLabel({ column: column.name, system: column.system, rowID, text: cellText(row, column), empty: isNullCell(row, column) })} class="text-muted-foreground/60 {column.system ? '' : 'hover:underline'}" onclick={() => startCellEdit(rowID, column, row[column.name])} disabled={column.system}>
													<span>Null</span>
												</button>
											{:else}
												<button type="button" title={column.system ? cellText(row, column) : `${cellText(row, column)} — click to edit`} aria-label={cellButtonLabel({ column: column.name, system: column.system, rowID, text: cellText(row, column), empty: isNullCell(row, column) })} class="max-w-full truncate {column.system ? 'cursor-default' : 'hover:underline'}" onclick={() => startCellEdit(rowID, column, row[column.name])} disabled={column.system}>
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
							{loadingMore ? 'Loading…' : 'Load more'}
						</Button>
					</div>
				{/if}
			</ListStates>
		</div>
	{/if}

	<Dialog.Root bind:open={rowDialogOpen}>
		<Dialog.Content aria-describedby="datastore-row-description">
			<Dialog.Header>
				<Dialog.Title>Add row</Dialog.Title>
				<Dialog.Description id="datastore-row-description">
					Set the value for each column. Empty stays Null — the row id and both
					timestamps are set by the database.
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
								<span class="text-xs text-muted-foreground">Checked means true.</span>
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
					<Button type="button" variant="outline" onclick={() => (rowDialogOpen = false)} disabled={savingRow}>Cancel</Button>
					<Button type="submit" disabled={savingRow}>{savingRow ? 'Adding…' : 'Add row'}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root bind:open={columnDialogOpen}>
		<Dialog.Content aria-describedby="datastore-column-description">
			<Dialog.Header>
				<Dialog.Title>Add column</Dialog.Title>
				<Dialog.Description id="datastore-column-description">
					A name and a type. Nothing offers nullability, uniqueness, or a default.
				</Dialog.Description>
			</Dialog.Header>
			<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void saveColumn(); }}>
				<div class="grid gap-2">
					<label for="column-name" class="text-sm font-medium">Name</label>
					<Input id="column-name" bind:value={columnName} placeholder="e.g. score" />
				</div>
				<div class="grid gap-2">
					<label for="column-type" class="text-sm font-medium">Type</label>
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
					<Button type="button" variant="outline" onclick={() => (columnDialogOpen = false)} disabled={savingColumn}>Cancel</Button>
					<Button type="submit" disabled={savingColumn}>{savingColumn ? 'Adding…' : 'Add column'}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root open={deletingColumn !== null} onOpenChange={(open) => { if (!open) deletingColumn = null; }}>
		<Dialog.Content aria-describedby="column-delete-description">
			<Dialog.Header>
				<Dialog.Title>Delete column {deletingColumn}?</Dialog.Title>
				<Dialog.Description id="column-delete-description">
					Every value in this column is removed with it. Cancelling sends no
					request and leaves the datastore untouched.
				</Dialog.Description>
			</Dialog.Header>
			{#if headerError}<p role="alert" class="text-sm text-destructive">{headerError}</p>{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (deletingColumn = null)} disabled={removingColumn}>Cancel</Button>
				<Button type="button" variant="destructive" onclick={() => void removeColumn()} disabled={removingColumn}>
					{removingColumn ? 'Deleting…' : 'Delete column'}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root bind:open={confirmingRows}>
		<Dialog.Content aria-describedby="rows-delete-description">
			<Dialog.Header>
				<Dialog.Title>Delete {selectedIDs.length} row{selectedIDs.length === 1 ? '' : 's'}?</Dialog.Title>
				<Dialog.Description id="rows-delete-description">
					Only the selected rows are removed. Cancelling sends no request and
					leaves every row untouched.
				</Dialog.Description>
			</Dialog.Header>
			{#if headerError}<p role="alert" class="text-sm text-destructive">{headerError}</p>{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (confirmingRows = false)} disabled={removingRows}>Cancel</Button>
				<Button type="button" variant="destructive" onclick={() => void removeRows()} disabled={removingRows}>
					{removingRows ? 'Deleting…' : 'Delete rows'}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root open={transfer !== null} onOpenChange={(open) => { if (!open) transfer = null; }}>
		<Dialog.Content aria-describedby="transfer-description">
			<Dialog.Header>
				<Dialog.Title>{transfer === 'import' ? 'Import rows' : 'Export rows'}</Dialog.Title>
				<Dialog.Description id="transfer-description">
					{#if transfer === 'import'}
						Upload a CSV file whose header names this datastore's columns. Every
						record is validated before any row is written: a file with a failed
						row imports nothing.
					{:else}
						Download this datastore's rows as a CSV file, in row order.
					{/if}
				</Dialog.Description>
			</Dialog.Header>

			{#if transfer === 'export'}
				<label class="flex cursor-pointer items-start gap-2 text-xs leading-5">
					<input type="checkbox" bind:checked={includeSystem} class="mt-1" />
					<span>
						Include system columns
						<span class="block text-muted-foreground">
							Adds <span class="font-mono">id</span>, <span class="font-mono">createdAt</span> and
							<span class="font-mono">updatedAt</span> around the user columns. Leave it off for a
							sheet to edit and re-import; turn it on for a backup.
						</span>
					</span>
				</label>
			{:else}
				<div class="grid gap-2">
					<label for="transfer-file" class="text-xs font-medium">CSV file</label>
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
						The header must name user columns only — a file carrying
						<span class="font-mono">id</span>, <span class="font-mono">createdAt</span>,
						<span class="font-mono">updatedAt</span> or <span class="font-mono">dryRunState</span> is refused.
					</p>
				</div>
			{/if}

			{#if transferError}
				<p role="alert" class="text-xs leading-5 text-destructive">{transferError}</p>
			{/if}
			{#if savedFilename}
				<p role="status" class="text-xs leading-5 text-muted-foreground">Saved {savedFilename}.</p>
			{/if}
			{#if transferReport}
				{#if transferReport.failed.length === 0}
					<p role="status" class="text-xs leading-5 text-muted-foreground">{summarizeReport(transferReport)}</p>
				{:else}
					<section aria-label="Blocking import diagnostics" class="grid gap-1.5">
						<div class="flex flex-wrap items-baseline gap-x-2">
							<h3 class="text-xs font-semibold">
								Blocking <span class="font-normal text-muted-foreground">· {transferReport.failed.length}</span>
							</h3>
							<p class="w-full text-xs leading-5 text-muted-foreground">
								No rows were imported. Fix the file and try again.
							</p>
						</div>
						<ul class="grid gap-1.5">
							{#each transferReport.failed as issue (`${issue.line}-${issue.column}`)}
								<li class="flex flex-wrap items-baseline gap-x-2 rounded-lg border border-border px-3 py-2 text-xs leading-5">
									<span class="inline-flex items-center rounded-full border border-destructive/30 bg-destructive/10 px-2 py-0.5 text-[0.6875rem] font-medium text-destructive">{severityLabel(issue.severity)}</span>
									<span class="font-mono text-[0.6875rem] text-muted-foreground">Line {issue.line} · {issue.column}</span>
									<span class="w-full text-muted-foreground">{issue.reason}</span>
								</li>
							{/each}
						</ul>
					</section>
				{/if}
			{/if}

			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (transfer = null)}>Close</Button>
				{#if transfer === 'import'}
					<Button type="button" onclick={() => void runImport()} disabled={transferBusy || !transferFile}>
						{transferBusy ? 'Uploading…' : 'Upload'}
					</Button>
				{:else}
					<Button type="button" onclick={() => void runExport()} disabled={transferBusy}>
						{transferBusy ? 'Downloading…' : 'Download'}
					</Button>
				{/if}
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
</section>
