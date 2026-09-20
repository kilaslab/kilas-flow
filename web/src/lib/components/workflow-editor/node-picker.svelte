<script lang="ts">
	import { tick } from 'svelte';
	import Search from '@lucide/svelte/icons/search';
	import X from '@lucide/svelte/icons/x';

	import type { Definition } from '$lib/api/generated/models';

	import { catalogEntries, searchCatalog } from '$lib/workflow-editor/catalog';

	import NodeIcon from './node-icon.svelte';

	let {
		definitions,
		open = $bindable(false),
		triggersOnly = false,
		connecting = false,
		providesKind = null,
		onSelect,
		onDismiss
	}: {
		definitions: Definition[];
		open?: boolean;
		triggersOnly?: boolean;
		/** Opened from an output port, so only nodes that can follow one are offered. */
		connecting?: boolean;
		/** Opened from an attachment slot, so only nodes providing this kind are offered. */
		providesKind?: string | null;
		onSelect: (definition: Definition) => void;
		onDismiss?: () => void;
	} = $props();

	let query = $state('');
	let searchInput = $state<HTMLInputElement>();
	let dialogElement = $state<HTMLDivElement>();
	let returnFocus = $state<HTMLElement | null>(null);
	let wasOpen = $state(false);
	// Which row the arrow keys are on. The search box keeps focus and points at
	// it with aria-activedescendant, so typing and arrowing do not fight over the
	// keyboard — the same shape as a combobox.
	let activeIndex = $state(0);

	// One entry per node type, at its latest version, and nothing this
	// deployment cannot run: the picker used to list MySQL twice and to offer
	// four arities of the import placeholder, none of which can be activated.
	const catalog = $derived(catalogEntries(definitions, { triggersOnly, acceptsMain: connecting, providesKind: providesKind ?? undefined }));
	const ranked = $derived(searchCatalog(catalog, query));
	const rows = $derived(buildRows(ranked));
	const active = $derived(rows.flat[activeIndex] ?? null);

	type PickerRow = { definition: Definition; id: string; index: number; heading?: string };

	function buildRows(entries: Definition[]): { flat: PickerRow[]; groups: { category: string; rows: PickerRow[] }[] } {
		const flat: PickerRow[] = [];
		const groups: { category: string; rows: PickerRow[] }[] = [];
		for (const definition of entries) {
			const row: PickerRow = { definition, id: `node-option-${definition.type}@${definition.version}`, index: flat.length };
			flat.push(row);
			const group = groups.at(-1);
			if (group && group.category === definition.category) {
				group.rows.push(row);
				continue;
			}
			groups.push({ category: definition.category, rows: [row] });
			row.heading = `${definition.category}-heading`;
		}
		return { flat, groups };
	}

	$effect(() => {
		if (open && !wasOpen) {
			returnFocus = globalThis.document.activeElement instanceof HTMLElement ? globalThis.document.activeElement : null;
			activeIndex = 0;
			void tick().then(() => searchInput?.focus());
		}
		wasOpen = open;
	});

	// A new result set starts from the top: the previous highlight pointed into
	// a list that no longer exists. Compared against a plain (non-reactive)
	// copy of the inputs so the effect does not re-run on the state it writes.
	let lastQueryKey = '';
	$effect(() => {
		const key = `${query}|${triggersOnly}|${connecting}|${providesKind ?? ''}`;
		if (lastQueryKey === key) return;
		lastQueryKey = key;
		activeIndex = 0;
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
		switch (event.key) {
			case 'Escape':
				close();
				return;
			case 'ArrowDown':
				event.preventDefault();
				activeIndex = Math.min(activeIndex + 1, rows.flat.length - 1);
				return;
			case 'ArrowUp':
				event.preventDefault();
				activeIndex = Math.max(activeIndex - 1, 0);
				return;
			case 'Home':
				event.preventDefault();
				activeIndex = 0;
				return;
			case 'End':
				event.preventDefault();
				activeIndex = Math.max(rows.flat.length - 1, 0);
				return;
			case 'Enter': {
				// "Type a name, press Enter" is how a node creator is used; it
				// previously did nothing at all.
				const row = rows.flat[activeIndex];
				if (!row) return;
				event.preventDefault();
				choose(row.definition);
				return;
			}
			case 'Tab':
				trapFocus(event, dialogElement);
		}
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
				<input
					id="node-picker-search"
					bind:this={searchInput}
					bind:value={query}
					class="h-6 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
					placeholder={triggersOnly ? 'Search triggers' : providesKind ? `Search ${providesKind} nodes` : 'Search nodes'}
					role="combobox"
					aria-expanded="true"
					aria-controls="node-picker-list"
					aria-autocomplete="list"
					aria-activedescendant={active?.id}
				/>
				<h2 id="node-picker-title" class="sr-only">{triggersOnly ? 'Choose a trigger' : connecting ? 'Add a connected step' : 'Add a step'}</h2>
				<button type="button" class="grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1" aria-label="Close node picker" onclick={close}>
					<X aria-hidden="true" class="size-3.5" />
				</button>
			</div>

			<div id="node-picker-list" role="listbox" aria-label="Registered node types" class="min-h-0 flex-1 overflow-y-auto p-1">
				{#if rows.flat.length === 0}
					<p class="px-3 py-10 text-center text-xs text-muted-foreground">
						{#if query.trim()}No registered node matches “{query}”.{:else}No node here can follow that port.{/if}
					</p>
				{:else}
					{#each rows.groups as group (group.category)}
						<div role="group" aria-labelledby={`node-category-${group.category}`}>
							<h3 id={`node-category-${group.category}`} class="px-2.5 pb-1 pt-2.5 text-[0.625rem] font-semibold uppercase tracking-wider text-muted-foreground">{group.category}</h3>
							{#each group.rows as row (row.id)}
								<button
									type="button"
									id={row.id}
									role="option"
									tabindex="-1"
									aria-selected={row.index === activeIndex}
									class="flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-accent aria-selected:bg-accent focus-visible:bg-accent focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring"
									onclick={() => choose(row.definition)}
									onmousemove={() => (activeIndex = row.index)}
								>
									<NodeIcon definition={row.definition} size="sm" />
									<span class="min-w-0 flex-1">
										<span class="block truncate text-[0.8125rem] font-medium leading-tight">{row.definition.displayName}</span>
										{#if row.definition.description}
											<span class="mt-0.5 block truncate text-[0.6875rem] leading-tight text-muted-foreground">{row.definition.description}</span>
										{/if}
									</span>
								</button>
							{/each}
						</div>
					{/each}
				{/if}
			</div>

			<p class="shrink-0 border-t border-border px-2.5 py-1.5 text-[0.625rem] text-muted-foreground">
				{rows.flat.length} of {catalog.length} nodes · one row per node type, always the latest version · ↑↓ then Enter
			</p>
		</div>
	</div>
{/if}
