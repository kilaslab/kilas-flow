<script lang="ts">
	import { onMount } from 'svelte';

	import { clampMenuPosition, nextMenuIndex, type NodeMenuAction, type NodeMenuItem, type Point, type Size } from '$lib/workflow-editor/node-menu';
	import { formatKeys } from '$lib/workflow-editor/shortcuts';

	/**
	 * The menu a right click (or Shift+F10 / the context-menu key) opens on a node.
	 *
	 * It owns what a menu owns: where it sits, which item has focus, and when it
	 * goes away. What the items do belongs to the editor, which already has each
	 * one behind a shortcut, so this component is told the items and reports the
	 * one chosen.
	 *
	 * Keyboard: the first item takes focus on open, the arrows wrap, Home and End
	 * jump, Enter and Space activate the focused item (they are buttons), Escape
	 * and Tab close it and give focus back to the node it came from. A press
	 * anywhere outside closes it without taking focus, because the user is
	 * already pointing somewhere else.
	 */
	let {
		at,
		area,
		items,
		label,
		mac,
		onSelect,
		onClose
	}: {
		/** Where the menu was asked for, in the canvas region's own coordinates. */
		at: Point;
		/** The canvas region's size, which the menu must stay inside. */
		area: Size;
		items: NodeMenuItem[];
		/** The accessible name: which node, or how many, the menu acts on. */
		label: string;
		mac: boolean;
		onSelect: (action: NodeMenuAction) => void;
		/** `restoreFocus` is false when the user pressed somewhere else. */
		onClose: (options: { restoreFocus: boolean }) => void;
	} = $props();

	let menu = $state<HTMLDivElement>();
	let width = $state(0);
	let height = $state(0);
	const position = $derived(clampMenuPosition(at, { width, height }, area));

	function entries(): HTMLButtonElement[] {
		return menu ? [...menu.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')] : [];
	}

	onMount(() => {
		entries()[0]?.focus();
	});

	function onMenuKeydown(event: KeyboardEvent) {
		if (event.key === 'Tab') {
			// A menu is not part of the tab order: Tab leaves it, as in a native one.
			event.preventDefault();
			onClose({ restoreFocus: true });
			return;
		}
		const list = entries();
		const current = list.indexOf(globalThis.document.activeElement as HTMLButtonElement);
		const next = nextMenuIndex(list.length, current, event.key);
		if (next === current) return;
		event.preventDefault();
		list[next]?.focus();
	}

	/** On the window so Escape still closes the menu if focus has strayed out of it. */
	function onWindowKeydown(event: KeyboardEvent) {
		if (event.key !== 'Escape') return;
		event.preventDefault();
		onClose({ restoreFocus: true });
	}

	function onOutsidePress(event: PointerEvent) {
		if (menu && event.target instanceof Node && menu.contains(event.target)) return;
		onClose({ restoreFocus: false });
	}
</script>

<!-- Capture phase: Svelte Flow stops the press on a node and on the pane from
     bubbling, which is how a menu that listened in the bubble phase stayed open
     over the very thing the user clicked. The wheel is left alone on purpose:
     it pans the canvas, the menu is placed in the canvas region rather than in
     flow coordinates so it stays under the pointer, and a trackpad's
     momentum scroll from just before the click would otherwise close it
     the moment it opened. -->
<svelte:window onkeydown={onWindowKeydown} onpointerdowncapture={onOutsidePress} onresize={() => onClose({ restoreFocus: false })} onblur={() => onClose({ restoreFocus: false })} />

<div
	bind:this={menu}
	bind:clientWidth={width}
	bind:clientHeight={height}
	role="menu"
	tabindex="-1"
	aria-label={label}
	aria-orientation="vertical"
	data-testid="node-context-menu"
	class="focus-region nodrag nopan absolute z-40 min-w-44 rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10"
	style={`left: ${position.x}px; top: ${position.y}px`}
	onkeydown={onMenuKeydown}
	oncontextmenu={(event) => event.preventDefault()}
>
	{#each items as item (item.action)}
		{#if item.separatorBefore}
			<div role="separator" class="my-1 h-px bg-border"></div>
		{/if}
		<button
			type="button"
			role="menuitem"
			tabindex="-1"
			data-menu-action={item.action}
			class="focus-inset flex w-full select-none items-center gap-4 rounded-md px-2 py-1 text-left text-xs active:bg-(--kf-pressed) {item.danger ? 'text-destructive hover:bg-destructive/10 focus:bg-destructive/10' : 'hover:bg-accent hover:text-accent-foreground focus:bg-accent focus:text-accent-foreground'}"
			onpointermove={(event) => event.currentTarget.focus({ preventScroll: true })}
			onclick={() => onSelect(item.action)}
		>
			<span class="flex-1">{item.label}</span>
			{#if item.keys}
				<kbd class="font-sans text-[0.625rem] tracking-wide text-muted-foreground">{formatKeys(item.keys, mac)}</kbd>
			{/if}
		</button>
	{/each}
</div>
