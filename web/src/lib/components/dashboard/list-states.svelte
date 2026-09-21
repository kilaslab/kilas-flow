<script lang="ts">
	import type { Component, Snippet } from 'svelte';
	import RefreshCw from '@lucide/svelte/icons/refresh-cw';

	import { message } from '$lib/api/http';
	import { failedBesideRows, listState } from '$lib/dashboard/list-state';
	import { Button } from '$lib/components/ui/button';
	import * as m from '$lib/paraglide/messages.js';

	/**
	 * The loading, failed and empty states of a dashboard list, the frame the
	 * loaded rows sit in, and the notice that reports a failure which arrived
	 * after those rows did.
	 *
	 * It owns the three states rather than the whole page. The page heading
	 * carries a different action on every list — a dialog trigger here, a
	 * refresh button there — and pulling that in would have meant a prop per
	 * variation and a component that is a switch statement wearing a page's
	 * clothes. What actually drifted across four copy-paste events was the
	 * skeleton, the error card and the empty state, and that is what this holds.
	 *
	 * The flags arrive as plain booleans, not as a query object. Three of the
	 * four lists read isPending/isError off TanStack Query; executions drives
	 * the same states by hand because it pages a cursor Query has no opinion
	 * about. Typing these props against a query would have forced that page to
	 * be rewritten to fit, and its out-of-order-response guard is exactly the
	 * kind of thing that vanishes in a rewrite without anything failing.
	 */
	let {
		label,
		loading,
		failed = false,
		error = null,
		count,
		rows = 3,
		onRetry,
		onRetryMore,
		// Capitalised locally so the markup can render it as a component; the
		// prop keeps the lower-case name every other prop here uses.
		emptyIcon: EmptyIcon,
		emptyTitle,
		emptyBody,
		emptyAction,
		children
	}: {
		/** The capitalised plural noun this list holds, e.g. "Workflows". */
		label: string;
		loading: boolean;
		failed?: boolean;
		/**
		 * The rejection itself, not a string. The shell renders it through the
		 * shared message(), which is the only place that decides whether an API
		 * failure shows its status; a page that pre-formatted its own text
		 * would be a sixth opinion on that.
		 */
		error?: unknown;
		/** Rows loaded — what separates "nothing here yet" from "not loaded yet". */
		count: number;
		/**
		 * Skeleton rows. Not a design token but a guess at how long this list
		 * usually is, which differs per surface, so each page keeps its own.
		 */
		rows?: number;
		onRetry: () => void;
		/**
		 * Retries the request behind the notice that sits beside loaded rows,
		 * where that is not the same request as `onRetry`. Executions is the
		 * one list where it differs: `onRetry` reloads from the top, which
		 * would throw away every page after the first to recover the one that
		 * failed. Left unset, the notice retries with `onRetry`, which is
		 * right for the lists whose only request is the one that fetches
		 * everything.
		 */
		onRetryMore?: () => void;
		emptyIcon: Component;
		emptyTitle: string;
		emptyBody: string;
		/** The one thing to do from an empty list, where there is one. */
		emptyAction?: Snippet;
		children: Snippet;
	} = $props();

	const state = $derived(listState({ loading, failed, count }));
	const besideRows = $derived(failedBesideRows({ loading, failed, count }));
</script>

{#if state === 'loading'}
	<!--
		The skeleton is shaped like the list it stands in for — a bordered
		container of row-height blocks — so the layout does not jump when the
		rows arrive. The loading sentence is sr-only rather than visible: the
		skeleton already says "loading" to anyone who can see it, and the live
		region says it to anyone who cannot.
	-->
	<div aria-live="polite" class="overflow-hidden rounded-lg border border-border">
		<p class="sr-only">{m.common_loading({ label: label.toLowerCase() })}</p>
		{#each Array(rows) as _}
			<div class="h-11 animate-pulse border-b border-border bg-muted/50 last:border-0" aria-hidden="true"></div>
		{/each}
	</div>
{:else if state === 'failed'}
	<div class="max-w-lg rounded-lg border border-destructive/25 bg-destructive/5 p-3">
		<h2 class="text-sm font-medium">{m.common_load_failed({ label })}</h2>
		<p class="mt-0.5 text-xs leading-5 text-muted-foreground">{message(error)}</p>
		<Button class="mt-2.5" size="sm" variant="outline" onclick={onRetry}>
			<RefreshCw aria-hidden="true" />
			{m.common_try_again()}
		</Button>
	</div>
{:else if state === 'empty'}
	<div class="grid min-h-56 place-items-center rounded-lg border border-dashed border-border px-6 py-10 text-center">
		<div class="max-w-xs">
			<div aria-hidden="true" class="mx-auto grid size-8 place-items-center rounded-lg bg-accent text-accent-foreground"><EmptyIcon class="size-4" /></div>
			<h2 class="mt-3 text-sm font-semibold tracking-tight">{emptyTitle}</h2>
			<p class="mt-1 text-xs leading-5 text-muted-foreground">{emptyBody}</p>
			{@render emptyAction?.()}
		</div>
	</div>
{:else}
	{@render children()}
	{#if besideRows}
		<!--
			After the rows rather than above them, because that is where the
			rows it failed to fetch would have gone. It is a line and a button
			rather than the error card above: the card is what a user reads
			when there is nothing else on screen, and at this size it would
			outweigh the list it is a footnote to.
		-->
		<div role="alert" class="mt-4 flex flex-wrap items-center justify-center gap-x-3 gap-y-2">
			<p class="text-xs text-destructive">{m.common_load_incomplete({ label, message: message(error) })}</p>
			<Button size="sm" variant="outline" onclick={onRetryMore ?? onRetry}>
				<RefreshCw aria-hidden="true" />
				{m.common_try_again()}
			</Button>
		</div>
	{/if}
{/if}
