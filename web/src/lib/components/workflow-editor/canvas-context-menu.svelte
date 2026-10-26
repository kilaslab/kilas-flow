<script lang="ts">
	import { tick } from 'svelte';

	import type { MenuItem } from '$lib/workflow-editor/canvas-menu';

	/**
	 * The canvas context menu (FEAT-ez6xtm): one menu for a node and for the
	 * pane, at the pointer, reusing the actions the editor already owns.
	 *
	 * Keyboard-first on purpose: it opens from a right-click, but it is a real
	 * menu — focus lands on the first item, arrows walk the list, Escape
	 * closes, and a click anywhere else dismisses it. It never owns Tab: Tab
	 * closes it and leaves the menu, the way a native menu does.
	 */
	let {
		x,
		y,
		ariaLabel,
		items,
		onAction,
		onClose
	}: {
		x: number;
		y: number;
		ariaLabel: string;
		items: MenuItem[];
		onAction: (action: MenuItem['action']) => void;
		onClose: () => void;
	} = $props();

	let menu = $state<HTMLDivElement>();
	let returnFocus = $state<HTMLElement | null>(null);

	// Clamp into the viewport so a right-click at the edge still shows the
	// whole list rather than spilling off-screen.
	const width = 192;
	const placed = $derived({
		left: Math.min(x, globalThis.innerWidth - width - 8),
		top: Math.min(y, globalThis.innerHeight - items.length * 30 - 24)
	});

	$effect(() => {
		returnFocus = globalThis.document.activeElement instanceof HTMLElement ? globalThis.document.activeElement : null;
		void tick().then(() => menu?.querySelector<HTMLButtonElement>('button')?.focus());
		// A click anywhere outside — including another contextmenu — dismisses.
		const dismiss = (event: Event) => {
			if (event.target instanceof Node && menu?.contains(event.target)) return;
			onClose();
		};
		globalThis.addEventListener('pointerdown', dismiss, true);
		return () => {
			globalThis.removeEventListener('pointerdown', dismiss, true);
			returnFocus?.focus?.();
		};
	});

	function moveFocus(from: HTMLElement, step: number) {
		const buttons = [...(menu?.querySelectorAll<HTMLButtonElement>('button') ?? [])];
		if (buttons.length === 0) return;
		const current = buttons.findIndex((button) => button === from);
		const next = (current + step + buttons.length) % buttons.length;
		buttons[next].focus();
	}
</script>

<div
	bind:this={menu}
	role="menu"
	tabindex="-1"
	aria-label={ariaLabel}
	data-testid="canvas-context-menu"
	class="fixed z-50 min-w-36 max-w-48 overflow-hidden rounded-lg border border-border bg-popover p-1 shadow-lg focus-region"
	style={`left: ${Math.max(8, placed.left)}px; top: ${Math.max(8, placed.top)}px; width: ${width}px`}
	onkeydown={(event) => {
		const active = globalThis.document.activeElement instanceof HTMLElement ? globalThis.document.activeElement : null;
		if (event.key === 'Escape') {
			event.preventDefault();
			onClose();
		} else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
			event.preventDefault();
			if (active) moveFocus(active, event.key === 'ArrowDown' ? 1 : -1);
		} else if (event.key === 'Tab') {
			onClose();
		} else if (event.key === 'Home' || event.key === 'End') {
			event.preventDefault();
			const buttons = [...(menu?.querySelectorAll<HTMLButtonElement>('button') ?? [])];
			(event.key === 'Home' ? buttons[0] : buttons.at(-1))?.focus();
		}
	}}
>
	{#each items as item (item.action)}
		<button
			type="button"
			role="menuitem"
			class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors hover:bg-muted focus-visible:bg-muted focus-visible:outline-none {item.danger
				? 'mt-1 border-t border-border pt-2.5 text-destructive hover:bg-destructive/10'
				: ''}"
			onclick={() => {
				onAction(item.action);
				onClose();
			}}
		>
			{item.label}
		</button>
	{/each}
</div>
