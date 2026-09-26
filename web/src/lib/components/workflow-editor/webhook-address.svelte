<script lang="ts">
	import Check from '@lucide/svelte/icons/check';
	import Copy from '@lucide/svelte/icons/copy';

	import { Button } from '$lib/components/ui/button';
	import * as m from '$lib/paraglide/messages.js';
	import type { WebhookAddress } from '$lib/workflow-editor/webhook-address';

	/**
	 * A webhook node's public address, or the reason there is not one yet.
	 *
	 * It renders a decision made elsewhere and fetches nothing, so the panel's
	 * two hosts and the tests all see the same text for the same answer. The
	 * one thing it never does is print a path-shaped URL: see `WebhookAddress`.
	 */
	let {
		address,
		onRetry
	}: {
		address: WebhookAddress;
		/** Offered when the lookup failed, which is the one state the user can act on from here. */
		onRetry?: () => void;
	} = $props();

	let copied = $state(false);
	let copyFailed = $state(false);
	let resetTimer: ReturnType<typeof setTimeout> | undefined;

	// The confirmation would otherwise fire against a panel that has been torn
	// down, which Svelte reports as a state update outside an effect.
	$effect(() => () => clearTimeout(resetTimer));

	async function copy(url: string) {
		clearTimeout(resetTimer);
		try {
			await navigator.clipboard.writeText(url);
			copyFailed = false;
		} catch {
			// A page served over plain HTTP has no clipboard API at all. The URL
			// sits beside the button as selectable text for exactly that case, so
			// the failure is named rather than leaving the user to paste whatever
			// was on the clipboard before.
			copyFailed = true;
		}
		copied = true;
		resetTimer = setTimeout(() => {
			copied = false;
			copyFailed = false;
		}, 2500);
	}
</script>

<div class="grid gap-1.5 rounded-lg border border-border bg-background/40 p-2" data-testid="webhook-address">
	<p class="text-[0.6875rem] font-medium uppercase tracking-wider text-muted-foreground">{m.properties_webhook_url()}</p>
	{#if address.kind === 'ready'}
		<div class="flex items-start gap-1.5">
			<!-- Wrapped rather than truncated: the tail of the URL is the minted
			     route, which is the one part a sender cannot guess. -->
			<code class="min-w-0 flex-1 break-all rounded border border-border bg-muted/40 px-1.5 py-1 font-mono text-[0.6875rem] leading-4 select-all" data-testid="webhook-url">{address.url}</code>
			<Button type="button" variant="outline" size="sm" class="h-7 shrink-0 px-2 text-[0.6875rem]" onclick={() => void copy(address.url)}>
				{#if copied && !copyFailed}
					<Check aria-hidden="true" class="size-3" />{m.workflows_copied()}
				{:else}
					<Copy aria-hidden="true" class="size-3" />{m.workflows_copy_url()}
				{/if}
			</Button>
		</div>
		{#if copied && copyFailed}
			<p role="alert" class="text-[0.625rem] leading-4 text-destructive">{m.editor_clipboard_unavailable()}</p>
		{/if}
		<!-- Top-aligned, because the note wraps in the narrow panel and a
		     centred dot would float between its two lines. -->
		<p class="flex items-start gap-1.5 text-[0.625rem] leading-4 text-muted-foreground">
			<span aria-hidden="true" class="mt-[0.3125rem] size-1.5 shrink-0 rounded-full {address.live ? 'bg-success' : 'bg-muted-foreground/40'}"></span>
			{address.live ? m.properties_webhook_live() : m.properties_webhook_inactive()}
		</p>
	{:else if address.kind === 'loading'}
		<p role="status" class="text-[0.625rem] leading-4 text-muted-foreground">{m.properties_webhook_loading()}</p>
	{:else if address.kind === 'unavailable'}
		<p class="text-[0.625rem] leading-4 text-muted-foreground">{m.properties_webhook_unavailable()}</p>
		{#if onRetry}
			<Button type="button" variant="outline" size="sm" class="h-7 justify-self-start px-2 text-[0.6875rem]" onclick={onRetry}>{m.common_try_again()}</Button>
		{/if}
	{:else if address.kind === 'unbound'}
		<p class="text-[0.625rem] leading-4 text-muted-foreground">{m.properties_webhook_unbound()}</p>
	{:else if address.kind === 'needs-save'}
		<p class="text-[0.625rem] leading-4 text-muted-foreground">{m.properties_webhook_needs_save()}</p>
	{:else}
		<p class="text-[0.625rem] leading-4 text-muted-foreground">{m.properties_webhook_set_path()}</p>
	{/if}
</div>
