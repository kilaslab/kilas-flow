<script lang="ts">
	import Copy from '@lucide/svelte/icons/copy';
	import Check from '@lucide/svelte/icons/check';
	import KeyRound from '@lucide/svelte/icons/key-round';
	import LogOut from '@lucide/svelte/icons/log-out';

	import { message } from '$lib/api/http';
	import {
		createApiKey,
		createGetMe,
		createListApiKeys,
		logout,
		revokeApiKey
	} from '$lib/api/generated/auth/auth';
	import { createGetHealth } from '$lib/api/generated/system/system';
	import type { APIKeyResource, CreatedAPIKeyResource, HealthOutputBody, PrincipalResource } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { formatTimestamp } from '$lib/workflow-editor/execution';

	/**
	 * Settings: account, API keys and instance info (FEAT-x5km1z).
	 *
	 * The key endpoints existed with no UI, so server-to-server keys needed
	 * curl plus a session cookie. Create shows the secret once — the server
	 * keeps only a hash and cannot show it again — and revoke keeps the row
	 * for audit, which is why the list marks revoked keys rather than
	 * dropping them.
	 */
	const keys = createListApiKeys<{ items: APIKeyResource[] }>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected API-key response');
				return { items: response.data.items ?? [] };
			}
		}
	}));
	const identity = createGetMe<PrincipalResource>(() => ({
		query: {
			retry: false,
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected identity response');
				return response.data;
			}
		}
	}));
	const health = createGetHealth<HealthOutputBody>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected health response');
				return response.data;
			}
		}
	}));

	let createOpen = $state(false);
	let label = $state('');
	let creating = $state(false);
	let createError = $state<string | null>(null);
	let minted = $state<CreatedAPIKeyResource | null>(null);
	let copied = $state(false);
	let revoking = $state<APIKeyResource | null>(null);
	let revokingBusy = $state(false);
	let pageError = $state<string | null>(null);

	const rows = $derived(keys.data?.items ?? []);
	const authOff = $derived(keys.isError || identity.isError);

	async function submitCreate() {
		const trimmed = label.trim();
		if (!trimmed) {
			createError = 'Give this key a label to continue.';
			return;
		}
		creating = true;
		createError = null;
		try {
			const response = await createApiKey({ label: trimmed });
			if (response.status !== 201) throw new Error('Unexpected API-key response');
			minted = response.data;
			label = '';
			await keys.refetch();
		} catch (error) {
			createError = message(error);
		} finally {
			creating = false;
		}
	}

	function closeCreate() {
		createOpen = false;
		createError = null;
		minted = null;
		copied = false;
	}

	async function copyToken() {
		if (!minted) return;
		try {
			await navigator.clipboard.writeText(minted.token);
			copied = true;
		} catch {
			copied = false;
		}
	}

	async function confirmRevoke() {
		if (!revoking || revokingBusy) return;
		revokingBusy = true;
		pageError = null;
		try {
			await revokeApiKey(revoking.id);
			revoking = null;
			await keys.refetch();
		} catch (error) {
			pageError = message(error);
		} finally {
			revokingBusy = false;
		}
	}

	async function signOut() {
		try {
			await logout();
		} finally {
			window.location.assign('/login');
		}
	}
</script>

<svelte:head>
	<title>Settings · KilasFlow</title>
</svelte:head>

