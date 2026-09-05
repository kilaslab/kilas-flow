<script lang="ts">
	import { message } from '$lib/api/http';
	import {
		createGetHealth,
		createGetReady
	} from '$lib/api/generated/system/system';
	import type { HealthOutputBody, ReadyOutputBody } from '$lib/api/generated/models';

	const health = createGetHealth<HealthOutputBody>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected health response');
				return response.data;
			}
		}
	}));
	const ready = createGetReady<ReadyOutputBody>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected readiness response');
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
	// backend answers, so "Backend unreachable" says more here than the shared
	// sentence would — but the status formatting is no longer its business.
	function errorMessage(error: unknown): string {
		return error instanceof Error ? message(error) : 'Backend unreachable';
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
				Embeddable workflow automation engine
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
			{connected ? 'Connected' : health.isPending ? 'Checking' : 'Disconnected'}
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
				Backend status
			</h2>
			<button
				onclick={() => void refresh()}
				class="rounded-md px-2 py-1 text-xs text-(--color-ink-muted) transition-colors
				       hover:bg-(--color-surface) hover:text-(--color-ink)"
			>
				Refresh
			</button>
		</div>

		<dl class="divide-y divide-(--color-border-subtle) text-sm">
			<div class="flex items-baseline justify-between gap-4 px-4 py-3">
				<dt class="font-medium">
					GET <code class="text-(--color-ink-muted)">/api/v1/health</code>
				</dt>
				<dd class="text-right">
					{#if health.isPending}
						<span class="text-(--color-ink-muted)">checking…</span>
					{:else if health.isSuccess}
						<span class="text-emerald-600 dark:text-emerald-400">
							{health.data.status}
						</span>
						<span class="ml-2 text-xs text-(--color-ink-muted)">
							v{health.data.version}
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
						<span class="text-(--color-ink-muted)">checking…</span>
					{:else if ready.isSuccess}
						<span class="text-emerald-600 dark:text-emerald-400">{ready.data.status}</span>
						<span class="ml-2 text-xs text-(--color-ink-muted)">
							database {ready.data.database}
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
			The API did not respond. Start the backend with <code>make dev-api</code>, or
			run both processes with <code>make dev</code>.
		</p>
	{/if}

	<nav class="flex flex-wrap gap-x-5 gap-y-2 text-sm">
		<a class="text-(--color-accent) hover:underline" href="/docs">API reference</a>
		<a class="text-(--color-accent) hover:underline" href="/api/openapi.json">OpenAPI JSON</a>
		<a class="text-(--color-accent) hover:underline" href="/api/openapi.yaml">OpenAPI YAML</a>
	</nav>

	<footer class="text-xs text-(--color-ink-muted)">
		{#if checkedAt}
			Last checked {checkedAt.toLocaleTimeString()}.
		{/if}
		Scaffolding only — the workflow canvas arrives in Milestone 1.
	</footer>
</main>
