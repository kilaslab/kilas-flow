<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import LogIn from '@lucide/svelte/icons/log-in';

	import { message } from '$lib/api/http';
	import { login } from '$lib/api/generated/auth/auth';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as m from '$lib/paraglide/messages.js';

	/**
	 * Sign-in for auth-enabled deployments. Without this route the dashboard
	 * renders its shell and every call fails with 401; the only way in was
	 * curl. On success the user returns to where the 401 found them (`next`).
	 */
	const next = $derived(page.url.searchParams.get('next') || '/app/workflows');

	let email = $state('');
	let password = $state('');
	let signingIn = $state(false);
	let error = $state<string | null>(null);

	async function submit(event: Event) {
		event.preventDefault();
		if (!email.trim() || !password) {
			error = m.auth_credentials_required();
			return;
		}
		signingIn = true;
		error = null;
		try {
			const response = await login({ email: email.trim(), password });
			if (response.status !== 200) throw new Error(m.auth_error_unexpected_response());
			const target = next.startsWith('/') && !next.startsWith('//') ? next : '/app/workflows';
			await goto(target);
		} catch (thrown) {
			error = message(thrown);
		} finally {
			signingIn = false;
		}
	}
</script>

<svelte:head>
	<title>{m.auth_page_title()}</title>
</svelte:head>

<main class="mx-auto grid min-h-dvh w-full max-w-sm place-items-center px-4 py-16">
	<section aria-label={m.auth_sign_in()} class="w-full rounded-xl border border-border bg-card p-6 shadow-sm">
		<div class="flex items-center gap-2">
			<span aria-hidden="true" class="grid size-6 place-items-center rounded bg-primary text-xs font-bold text-primary-foreground">K</span>
			<h1 class="text-base font-semibold tracking-tight">{m.auth_sign_in_to()}</h1>
		</div>
		<p class="mt-1 text-xs leading-5 text-muted-foreground">
			{m.auth_sign_in_explanation()}
		</p>
		<form class="mt-5 grid gap-4" onsubmit={submit}>
			<div class="grid gap-2">
				<label for="login-email" class="text-sm font-medium">{m.auth_email()}</label>
				<Input id="login-email" type="email" autocomplete="username" bind:value={email} placeholder={m.auth_email_placeholder()} />
			</div>
			<div class="grid gap-2">
				<label for="login-password" class="text-sm font-medium">{m.auth_password()}</label>
				<Input id="login-password" type="password" autocomplete="current-password" bind:value={password} placeholder="••••••••" />
			</div>
			{#if error}
				<p role="alert" class="text-sm text-destructive">{error}</p>
			{/if}
			<Button type="submit" disabled={signingIn}>
				<LogIn aria-hidden="true" />
				{signingIn ? m.auth_signing_in() : m.auth_sign_in()}
			</Button>
		</form>
	</section>
</main>
