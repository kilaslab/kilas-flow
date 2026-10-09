<script lang="ts" module>
	import { tv } from "tailwind-variants";
	import { cn, type WithElementRef } from "$lib/utils.js";
	import type { HTMLAnchorAttributes, HTMLAttributes } from "svelte/elements";

	// One list row for every dashboard list (BUG-ywbvfa): 40px, the shared
	// hover and pressed surfaces, the inset focus ring dense lists use, and
	// selection drawn as the accent's row tint with a 2px selection bar.
	export const listRowVariants = tv({
		base: "focus-inset relative flex min-h-(--kf-row-h) w-full items-center gap-2 px-3 text-left text-sm text-foreground transition-colors duration-(--kf-dur-fast) hover:bg-(--kf-hover) active:bg-(--kf-pressed) aria-selected:bg-(--kf-selection-row) aria-selected:shadow-[inset_2px_0_0_var(--kf-selection)] data-selected:bg-(--kf-selection-row) data-selected:shadow-[inset_2px_0_0_var(--kf-selection)] aria-disabled:pointer-events-none aria-disabled:text-(--kf-fg-disabled)",
	});

	export type ListRowProps = WithElementRef<HTMLAttributes<HTMLDivElement>> &
		WithElementRef<HTMLAnchorAttributes> & { href?: string };
</script>

<script lang="ts">
	let { ref = $bindable(null), href = undefined, class: className, children, ...restProps }: ListRowProps = $props();
</script>

{#if href}
	<a bind:this={ref} data-slot="list-row" class={cn(listRowVariants(), className)} {href} {...restProps}>
		{@render children?.()}
	</a>
{:else}
	<div bind:this={ref} data-slot="list-row" class={cn(listRowVariants(), className)} {...restProps}>
		{@render children?.()}
	</div>
{/if}
