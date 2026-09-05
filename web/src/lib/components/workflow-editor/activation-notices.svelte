<script lang="ts">
	import Check from '@lucide/svelte/icons/check';
	import Copy from '@lucide/svelte/icons/copy';
	import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
	import X from '@lucide/svelte/icons/x';

	import { cn } from '$lib/utils';
	import type { ActivationNoticeView } from '$lib/workflow-editor/activation';

	let {
		notices,
		heading,
		class: className,
		onDismiss
	}: {
		notices: ActivationNoticeView[];
		/** Names the workflow on a surface that activates more than one. */
		heading?: string;
		/** The editor stacks this as a toolbar banner; a list wants a card. */
		class?: string;
		onDismiss: (key: string) => void;
	} = $props();

	let copiedKey = $state<string | null>(null);
	let copyFailed = $state(false);
	let resetTimer: ReturnType<typeof setTimeout> | undefined;

	// The confirmation would otherwise fire against a component that has been
	// torn down, which Svelte reports as a state update outside an effect.
	$effect(() => () => clearTimeout(resetTimer));

	async function copy(notice: ActivationNoticeView) {
		if (!notice.url) return;
		clearTimeout(resetTimer);
		try {
			await navigator.clipboard.writeText(notice.url);
			copyFailed = false;
		} catch {
			// A page served over plain HTTP has no clipboard API at all, and a
			// browser can refuse the write outright. The URL sits beside this
			// button as selectable text for exactly that case, so the failure is
			// worth naming rather than leaving the user to wonder whether the
			// paste they are about to make is the old contents of their clipboard.
			copyFailed = true;
		}
		copiedKey = notice.key;
		resetTimer = setTimeout(() => {
			copiedKey = null;
			copyFailed = false;
		}, 2500);
	}
</script>

<!--
	A live region has to be in the DOM before its content arrives, or several
	screen readers announce nothing at all — which is why this is a separate,
	always-mounted element rather than an attribute on the panel below. It is
	positioned out of flow, so it costs the editor's toolbar column nothing.
-->
<p class="sr-only" aria-live="polite">
	{notices.length === 0 ? '' : `${notices.length} activation ${notices.length === 1 ? 'notice' : 'notices'}. The workflow is active, but a trigger still needs something done before it receives anything.`}
</p>
{#if notices.length > 0}
	<section
		aria-label="Activation notices"
		class={cn('shrink-0 border-b border-warning/30 bg-warning/5', className)}
		data-testid="activation-notices"
	>
		{#if heading}
			<p class="border-b border-warning/20 px-3 py-1.5 text-[0.6875rem] font-medium text-muted-foreground">{heading}</p>
		{/if}
		<ul class="divide-y divide-warning/15">
			{#each notices as notice (notice.key)}
				<li class="flex items-start gap-2 px-3 py-2">
					<TriangleAlert aria-hidden="true" class="mt-0.5 size-3.5 shrink-0 text-warning" />
					<div class="min-w-0 flex-1">
						<p class="text-xs leading-5">
							<span class="font-medium">{notice.nodeName}</span>
							<span class="text-muted-foreground"> — {notice.message}</span>
						</p>
						{#if notice.url}
							<div class="mt-1.5 flex items-start gap-1.5">
								<!-- Selectable rather than truncated: when the clipboard is
								     unavailable, reading the URL by eye is the only way left
								     to get it into somebody else's console. -->
								<code class="min-w-0 flex-1 break-all rounded border border-border bg-muted/60 px-1.5 py-0.5 font-mono text-[0.6875rem] leading-4">{notice.url}</code>
								<button
									type="button"
									class="inline-flex h-6 shrink-0 items-center gap-1 whitespace-nowrap rounded-md border border-border bg-card px-1.5 text-[0.6875rem] font-medium transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring"
									onclick={() => void copy(notice)}
								>
									{#if copiedKey === notice.key && !copyFailed}
										<Check aria-hidden="true" class="size-3" />Copied
									{:else}
										<Copy aria-hidden="true" class="size-3" />Copy URL
									{/if}
									<!-- Every notice carries an identical button, so the node is
									     what tells a screen-reader user which URL they are about
									     to put on their clipboard. -->
									<span class="sr-only"> for {notice.nodeName}</span>
								</button>
							</div>
							{#if copiedKey === notice.key && copyFailed}
								<p role="alert" class="mt-1 text-[0.6875rem] text-destructive">The clipboard is unavailable here. Select the URL above and copy it by hand.</p>
							{/if}
						{/if}
					</div>
					<button
						type="button"
						class="grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring"
						aria-label={`Dismiss the activation notice for ${notice.nodeName}`}
						onclick={() => onDismiss(notice.key)}
					>
						<X aria-hidden="true" class="size-3.5" />
					</button>
				</li>
			{/each}
		</ul>
	</section>
{/if}
