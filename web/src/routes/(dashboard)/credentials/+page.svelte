<script lang="ts">
	import { onMount } from 'svelte';
	import KeyRound from '@lucide/svelte/icons/key-round';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import { message } from '$lib/api/http';
	import {
		createCredential,
		createListCredentialTypes,
		deleteCredential,
		listCredentials,
		testCredentialPayload,
		updateCredential
	} from '$lib/api/generated/credentials/credentials';
	import type { CredentialResource, CredentialTypeResource } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { DRAIN_PAGE_LIMIT, drainPages, headerCursor, readPage, type CursorPage } from '$lib/dashboard/cursor-page';
	import { RequestGuard } from '$lib/dashboard/request-guard';
	import * as m from '$lib/paraglide/messages.js';

	// The listing is paged by the server, so one request is only its first
	// page: the rows are read to the end before they are shown, or a credential
	// past it would be invisible and unreachable from the editor's picker.
	let rows = $state<CredentialResource[]>([]);
	let loading = $state(true);
	let listFailure = $state<unknown>(null);
	const listGuard = new RequestGuard();

	/** One page of the credential listing: rows from the body, cursor from the header. */
	async function fetchCredentialPage(cursor: string): Promise<CursorPage<CredentialResource>> {
		const response = await listCredentials({ limit: DRAIN_PAGE_LIMIT, cursor: cursor || undefined });
		if (response.status !== 200) throw new Error(m.credentials_error_list());
		return readPage({ items: response.data, nextCursor: headerCursor(response.headers) });
	}

	async function loadCredentials() {
		const token = listGuard.start();
		listFailure = null;
		loading = rows.length === 0;
		try {
			const items = await drainPages(fetchCredentialPage);
			if (!listGuard.holds(token)) return;
			rows = items;
		} catch (cause) {
			if (!listGuard.holds(token)) return;
			listFailure = cause;
		} finally {
			if (listGuard.holds(token)) loading = false;
		}
	}

	onMount(() => void loadCredentials());

	const types = createListCredentialTypes<CredentialTypeResource[]>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error(m.credentials_error_types());
				return response.data ?? [];
			}
		}
	}));

	let editorOpen = $state(false);
	let editing = $state<CredentialResource | null>(null);
	let name = $state('');
	let typeID = $state('');
	let fields = $state<Record<string, string>>({});
	let touchedSecrets = $state<Set<string>>(new Set());
	let domains = $state('');
	let saving = $state(false);
	let formError = $state<string | null>(null);
	let deleteOpen = $state(false);
	let pendingDelete = $state<CredentialResource | null>(null);
	let deleting = $state(false);
	let deleteError = $state<string | null>(null);
	let testResult = $state<{ ok: boolean; detail: string } | null>(null);
	let testing = $state(false);

	const definition = $derived((types.data ?? []).find((candidate) => candidate.id === typeID) ?? null);

	const REDACTED = '••••••••';

	function defaultsFor(id: string): Record<string, string> {
		const next: Record<string, string> = {};
		for (const field of ((types.data ?? []).find((candidate) => candidate.id === id)?.fields ?? [])) {
			if (field.default !== undefined) next[field.key] = field.default;
		}
		return next;
	}

	function nonSecretFields(credential: CredentialResource): Record<string, string> {
		const next: Record<string, string> = {};
		for (const [key, value] of Object.entries(credential.fields ?? {})) {
			if (value === REDACTED) continue;
			next[key] = value;
		}
		return next;
	}

	function markTouched(key: string) {
		touchedSecrets = new Set(touchedSecrets).add(key);
	}

	function onTypeChange(next: string) {
		typeID = next;
		if (!editing) fields = defaultsFor(next);
		testResult = null;
	}

	function saveFields(): Record<string, string> {
		const next: Record<string, string> = { ...fields };
		if (editing) {
			for (const field of (definition?.fields ?? [])) {
				if (!field.secret) continue;
				if (touchedSecrets.has(field.key)) continue;
				next[field.key] = REDACTED;
			}
		}
		return next;
	}
	function isSecretStored(field: { key: string; secret: boolean }): boolean {
		return Boolean(editing) && field.secret && !touchedSecrets.has(field.key);
	}

	function openCreate() {
		editing = null;
		name = '';
		typeID = types.data?.[0]?.id ?? '';
		fields = defaultsFor(typeID);
		touchedSecrets = new Set();
		domains = '';
		formError = null;
		deleteError = null;
		editorOpen = true;
	}

	function openEdit(credential: CredentialResource) {
		editing = credential;
		name = credential.name;
		typeID = credential.type;
		// Secret fields render empty with a "Stored" placeholder: the server's
		// redaction marker is never placed in the input, so typing can never
		// save mask bullets as part of the secret. The marker is only sent
		// for a secret the user left untouched.
		fields = nonSecretFields(credential);
		touchedSecrets = new Set();
		domains = (credential.allowedDomains ?? []).join(', ');
		formError = null;
		deleteError = null;
		editorOpen = true;
	}

	async function save() {
		if (!name.trim()) {
			formError = m.credentials_error_name_required();
			return;
		}
		saving = true;
		formError = null;
		const body = {
			name: name.trim(),
			type: typeID,
			fields: saveFields(),
			allowedDomains: domains.split(',').map((entry) => entry.trim()).filter(Boolean)
		};
		try {
			const response = editing ? await updateCredential(editing.id, body) : await createCredential(body);
			if (response.status !== 200 && response.status !== 201) throw new Error(m.credentials_error_save());
			await loadCredentials();
			editorOpen = false;
		} catch (error) {
			formError = message(error);
		} finally {
			saving = false;
		}
	}

	function askDelete(credential: CredentialResource) {
		pendingDelete = credential;
		deleteError = null;
		deleteOpen = true;
	}

	async function confirmDelete() {
		if (!pendingDelete || deleting) return;
		deleting = true;
		deleteError = null;
		try {
			await deleteCredential(pendingDelete.id);
			await loadCredentials();
			deleteOpen = false;
			pendingDelete = null;
		} catch (error) {
			deleteError = message(error);
		} finally {
			deleting = false;
		}
	}
	async function testCurrent() {
		testResult = null;
		if (!typeID) return;
		testing = true;
		try {
			const payload = { fields: saveFields(), ...(editing ? { credentialId: editing.id } : {}) };
			const response = await testCredentialPayload(typeID, payload);
			if (response.status !== 200) throw new Error(m.credentials_error_test());
			testResult = { ok: response.data.ok, detail: response.data.detail ?? '' };
		} catch (error) {
			testResult = { ok: false, detail: message(error) };
		} finally {
			testing = false;
		}
	}
