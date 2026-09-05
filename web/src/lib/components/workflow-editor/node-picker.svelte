<script lang="ts">
	import { tick } from 'svelte';
	import Search from '@lucide/svelte/icons/search';

	import type { Definition } from '$lib/api/generated/models';

	let {
		definitions,
		open = $bindable(false),
		triggersOnly = false,
		onSelect
	}: {
		definitions: Definition[];
		open?: boolean;
		triggersOnly?: boolean;
		onSelect: (definition: Definition) => void;
	} = $props();

	let query = $state('');
	let searchInput = $state<HTMLInputElement>();
	let dialogElement = $state<HTMLDivElement>();
	let returnFocus = $state<HTMLElement | null>(null);
	let wasOpen = $state(false);
	const normalizedQuery = $derived(query.trim().toLocaleLowerCase());
	const available = $derived(
		definitions.filter((definition) => {
			if (triggersOnly && definition.category !== 'Triggers') return false;
			if (!normalizedQuery) return true;
			return `${definition.displayName} ${definition.description ?? ''} ${definition.category}`.toLocaleLowerCase().includes(normalizedQuery);
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
		void tick().then(() => returnFocus?.focus());
	}

	function choose(definition: Definition) {
		onSelect(definition);
		close();
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
	<div class="absolute inset-0 z-40 grid place-items-center bg-background/50 p-4 backdrop-blur-sm" role="presentation">
		<div bind:this={dialogElement} class="flex max-h-[min(42rem,calc(100dvh-2rem))] w-full max-w-xl flex-col overflow-hidden rounded-2xl border border-border bg-popover shadow-xl" role="dialog" aria-modal="true" aria-labelledby="node-picker-title" tabindex="-1" onkeydown={handleKeydown}>
			<div class="border-b border-border p-4">
				<div class="flex items-start gap-3">
					<div class="min-w-0 flex-1">
						<h2 id="node-picker-title" class="text-base font-semibold">{triggersOnly ? 'Choose a trigger' : 'Add a step'}</h2>
						<p class="mt-1 text-sm text-muted-foreground">The available nodes come from this workspace’s server registry.</p>
					</div>
					<button type="button" class="rounded-md px-2 py-1 text-sm text-muted-foreground hover:bg-muted focus-visible:outline-2" onclick={close}>Close</button>
				</div>
				<label class="relative mt-4 block">
					<Search aria-hidden="true" class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
					<span class="sr-only">Search registered node types</span>
					<input bind:this={searchInput} bind:value={query} class="h-10 w-full rounded-lg border border-input bg-background pl-9 pr-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring" placeholder="Search nodes" />
				</label>
			</div>

			<div class="min-h-0 overflow-y-auto p-2">
				{#if available.length === 0}
					<p class="px-3 py-8 text-center text-sm text-muted-foreground">No registered nodes match “{query}”.</p>
				{:else}
					{#each categories as category}
						<section aria-labelledby={`node-category-${category}`} class="py-2">
							<h3 id={`node-category-${category}`} class="px-3 pb-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{category}</h3>
							{#each available.filter((definition) => definition.category === category) as definition (`${definition.type}@${definition.version}`)}
								<button type="button" class="block w-full rounded-xl px-3 py-3 text-left hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring" onclick={() => choose(definition)}>
									<span class="block text-sm font-medium">{definition.displayName}</span>
									<span class="mt-1 block text-xs leading-5 text-muted-foreground">{definition.description || definition.type}</span>
								</button>
							{/each}
						</section>
					{/each}
				{/if}
			</div>
		</div>
	</div>
{/if}
