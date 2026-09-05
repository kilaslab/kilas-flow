<script lang="ts">
	import KeyRound from '@lucide/svelte/icons/key-round';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import { message } from '$lib/api/http';
	import {
		createCredential,
		createListCredentialTypes,
		createListCredentials,
		deleteCredential,
		updateCredential
	} from '$lib/api/generated/credentials/credentials';
	import type { CredentialResource, CredentialTypeResource } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';

	const credentials = createListCredentials<CredentialResource[]>(() => ({
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
	let domains = $state('');
	let saving = $state(false);
	let formError = $state<string | null>(null);

	const definition = $derived((types.data ?? []).find((candidate) => candidate.id === typeID) ?? null);
	// Read once here rather than through the query object in the markup: the
	// rows are used inside a snippet, where the `!isPending && !isError`
	// narrowing that made `.data` non-optional no longer reaches.
	const rows = $derived(credentials.data ?? []);

	function openCreate() {
		editing = null;
		name = '';
		typeID = types.data?.[0]?.id ?? '';
		fields = {};
		domains = '';
		formError = null;
		editorOpen = true;
	}

	function openEdit(credential: CredentialResource) {
		editing = credential;
		name = credential.name;
		typeID = credential.type;
		// Secret fields arrive as a placeholder. Sending it back unchanged tells
		// the server to keep the stored secret it never disclosed.
		fields = { ...(credential.fields ?? {}) };
		domains = (credential.allowedDomains ?? []).join(', ');
		formError = null;
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
			fields,
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

	async function remove(credential: CredentialResource) {
		try {
			await deleteCredential(credential.id);
			await credentials.refetch();
		} catch (error) {
			formError = message(error);
		}
	}
</script>

<svelte:head>
	<title>Credentials · KilasFlow</title>
</svelte:head>

<section class="mx-auto w-full max-w-4xl">
	<div class="flex items-center justify-between gap-4">
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
					<li class="flex h-11 items-center gap-3 px-3">
						<div class="min-w-0 flex-1">
							<p class="truncate text-sm font-medium">{credential.name}</p>
							<p class="mt-1 text-xs text-muted-foreground">
								{credential.type}
								· {(credential.allowedDomains ?? []).length > 0 ? (credential.allowedDomains ?? []).join(', ') : 'Any host'}
							</p>
						</div>
						<Button variant="outline" size="sm" onclick={() => openEdit(credential)}>Edit</Button>
						<Button variant="ghost" size="sm" aria-label={`Delete ${credential.name}`} onclick={() => void remove(credential)}>
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
					<select id="credential-type" bind:value={typeID} disabled={Boolean(editing)} class="h-7 rounded-md border border-input bg-background px-2 text-xs disabled:opacity-60">
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
							autocomplete="off"
							oninput={(event) => (fields = { ...fields, [field.key]: event.currentTarget.value })}
						/>
						{#if field.description}<p class="text-xs leading-5 text-muted-foreground">{field.description}</p>{/if}
					</div>
				{/each}
				<div class="grid gap-2">
					<label for="credential-domains" class="text-sm font-medium">Allowed hosts</label>
					<Input id="credential-domains" bind:value={domains} placeholder="api.partner.test, *.eu.partner.test" />
					<p class="text-xs leading-5 text-muted-foreground">Comma separated. Leave empty to allow any host.</p>
				</div>
				{#if formError}<p role="alert" class="text-sm text-destructive">{formError}</p>{/if}
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => (editorOpen = false)} disabled={saving}>Cancel</Button>
					<Button type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save credential'}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>
</section>
