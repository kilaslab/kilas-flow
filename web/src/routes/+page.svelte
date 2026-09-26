<script lang="ts">
	import { message } from '$lib/api/http';
	import {
		createGetHealth,
		createGetReady
	} from '$lib/api/generated/system/system';
	import type { HealthOutputBody, ReadyOutputBody } from '$lib/api/generated/models';
	import * as m from '$lib/paraglide/messages.js';

	const health = createGetHealth<HealthOutputBody>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error(m.home_error_unexpected_health());
				return response.data;
			}
		}
	}));
	const ready = createGetReady<ReadyOutputBody>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error(m.home_error_unexpected_readiness());
				return response.data;
			}
		}
	}));

	const checkedAt = $derived(
		health.isSuccess && ready.isSuccess
			? new Date(Math.max(health.dataUpdatedAt, ready.dataUpdatedAt))
			: null
	);

	async function refresh() {
		await Promise.all([health.refetch(), ready.refetch()]);
	}

	const connected = $derived(health.isSuccess);

	// Keeps its own last resort — this page's whole subject is whether the
	// backend answers, so the dedicated message says more here than the shared
	// sentence would — but the status formatting is no longer its business.
	function errorMessage(error: unknown): string {
		return error instanceof Error ? message(error) : m.home_backend_unreachable();
	}
</script>

<svelte:head>
	<title>KilasFlow</title>
</svelte:head>

<main class="mx-auto flex min-h-screen max-w-2xl flex-col justify-center gap-8 px-6 py-16">
	<header class="flex items-start justify-between gap-4">
		<div>
			<h1 class="text-xl font-semibold tracking-tight">KilasFlow</h1>
			<p class="mt-1 text-sm text-(--color-ink-muted)">
				{m.home_tagline()}
			</p>
		</div>

		<span
			class="mt-1 inline-flex shrink-0 items-center gap-2 rounded-full border
			       border-(--color-border-subtle) px-3 py-1 text-xs font-medium"
		>
			<span
				class="size-1.5 rounded-full"
				class:bg-emerald-500={connected}
				class:bg-red-500={health.isError}
				class:bg-amber-400={health.isPending}
			></span>
			{connected
				? m.home_status_connected()
				: health.isPending
					? m.home_status_checking()
					: m.home_status_disconnected()}
		</span>
	</header>

	<!--
		This panel is the frontend-to-backend integration test. It calls the Go
		API through the Vite dev proxy using relative URLs, so if the proxy, the
		route table or the backend regresses, it shows here immediately.
	-->
	<section
		class="overflow-hidden rounded-xl border border-(--color-border-subtle)
		       bg-(--color-surface-raised)"
	>
		<div
			class="flex items-center justify-between border-b border-(--color-border-subtle)
			       px-4 py-2.5"
		>
			<h2 class="text-xs font-semibold tracking-wide uppercase text-(--color-ink-muted)">
				{m.home_status_heading()}
			</h2>
			<button
				onclick={() => void refresh()}
				class="rounded-md px-2 py-1 text-xs text-(--color-ink-muted) transition-colors
				       hover:bg-(--color-surface) hover:text-(--color-ink)"
			>
				{m.home_refresh()}
			</button>
		</div>

		<dl class="divide-y divide-(--color-border-subtle) text-sm">
			<div class="flex items-baseline justify-between gap-4 px-4 py-3">
				<dt class="font-medium">
					GET <code class="text-(--color-ink-muted)">/api/v1/health</code>
				</dt>
				<dd class="text-right">
					{#if health.isPending}
						<span class="text-(--color-ink-muted)">{m.home_checking()}</span>
					{:else if health.isSuccess}
						<span class="text-emerald-600 dark:text-emerald-400">
							{health.data.status}
						</span>
						<span class="ml-2 text-xs text-(--color-ink-muted)">
							{m.home_health_version({ version: health.data.version })}
						</span>
					{:else}
						<span class="text-red-600 dark:text-red-400">{errorMessage(health.error)}</span>
					{/if}
				</dd>
			</div>

			<div class="flex items-baseline justify-between gap-4 px-4 py-3">
				<dt class="font-medium">
					GET <code class="text-(--color-ink-muted)">/api/v1/ready</code>
				</dt>
				<dd class="text-right">
					{#if ready.isPending}
						<span class="text-(--color-ink-muted)">{m.home_checking()}</span>
					{:else if ready.isSuccess}
						<span class="text-emerald-600 dark:text-emerald-400">{ready.data.status}</span>
						<span class="ml-2 text-xs text-(--color-ink-muted)">
							{m.home_ready_database({ name: ready.data.database })}
						</span>
					{:else}
						<span class="text-red-600 dark:text-red-400">{errorMessage(ready.error)}</span>
					{/if}
				</dd>
			</div>
		</dl>
	</section>

	{#if health.isError}
		<p
			class="rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm
			       text-(--color-ink-muted)"
		>
			{m.home_backend_help({ api: 'make dev-api', dev: 'make dev' })}
		</p>
	{/if}

	<nav class="flex flex-wrap gap-x-5 gap-y-2 text-sm">
		<a class="text-primary hover:underline" href="/docs">{m.home_link_api_reference()}</a>
		<a class="text-primary hover:underline" href="/api/openapi.json">{m.home_link_openapi_json()}</a>
		<a class="text-primary hover:underline" href="/api/openapi.yaml">{m.home_link_openapi_yaml()}</a>
	</nav>

	<footer class="text-xs text-(--color-ink-muted)">
		{#if checkedAt}
			{m.home_last_checked({ time: checkedAt.toLocaleTimeString() })}
		{/if}
		{m.home_scaffolding_note()}
	</footer>
</main>
