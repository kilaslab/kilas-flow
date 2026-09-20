<script lang="ts">
	import KeyRound from '@lucide/svelte/icons/key-round';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import { message } from '$lib/api/http';
	import {
		createCredential,
		createListCredentialTypes,
		createListCredentials,
		deleteCredential,
		testCredentialPayload,
		updateCredential
	} from '$lib/api/generated/credentials/credentials';
	import type { CredentialResource, CredentialTypeResource } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';

	const credentials = createListCredentials<CredentialResource[]>(undefined, () => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected credential-list response');
				return response.data ?? [];
			}
		}
	}));
	const types = createListCredentialTypes<CredentialTypeResource[]>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected credential-type response');
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
	// Read once here rather than through the query object in the markup: the
	// rows are used inside a snippet, where the `!isPending && !isError`
	// narrowing that made `.data` non-optional no longer reaches.
	const rows = $derived(credentials.data ?? []);

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
			formError = 'Give this credential a name.';
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
			if (response.status !== 200 && response.status !== 201) throw new Error('Unexpected credential-save response');
			await credentials.refetch();
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
			await credentials.refetch();
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
			if (response.status !== 200) throw new Error('Unexpected credential-test response');
			testResult = { ok: response.data.ok, detail: response.data.detail ?? '' };
		} catch (error) {
			testResult = { ok: false, detail: message(error) };
		} finally {
			testing = false;
		}
	}
</script>

<svelte:head>
	<title>Credentials · KilasFlow</title>
</svelte:head>

<section class="mx-auto w-full max-w-4xl">
	<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
		<div class="max-w-xl">
			<h1 class="text-base font-semibold tracking-tight">Credentials</h1>
			<p class="text-xs text-muted-foreground">
				Secrets are encrypted before storage and never returned once saved. Scope a credential to the hosts it may be sent to.
			</p>
		</div>
		<Button onclick={openCreate} disabled={types.isPending} class="w-full sm:w-auto">
			<KeyRound aria-hidden="true" />
			New credential
		</Button>
	</div>

	{#if deleteError && !deleteOpen}
		<p role="alert" class="mt-4 rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">Delete failed: {deleteError}</p>
	{/if}
	<div class="mt-4">
		<ListStates
			label="Credentials"
			loading={credentials.isPending}
			failed={credentials.isError}
			error={credentials.error}
			count={rows.length}
			rows={2}
			onRetry={() => void credentials.refetch()}
			emptyIcon={KeyRound}
			emptyTitle="No credentials yet"
			emptyBody="Add one to authenticate HTTP Request nodes without putting a secret in a workflow."
		>
			<ul aria-label="Credentials" class="divide-y divide-border overflow-hidden rounded-lg border border-border">
				{#each rows as credential (credential.id)}
					{@const typeName = (types.data ?? []).find((candidate) => candidate.id === credential.type)?.displayName ?? credential.type}
					<li class="flex min-h-11 items-center gap-3 px-3 py-1.5">
						<div class="min-w-0 flex-1">
							<p class="truncate text-sm font-medium">{credential.name}</p>
							<p class="truncate text-xs text-muted-foreground" title={`${typeName} · ${(credential.allowedDomains ?? []).length > 0 ? (credential.allowedDomains ?? []).join(', ') : 'Any host'}`}>
								{typeName}
								· {(credential.allowedDomains ?? []).length > 0 ? (credential.allowedDomains ?? []).join(', ') : 'Any host'}
							</p>
						</div>
						<Button variant="outline" size="sm" onclick={() => openEdit(credential)}>Edit</Button>
						<Button variant="ghost" size="sm" aria-label={`Delete ${credential.name}`} onclick={() => askDelete(credential)}>
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
				<Dialog.Title>{editing ? 'Edit credential' : 'New credential'}</Dialog.Title>
				<Dialog.Description id="credential-form-description">
					{editing ? 'Leave a secret field untouched to keep its stored value.' : 'Values are encrypted before they are stored.'}
				</Dialog.Description>
			</Dialog.Header>
			<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void save(); }}>
				<div class="grid gap-2">
					<label for="credential-name" class="text-sm font-medium">Name</label>
					<Input id="credential-name" bind:value={name} placeholder="e.g. Partner API" />
				</div>
				<div class="grid gap-2">
					<label for="credential-type" class="text-sm font-medium">Type</label>
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
							placeholder={isSecretStored(field) ? 'Stored — type to replace' : field.default ? `Default: ${field.default}` : undefined}
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
					<label for="credential-domains" class="text-sm font-medium">Allowed hosts</label>
					<Input id="credential-domains" bind:value={domains} placeholder="api.partner.test, *.eu.partner.test" />
					<p class="text-xs leading-5 text-muted-foreground">Comma separated. Leave empty to allow any host.</p>
				</div>
				{#if testResult}
					<p role="status" class={`text-sm ${testResult.ok ? 'text-success' : 'text-destructive'}`}>{testResult.ok ? `Connected${testResult.detail ? ` — ${testResult.detail}` : ''}` : `Test failed — ${testResult.detail}`}</p>
				{/if}
				{#if formError}<p role="alert" class="text-sm text-destructive">{formError}</p>{/if}
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => void testCurrent()} disabled={saving || testing}>{testing ? 'Testing…' : 'Test'}</Button>
					<Button type="button" variant="outline" onclick={() => (editorOpen = false)} disabled={saving}>Cancel</Button>
					<Button type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save credential'}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root bind:open={deleteOpen}>
		<Dialog.Content aria-describedby="credential-delete-description">
			<Dialog.Header>
				<Dialog.Title>Delete {pendingDelete?.name ?? 'credential'}?</Dialog.Title>
				<Dialog.Description id="credential-delete-description">
					This destroys the encrypted secret. Workflows using it will fail until they are given another credential.
				</Dialog.Description>
			</Dialog.Header>
			{#if deleteError}<p role="alert" class="text-sm text-destructive">{deleteError}</p>{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (deleteOpen = false)} disabled={deleting}>Cancel</Button>
				<Button type="button" variant="destructive" onclick={() => void confirmDelete()} disabled={deleting}>{deleting ? 'Deleting…' : 'Delete'}</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
</section>
