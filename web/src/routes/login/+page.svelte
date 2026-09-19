<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import LogIn from '@lucide/svelte/icons/log-in';

	import { message } from '$lib/api/http';
	import { login } from '$lib/api/generated/auth/auth';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';

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
			error = 'Enter the account email and password to continue.';
			return;
		}
		signingIn = true;
		error = null;
		try {
			const response = await login({ email: email.trim(), password });
			if (response.status !== 200) throw new Error('Unexpected sign-in response');
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
	<title>Sign in · KilasFlow</title>
</svelte:head>

<main class="mx-auto grid min-h-dvh w-full max-w-sm place-items-center px-4 py-16">
	<section aria-label="Sign in" class="w-full rounded-xl border border-border bg-card p-6 shadow-sm">
		<div class="flex items-center gap-2">
			<span aria-hidden="true" class="grid size-6 place-items-center rounded bg-primary text-xs font-bold text-primary-foreground">K</span>
			<h1 class="text-base font-semibold tracking-tight">Sign in to KilasFlow</h1>
		</div>
		<p class="mt-1 text-xs leading-5 text-muted-foreground">
			This instance requires authentication. Sign in with the operator account from the server configuration.
		</p>
		<form class="mt-5 grid gap-4" onsubmit={submit}>
			<div class="grid gap-2">
				<label for="login-email" class="text-sm font-medium">Email</label>
				<Input id="login-email" type="email" autocomplete="username" bind:value={email} placeholder="ops@example.com" />
			</div>
			<div class="grid gap-2">
				<label for="login-password" class="text-sm font-medium">Password</label>
				<Input id="login-password" type="password" autocomplete="current-password" bind:value={password} placeholder="••••••••" />
			</div>
			{#if error}
				<p role="alert" class="text-sm text-destructive">{error}</p>
			{/if}
			<Button type="submit" disabled={signingIn}>
				<LogIn aria-hidden="true" />
				{signingIn ? 'Signing in…' : 'Sign in'}
			</Button>
		</form>
	</section>
</main>