</script>

<svelte:head>
	<title>{m.credentials_page_title()}</title>
</svelte:head>

<section class="mx-auto w-full max-w-4xl">
	<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
		<div class="max-w-xl">
			<h1 class="text-base font-semibold tracking-tight">{m.nav_credentials()}</h1>
			<p class="text-xs text-muted-foreground">
				{m.credentials_description()}
			</p>
		</div>
		<Button onclick={openCreate} disabled={types.isPending} class="w-full sm:w-auto">
			<KeyRound aria-hidden="true" />
			{m.credentials_new()}
		</Button>
	</div>

	{#if deleteError && !deleteOpen}
		<p role="alert" class="mt-4 rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">{m.credentials_delete_failed({ message: deleteError })}</p>
	{/if}
	<div class="mt-4">
		<ListStates
			label={m.nav_credentials()}
			loading={loading}
			failed={listFailure !== null}
			error={listFailure}
			count={rows.length}
			rows={2}
			onRetry={() => void loadCredentials()}
			emptyIcon={KeyRound}
			emptyTitle={m.credentials_empty_title()}
			emptyBody={m.credentials_empty_body()}
		>
			<ul aria-label={m.nav_credentials()} class="divide-y divide-border overflow-hidden rounded-lg border border-border">
				{#each rows as credential (credential.id)}
					{@const typeName = (types.data ?? []).find((candidate) => candidate.id === credential.type)?.displayName ?? credential.type}
					<li class="flex min-h-11 items-center gap-3 px-3 py-1.5">
						<div class="min-w-0 flex-1">
							<p class="truncate text-sm font-medium">{credential.name}</p>
							<p class="truncate text-xs text-muted-foreground" title={`${typeName} · ${(credential.allowedDomains ?? []).length > 0 ? (credential.allowedDomains ?? []).join(', ') : m.credentials_any_host()}`}>
								{typeName}
								· {(credential.allowedDomains ?? []).length > 0 ? (credential.allowedDomains ?? []).join(', ') : m.credentials_any_host()}
							</p>
						</div>
						<Button variant="outline" size="sm" onclick={() => openEdit(credential)}>{m.credentials_edit()}</Button>
						<Button variant="ghost" size="sm" aria-label={m.credentials_delete_aria({ name: credential.name })} onclick={() => askDelete(credential)}>
							<Trash2 aria-hidden="true" class="size-4 text-destructive" />
						</Button>
					</li>
				{/each}
			</ul>
		</ListStates>
	</div>

	<Dialog.Root bind:open={editorOpen}>
		<Dialog.Content aria-describedby="credential-form-description">
			<Dialog.Header>
				<Dialog.Title>{editing ? m.credentials_edit_title() : m.credentials_new()}</Dialog.Title>
				<Dialog.Description id="credential-form-description">
					{editing ? m.credentials_form_description_edit() : m.credentials_form_description_new()}
				</Dialog.Description>
			</Dialog.Header>
			<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void save(); }}>
				<div class="grid gap-2">
					<label for="credential-name" class="text-sm font-medium">{m.credentials_name()}</label>
					<Input id="credential-name" bind:value={name} placeholder={m.credentials_name_placeholder()} />
				</div>
				<div class="grid gap-2">
					<label for="credential-type" class="text-sm font-medium">{m.credentials_type()}</label>
					<select id="credential-type" value={typeID} disabled={Boolean(editing)} onchange={(event) => onTypeChange(event.currentTarget.value)} class="h-7 rounded-md border border-input bg-background px-2 text-xs disabled:opacity-60">
						{#each types.data ?? [] as candidate (candidate.id)}
							<option value={candidate.id}>{candidate.displayName}</option>
						{/each}
					</select>
					{#if definition?.description}<p class="text-xs leading-5 text-muted-foreground">{definition.description}</p>{/if}
				</div>
				{#each definition?.fields ?? [] as field (field.key)}
					<div class="grid gap-2">
						<label for={`credential-field-${field.key}`} class="text-sm font-medium">
							{field.label}{#if field.required}<span aria-hidden="true" class="text-destructive"> *</span>{/if}
						</label>
						<Input
							id={`credential-field-${field.key}`}
							type={field.secret ? 'password' : 'text'}
							value={fields[field.key] ?? ''}
							placeholder={isSecretStored(field) ? m.credentials_stored_placeholder() : field.default ? m.credentials_default_placeholder({ value: field.default }) : undefined}
							autocomplete="off"
							oninput={(event) => {
								fields = { ...fields, [field.key]: event.currentTarget.value };
								if (field.secret) markTouched(field.key);
								testResult = null;
							}}
						/>
						{#if field.description}<p class="text-xs leading-5 text-muted-foreground">{field.description}</p>{/if}
					</div>
				{/each}
				<div class="grid gap-2">
					<label for="credential-domains" class="text-sm font-medium">{m.credentials_allowed_hosts()}</label>
					<Input id="credential-domains" bind:value={domains} placeholder={m.credentials_allowed_hosts_placeholder()} />
					<p class="text-xs leading-5 text-muted-foreground">{m.credentials_allowed_hosts_hint()}</p>
				</div>
				{#if testResult}
					<p role="status" class={`text-sm ${testResult.ok ? 'text-success' : 'text-destructive'}`}>{testResult.ok ? (testResult.detail ? m.credentials_test_connected_detail({ detail: testResult.detail }) : m.credentials_test_connected()) : m.credentials_test_failed({ detail: testResult.detail })}</p>
				{/if}
				{#if formError}<p role="alert" class="text-sm text-destructive">{formError}</p>{/if}
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => void testCurrent()} disabled={saving || testing}>{testing ? m.credentials_testing() : m.credentials_test()}</Button>
					<Button type="button" variant="outline" onclick={() => (editorOpen = false)} disabled={saving}>{m.credentials_cancel()}</Button>
					<Button type="submit" disabled={saving}>{saving ? m.credentials_saving() : m.credentials_save()}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root bind:open={deleteOpen}>
		<Dialog.Content aria-describedby="credential-delete-description">
			<Dialog.Header>
				<Dialog.Title>{m.credentials_delete_title({ name: pendingDelete?.name ?? m.credentials_delete_unnamed() })}</Dialog.Title>
				<Dialog.Description id="credential-delete-description">
					{m.credentials_delete_body()}
				</Dialog.Description>
			</Dialog.Header>
			{#if deleteError}<p role="alert" class="text-sm text-destructive">{deleteError}</p>{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (deleteOpen = false)} disabled={deleting}>{m.credentials_cancel()}</Button>
				<Button type="button" variant="destructive" onclick={() => void confirmDelete()} disabled={deleting}>{deleting ? m.credentials_deleting() : m.credentials_delete()}</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
</section>