<section class="mx-auto w-full max-w-5xl">
	<div class="max-w-xl">
		<h1 class="text-base font-semibold tracking-tight">Settings</h1>
		<p class="text-xs text-muted-foreground">Account, API keys for server-to-server calls, and this instance.</p>
	</div>
	{#if pageError}
		<p role="alert" class="mt-4 rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">{pageError}</p>
	{/if}

	<div class="mt-6 grid gap-6">
		<section aria-label="Account" class="rounded-xl border border-border bg-card p-4">
			<h2 class="text-sm font-semibold">Account</h2>
			{#if identity.isPending}
				<p class="mt-1 text-xs text-muted-foreground">Loading account…</p>
			{:else if identity.isError}
				<p class="mt-1 text-xs leading-5 text-muted-foreground">
					Authentication looks off on this instance — every API call is unauthenticated, so there is no
					signed-in account to show. Enable auth on the server to sign in.
				</p>
			{:else if identity.data}
				<dl class="mt-2 grid gap-1 text-xs">
					{#if identity.data.email}
						<div class="flex gap-2"><dt class="w-20 shrink-0 text-muted-foreground">Email</dt><dd class="min-w-0 truncate">{identity.data.email}</dd></div>
					{/if}
					<div class="flex gap-2"><dt class="w-20 shrink-0 text-muted-foreground">Tenant</dt><dd class="min-w-0 truncate font-mono text-[0.6875rem]">{identity.data.tenantId}</dd></div>
					<div class="flex gap-2"><dt class="w-20 shrink-0 text-muted-foreground">Signed in</dt><dd>as {identity.data.kind}</dd></div>
				</dl>
				<Button variant="outline" size="sm" class="mt-3" onclick={() => void signOut()}>
					<LogOut aria-hidden="true" />
					Sign out
				</Button>
			{/if}
		</section>

		<section aria-label="API keys" class="rounded-xl border border-border bg-card p-4">
			<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
				<div>
					<h2 class="text-sm font-semibold">API keys</h2>
					<p class="mt-0.5 text-xs leading-5 text-muted-foreground">
						Keys authenticate server-to-server calls. A key's secret is shown once and never again.
					</p>
				</div>
				<Dialog.Root bind:open={createOpen} onOpenChange={(open) => !open && closeCreate()}>
					<Dialog.Trigger>
						{#snippet child({ props })}
							<Button {...props} size="sm">
								<KeyRound aria-hidden="true" />
								New API key
							</Button>
						{/snippet}
					</Dialog.Trigger>
					<Dialog.Content aria-describedby="new-api-key-description">
						<Dialog.Header>
							<Dialog.Title>Create an API key</Dialog.Title>
							<Dialog.Description id="new-api-key-description">
								Name it for where it will be used. Copy the secret now — it cannot be shown again.
							</Dialog.Description>
						</Dialog.Header>
						{#if minted}
							<div class="grid gap-2">
								<label for="minted-key" class="text-sm font-medium">Copy this now</label>
								<div class="flex items-center gap-2">
									<code id="minted-key" class="min-w-0 flex-1 truncate rounded-md border border-border bg-muted/40 px-2 py-1.5 font-mono text-xs select-all">{minted.token}</code>
									<Button variant="outline" size="sm" onclick={() => void copyToken()}>
										{#if copied}<Check aria-hidden="true" />Copied{:else}<Copy aria-hidden="true" />Copy{/if}
									</Button>
								</div>
								{#if !copied}
									<p class="text-xs text-muted-foreground">Select the key above and copy it by hand if the button failed.</p>
								{/if}
							</div>
							<Dialog.Footer>
								<Button onclick={closeCreate}>Done</Button>
							</Dialog.Footer>
						{:else}
							<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void submitCreate(); }}>
								<div class="grid gap-2">
									<label for="api-key-label" class="text-sm font-medium">Label</label>
									<Input id="api-key-label" bind:value={label} maxlength={255} placeholder="e.g. CI deploy" />
									{#if createError}<p role="alert" class="text-sm text-destructive">{createError}</p>{/if}
								</div>
								<Dialog.Footer>
									<Button type="button" variant="outline" onclick={closeCreate} disabled={creating}>Cancel</Button>
									<Button type="submit" disabled={creating}>{creating ? 'Creating…' : 'Create key'}</Button>
								</Dialog.Footer>
							</form>
						{/if}
					</Dialog.Content>
				</Dialog.Root>
			</div>
			<div class="mt-4">
				<ListStates
					label="API keys"
					loading={keys.isPending}
					failed={keys.isError}
					error={keys.error}
					count={rows.length}
					rows={2}
					onRetry={() => void keys.refetch()}
					emptyIcon={KeyRound}
					emptyTitle={authOff ? 'API keys need authentication' : 'No API keys yet'}
					emptyBody={authOff
						? 'This instance has auth off, so keys cannot be issued. Enable auth on the server first.'
						: 'Create one to call the API from a backend or CI job.'}
				>
					<ul aria-label="API keys" class="divide-y divide-border overflow-hidden rounded-lg border border-border">
						{#each rows as key (key.id)}
							<li class="flex min-h-11 items-center gap-3 px-3 py-1.5">
								<div class="min-w-0 flex-1">
									<p class="truncate text-sm font-medium">{key.label}</p>
									<p class="truncate text-xs text-muted-foreground" title={`${key.prefix} · created ${key.createdAt}${key.lastUsedAt ? ` · last used ${key.lastUsedAt}` : ''}${key.revokedAt ? ' · revoked' : ''}`}>
										<span class="font-mono">{key.prefix}</span>
										· created {formatTimestamp(key.createdAt)}
										{#if key.lastUsedAt}· last used {formatTimestamp(key.lastUsedAt)}{/if}
										{#if key.revokedAt}<span class="ml-1 rounded border border-border bg-muted px-1 py-px text-[0.625rem]">revoked</span>{/if}
									</p>
								</div>
								{#if !key.revokedAt}
									<Button variant="ghost" size="sm" class="text-destructive" onclick={() => (revoking = key)}>Revoke</Button>
								{/if}
							</li>
						{/each}
					</ul>
				</ListStates>
			</div>
		</section>

		<section aria-label="About this instance" class="rounded-xl border border-border bg-card p-4">
			<h2 class="text-sm font-semibold">About this instance</h2>
			{#if health.isPending}
				<p class="mt-1 text-xs text-muted-foreground">Loading instance info…</p>
			{:else if health.isError}
				<p class="mt-1 text-xs text-muted-foreground">The health endpoint did not answer.</p>
			{:else if health.data}
				<dl class="mt-2 grid gap-1 text-xs">
					<div class="flex gap-2"><dt class="w-20 shrink-0 text-muted-foreground">Status</dt><dd>{health.data.status}</dd></div>
					<div class="flex gap-2"><dt class="w-20 shrink-0 text-muted-foreground">Version</dt><dd class="font-mono text-[0.6875rem]">{health.data.version}</dd></div>
				</dl>
			{/if}
		</section>
	</div>

	<Dialog.Root open={revoking !== null} onOpenChange={(open) => !open && (revoking = null)}>
		<Dialog.Content aria-describedby="revoke-key-description">
			<Dialog.Header>
				<Dialog.Title>Revoke {revoking?.label ?? 'key'}?</Dialog.Title>
				<Dialog.Description id="revoke-key-description">
					It stops authenticating immediately and permanently. The row is kept so an audit still has
					something to name.
				</Dialog.Description>
			</Dialog.Header>
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (revoking = null)} disabled={revokingBusy}>Cancel</Button>
				<Button type="button" variant="destructive" onclick={() => void confirmRevoke()} disabled={revokingBusy}>
					{revokingBusy ? 'Revoking…' : 'Revoke'}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
</section>
