<script lang="ts">
	import { tick } from 'svelte';
	import Search from '@lucide/svelte/icons/search';
	import X from '@lucide/svelte/icons/x';

	import type { Definition } from '$lib/api/generated/models';

	import NodeIcon from './node-icon.svelte';

	let {
		definitions,
		open = $bindable(false),
		triggersOnly = false,
		connecting = false,
		onSelect,
		onDismiss
	}: {
		definitions: Definition[];
		open?: boolean;
		triggersOnly?: boolean;
		/** Opened from an output port, so only nodes that can follow one are offered. */
		connecting?: boolean;
		onSelect: (definition: Definition) => void;
		onDismiss?: () => void;
	} = $props();

	let query = $state('');
	let searchInput = $state<HTMLInputElement>();
	let dialogElement = $state<HTMLDivElement>();
	let returnFocus = $state<HTMLElement | null>(null);
	let wasOpen = $state(false);
	const normalizedQuery = $derived(query.trim().toLocaleLowerCase());
	const available = $derived(
		definitions.filter((definition) => {
			// Behaviour comes from the group, not the category caption. Deciding
			// whether a node can start a workflow by comparing a display string
			// meant a node filed anywhere else could never be a trigger, however
			// it behaved.
			if (triggersOnly && !(definition.group ?? []).includes('trigger')) return false;
			// A step is being added after an existing one, so anything without a
			// main input could never receive its items.
			if (connecting && !(definition.inputs ?? []).some((port) => port.kind === 'main')) return false;
			if (!normalizedQuery) return true;
			return `${definition.displayName} ${definition.description ?? ''} ${definition.category} ${definition.type}`.toLocaleLowerCase().includes(normalizedQuery);
		})
	);
	const categories = $derived([...new Set(available.map((definition) => definition.category))].sort());

	$effect(() => {
		if (open && !wasOpen) {
			returnFocus = globalThis.document.activeElement instanceof HTMLElement ? globalThis.document.activeElement : null;
			void tick().then(() => searchInput?.focus());
		}
		wasOpen = open;
	});

	function close() {
		open = false;
		query = '';
		onDismiss?.();
		void tick().then(() => returnFocus?.focus());
	}

	function choose(definition: Definition) {
		open = false;
		query = '';
		// Deliberately not `onDismiss`: that is the parent's "nothing was chosen"
		// path and it discards the port this picker was opened from, which
		// `onSelect` still needs. The parent also owns focus from here, because
		// the control that opened the picker is usually destroyed by the step it
		// adds — restoring focus to it would silently drop focus on the body.
		onSelect(definition);
	}

	function handleKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape') close();
		if (event.key === 'Tab') trapFocus(event, dialogElement);
	}

	function trapFocus(event: KeyboardEvent, container: HTMLElement | undefined) {
		if (!container) return;
		const focusable = [...container.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])')];
		const first = focusable[0];
		const last = focusable.at(-1);
		if (!first || !last) return;
		if (event.shiftKey && globalThis.document.activeElement === first) {
			event.preventDefault();
			last.focus();
		} else if (!event.shiftKey && globalThis.document.activeElement === last) {
			event.preventDefault();
			first.focus();
		}
	}
</script>

{#if open}
	<div class="absolute inset-0 z-40 grid place-items-center bg-background/60 p-4 backdrop-blur-sm" role="presentation">
		<!-- Clicking away is how a picker is expected to close. Hidden from
		     assistive technology because Escape and the close button already
		     serve that path. -->
		<button type="button" tabindex="-1" aria-hidden="true" class="absolute inset-0 cursor-default" onclick={close}></button>
		<div bind:this={dialogElement} class="relative flex max-h-[min(30rem,calc(100dvh-2rem))] w-full max-w-md flex-col overflow-hidden rounded-xl border border-border bg-popover shadow-2xl" role="dialog" aria-modal="true" aria-labelledby="node-picker-title" tabindex="-1" onkeydown={handleKeydown}>
			<div class="flex items-center gap-2 border-b border-border px-2.5 py-2">
				<Search aria-hidden="true" class="size-4 shrink-0 text-muted-foreground" />
				<label class="sr-only" for="node-picker-search">Search registered node types</label>
				<input id="node-picker-search" bind:this={searchInput} bind:value={query} class="h-6 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground" placeholder={triggersOnly ? 'Search triggers' : 'Search nodes'} />
				<h2 id="node-picker-title" class="sr-only">{triggersOnly ? 'Choose a trigger' : connecting ? 'Add a connected step' : 'Add a step'}</h2>
				<button type="button" class="grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1" aria-label="Close node picker" onclick={close}>
					<X aria-hidden="true" class="size-3.5" />
				</button>
			</div>

			<div class="min-h-0 flex-1 overflow-y-auto p-1">
				{#if available.length === 0}
					<p class="px-3 py-10 text-center text-xs text-muted-foreground">
						{#if normalizedQuery}No registered node matches “{query}”.{:else}No node here can follow that port.{/if}
					</p>
				{:else}
					{#each categories as category (category)}
						<section aria-labelledby={`node-category-${category}`}>
							<h3 id={`node-category-${category}`} class="px-2.5 pb-1 pt-2.5 text-[0.625rem] font-semibold uppercase tracking-wider text-muted-foreground">{category}</h3>
							{#each available.filter((definition) => definition.category === category) as definition (`${definition.type}@${definition.version}`)}
								<button type="button" class="flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-accent focus-visible:bg-accent focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring" onclick={() => choose(definition)}>
									<NodeIcon {definition} size="sm" />
									<span class="min-w-0 flex-1">
										<span class="block truncate text-[0.8125rem] font-medium leading-tight">{definition.displayName}</span>
										{#if definition.description}
											<span class="mt-0.5 block truncate text-[0.6875rem] leading-tight text-muted-foreground">{definition.description}</span>
										{/if}
									</span>
								</button>
							{/each}
						</section>
					{/each}
				{/if}
			</div>

			<p class="shrink-0 border-t border-border px-2.5 py-1.5 text-[0.625rem] text-muted-foreground">
				{available.length} of {definitions.length} nodes · from this workspace’s server registry
			</p>
		</div>
	</div>
{/if}
