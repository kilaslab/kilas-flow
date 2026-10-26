<script lang="ts">
	import { Select as SelectPrimitive } from "bits-ui";
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import { cn, type WithoutChild } from "$lib/utils.js";

	// One trigger height (28px, FEAT-npc3ge): the old sm/default split was two
	// of the seventeen control heights the editor screen carried.
	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithoutChild<SelectPrimitive.TriggerProps> = $props();
</script>

<SelectPrimitive.Trigger
	bind:ref
	data-slot="select-trigger"
	class={cn(
		"gap-2 rounded-lg border border-input bg-transparent h-7 pr-2 pl-2 text-sm transition-colors select-none hover:border-(--kf-border-hover) aria-invalid:border-destructive data-placeholder:text-muted-foreground *:data-[slot=select-value]:flex *:data-[slot=select-value]:gap-2 dark:bg-input/30 dark:aria-invalid:border-destructive/50 [&_svg:not([class*='size-'])]:size-4 flex w-fit items-center justify-between whitespace-nowrap disabled:cursor-not-allowed disabled:opacity-50 *:data-[slot=select-value]:line-clamp-1 *:data-[slot=select-value]:flex *:data-[slot=select-value]:items-center [&_svg]:pointer-events-none [&_svg]:shrink-0",
		className
	)}
	{...restProps}
>
	{@render children?.()}
	<ChevronDownIcon class="size-4 text-muted-foreground pointer-events-none" />
</SelectPrimitive.Trigger>
