<script lang="ts">
	import { onMount } from 'svelte';
	import Copy from '@lucide/svelte/icons/copy';
	import Check from '@lucide/svelte/icons/check';
	import KeyRound from '@lucide/svelte/icons/key-round';
	import LogOut from '@lucide/svelte/icons/log-out';

	import { message } from '$lib/api/http';
	import {
		createApiKey,
		createGetMe,
		listApiKeys,
		logout,
		revokeApiKey
	} from '$lib/api/generated/auth/auth';
	import { createGetHealth } from '$lib/api/generated/system/system';
	import type { APIKeyResource, CreatedAPIKeyResource, HealthOutputBody, PrincipalResource } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { DRAIN_PAGE_LIMIT, drainPages, headerCursor, readPage, type CursorPage } from '$lib/dashboard/cursor-page';
	import { RequestGuard } from '$lib/dashboard/request-guard';
	import * as m from '$lib/paraglide/messages.js';
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
	let rows = $state<APIKeyResource[]>([]);
	let loading = $state(true);
	let listFailure = $state<unknown>(null);
	const listGuard = new RequestGuard();

	/** One page of the key listing: rows from the body, cursor from the header. */
	async function fetchKeyPage(cursor: string): Promise<CursorPage<APIKeyResource>> {
		const response = await listApiKeys({ limit: DRAIN_PAGE_LIMIT, cursor: cursor || undefined });
		if (response.status !== 200) throw new Error(m.settings_error_unexpected_key());
		return readPage({ items: response.data.items, nextCursor: headerCursor(response.headers) });
	}

	async function loadKeys() {
		const token = listGuard.start();
		listFailure = null;
		loading = rows.length === 0;
		try {
			const items = await drainPages(fetchKeyPage);
			if (!listGuard.holds(token)) return;
			rows = items;
		} catch (cause) {
			if (!listGuard.holds(token)) return;
			listFailure = cause;
		} finally {
			if (listGuard.holds(token)) loading = false;
		}
	}

	onMount(() => void loadKeys());

	const identity = createGetMe<PrincipalResource>(() => ({
		query: {
			retry: false,
			select: (response) => {
				if (response.status !== 200) throw new Error(m.settings_error_unexpected_identity());
				return response.data;
			}
		}
	}));
	const health = createGetHealth<HealthOutputBody>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error(m.settings_error_unexpected_health());
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

	const authOff = $derived(listFailure !== null || identity.isError);

	async function submitCreate() {
		const trimmed = label.trim();
		if (!trimmed) {
			createError = m.settings_label_required();
			return;
		}
		creating = true;
		createError = null;
		try {
			const response = await createApiKey({ label: trimmed });
			if (response.status !== 201) throw new Error(m.settings_error_unexpected_key());
			minted = response.data;
			label = '';
			await loadKeys();
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
			await loadKeys();
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

	/**
	 * The hover text for a key row: the facts the row shows, unstyled and
	 * untruncated. Assembled from the row's own messages rather than a second
	 * English template, so the tooltip and the line it stands in for agree in
	 * every locale. The revoked badge carries no separator of its own — in the
	 * row it sits in a chip — so the tooltip puts one back.
	 */
	function keyTitle(key: APIKeyResource): string {
		const parts = [key.prefix, m.settings_key_created({ at: key.createdAt })];
		if (key.lastUsedAt) parts.push(m.settings_key_last_used({ at: key.lastUsedAt }));
		if (key.revokedAt) parts.push(`· ${m.settings_key_revoked()}`);
		return parts.join(' ');
	}
</script>

<svelte:head>
	<title>{m.settings_page_title()}</title>
</svelte:head>

<section class="mx-auto w-full max-w-5xl">
	<div class="max-w-xl">
		<h1 class="text-base font-semibold tracking-tight">{m.nav_settings()}</h1>
		<p class="text-xs text-muted-foreground">{m.settings_intro()}</p>
	</div>
	{#if pageError}
		<p role="alert" class="mt-4 rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">{pageError}</p>
	{/if}

	<div class="mt-6 grid gap-6">
		<section aria-label={m.settings_account()} class="rounded-xl border border-border bg-card p-4">
			<h2 class="text-sm font-semibold">{m.settings_account()}</h2>
			{#if identity.isPending}
				<p class="mt-1 text-xs text-muted-foreground">{m.settings_account_loading()}</p>
			{:else if identity.isError}
				<p class="mt-1 text-xs leading-5 text-muted-foreground">
					{m.settings_account_auth_off()}
				</p>
			{:else if identity.data}
				<dl class="mt-2 grid gap-1 text-xs">
					{#if identity.data.email}
						<div class="flex gap-2"><dt class="w-20 shrink-0 text-muted-foreground">{m.settings_email()}</dt><dd class="min-w-0 truncate">{identity.data.email}</dd></div>
					{/if}
					<div class="flex gap-2"><dt class="w-20 shrink-0 text-muted-foreground">{m.settings_tenant()}</dt><dd class="min-w-0 truncate font-mono text-[0.6875rem]">{identity.data.tenantId}</dd></div>
					<div class="flex gap-2"><dt class="w-20 shrink-0 text-muted-foreground">{m.common_signed_in()}</dt><dd>{m.settings_signed_in_as({ kind: identity.data.kind })}</dd></div>
				</dl>
				<Button variant="outline" size="sm" class="mt-3" onclick={() => void signOut()}>
					<LogOut aria-hidden="true" />
					{m.common_sign_out()}
				</Button>
			{/if}
		</section>

		<section aria-label={m.settings_api_keys()} class="rounded-xl border border-border bg-card p-4">
			<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
				<div>
					<h2 class="text-sm font-semibold">{m.settings_api_keys()}</h2>
					<p class="mt-0.5 text-xs leading-5 text-muted-foreground">{m.settings_api_keys_description()}</p>
				</div>
				<Dialog.Root bind:open={createOpen} onOpenChange={(open) => !open && closeCreate()}>
					<Dialog.Trigger>
						{#snippet child({ props })}
							<Button {...props} size="sm">
								<KeyRound aria-hidden="true" />
								{m.settings_new_key()}
							</Button>
						{/snippet}
					</Dialog.Trigger>
					<Dialog.Content aria-describedby="new-api-key-description">
						<Dialog.Header>
							<Dialog.Title>{m.settings_create_key()}</Dialog.Title>
							<Dialog.Description id="new-api-key-description">
								{m.settings_create_key_description()}
							</Dialog.Description>
						</Dialog.Header>
						{#if minted}
							<div class="grid gap-2">
								<label for="minted-key" class="text-sm font-medium">{m.settings_copy_now()}</label>
								<div class="flex items-center gap-2">
									<code id="minted-key" class="min-w-0 flex-1 truncate rounded-md border border-border bg-muted/40 px-2 py-1.5 font-mono text-xs select-all">{minted.token}</code>
									<Button variant="outline" size="sm" onclick={() => void copyToken()}>
										{#if copied}<Check aria-hidden="true" />{m.settings_copied()}{:else}<Copy aria-hidden="true" />{m.settings_copy()}{/if}
									</Button>
								</div>
								{#if !copied}
									<p class="text-xs text-muted-foreground">{m.settings_copy_by_hand()}</p>
								{/if}
							</div>
							<Dialog.Footer>
								<Button onclick={closeCreate}>{m.settings_done()}</Button>
							</Dialog.Footer>
						{:else}
							<form class="grid gap-4" onsubmit={(event) => { event.preventDefault(); void submitCreate(); }}>
								<div class="grid gap-2">
									<label for="api-key-label" class="text-sm font-medium">{m.settings_label()}</label>
									<Input id="api-key-label" bind:value={label} maxlength={255} placeholder={m.settings_label_placeholder()} />
									{#if createError}<p role="alert" class="text-sm text-destructive">{createError}</p>{/if}
								</div>
								<Dialog.Footer>
									<Button type="button" variant="outline" onclick={closeCreate} disabled={creating}>{m.settings_cancel()}</Button>
									<Button type="submit" disabled={creating}>{creating ? m.settings_creating() : m.settings_create_key_action()}</Button>
								</Dialog.Footer>
							</form>
						{/if}
					</Dialog.Content>
				</Dialog.Root>
			</div>
			<div class="mt-4">
				<ListStates
					label={m.settings_api_keys()}
					loading={loading}
					failed={listFailure !== null}
					error={listFailure}
					count={rows.length}
					rows={2}
					onRetry={() => void loadKeys()}
					emptyIcon={KeyRound}
					emptyTitle={authOff ? m.settings_keys_need_auth() : m.settings_keys_empty_title()}
					emptyBody={authOff ? m.settings_keys_need_auth_body() : m.settings_keys_empty_body()}
				>
					<ul aria-label={m.settings_api_keys()} class="divide-y divide-border overflow-hidden rounded-lg border border-border">
						{#each rows as key (key.id)}
							<li class="flex min-h-11 items-center gap-3 px-3 py-1.5">
								<div class="min-w-0 flex-1">
									<p class="truncate text-sm font-medium">{key.label}</p>
									<p class="truncate text-xs text-muted-foreground" title={keyTitle(key)}>
										<span class="font-mono">{key.prefix}</span>
										{m.settings_key_created({ at: formatTimestamp(key.createdAt) })}
										{#if key.lastUsedAt}{m.settings_key_last_used({ at: formatTimestamp(key.lastUsedAt) })}{/if}
										{#if key.revokedAt}<span class="ml-1 rounded border border-border bg-muted px-1 py-px text-[0.625rem]">{m.settings_key_revoked()}</span>{/if}
									</p>
								</div>
								{#if !key.revokedAt}
									<Button variant="ghost" size="sm" class="text-destructive" onclick={() => (revoking = key)}>{m.settings_revoke()}</Button>
								{/if}
							</li>
						{/each}
					</ul>
				</ListStates>
			</div>
		</section>

		<section aria-label={m.settings_about()} class="rounded-xl border border-border bg-card p-4">
			<h2 class="text-sm font-semibold">{m.settings_about()}</h2>
			{#if health.isPending}
				<p class="mt-1 text-xs text-muted-foreground">{m.settings_about_loading()}</p>
			{:else if health.isError}
				<p class="mt-1 text-xs text-muted-foreground">{m.settings_about_unavailable()}</p>
			{:else if health.data}
				<dl class="mt-2 grid gap-1 text-xs">
					<div class="flex gap-2"><dt class="w-20 shrink-0 text-muted-foreground">{m.settings_status()}</dt><dd>{health.data.status}</dd></div>
					<div class="flex gap-2"><dt class="w-20 shrink-0 text-muted-foreground">{m.settings_version()}</dt><dd class="font-mono text-[0.6875rem]">{health.data.version}</dd></div>
				</dl>
			{/if}
		</section>
	</div>

	<Dialog.Root open={revoking !== null} onOpenChange={(open) => !open && (revoking = null)}>
		<Dialog.Content aria-describedby="revoke-key-description">
			<Dialog.Header>
				<Dialog.Title>{m.settings_revoke_title({ label: revoking?.label ?? m.settings_key_noun() })}</Dialog.Title>
				<Dialog.Description id="revoke-key-description">
					{m.settings_revoke_description()}
				</Dialog.Description>
			</Dialog.Header>
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (revoking = null)} disabled={revokingBusy}>{m.settings_cancel()}</Button>
				<Button type="button" variant="destructive" onclick={() => void confirmRevoke()} disabled={revokingBusy}>
					{revokingBusy ? m.settings_revoking() : m.settings_revoke()}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
</section>
