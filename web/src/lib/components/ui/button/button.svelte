<script lang="ts" module>
	import { type VariantProps, tv } from "tailwind-variants";
	import { cn, type WithElementRef } from "$lib/utils.js";
	import type { HTMLAnchorAttributes, HTMLButtonAttributes } from "svelte/elements";

	export const buttonVariants = tv({
		base: "rounded-lg border border-transparent bg-clip-padding text-sm font-medium active:not-aria-[haspopup]:translate-y-px aria-invalid:border-destructive dark:aria-invalid:border-destructive/50 [&_svg:not([class*='size-'])]:size-4 group/button inline-flex shrink-0 items-center justify-center whitespace-nowrap transition-[color,background-color,border-color,opacity,translate] duration-(--kf-dur-fast) select-none disabled:pointer-events-none disabled:opacity-50 aria-disabled:pointer-events-none aria-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
		variants: {
			variant: {
				default: "bg-primary text-primary-foreground hover:bg-(--kf-accent-hover) active:bg-(--kf-accent-press)",
				outline: "border-border bg-transparent hover:bg-accent hover:text-foreground active:bg-(--kf-pressed) aria-expanded:bg-accent aria-expanded:text-foreground",
				secondary: "bg-secondary text-secondary-foreground hover:bg-accent active:bg-(--kf-pressed) aria-expanded:bg-accent aria-expanded:text-secondary-foreground",
				ghost: "hover:bg-accent hover:text-foreground active:bg-(--kf-pressed) aria-expanded:bg-accent aria-expanded:text-foreground",
				destructive: "bg-destructive/10 text-destructive hover:bg-destructive/20 active:bg-destructive/30 dark:bg-destructive/20 dark:hover:bg-destructive/30 dark:active:bg-destructive/40",
				link: "text-(--kf-accent-text) underline-offset-4 hover:underline",
			},
			// The density scale (FEAT-npc3ge): two control heights everywhere —
			// 24 for compact canvas and table actions, 28 as the default — and a
			// 32 that exists only under a coarse pointer. The old sm step (28 px
			// at a 12.8 px face) folded into the default, which is that height.
			size: {
				default: "h-7 gap-1 px-2",
				xs: "h-6 gap-1 rounded-[min(var(--radius-md),10px)] px-2 text-xs in-data-[slot=button-group]:rounded-lg [&_svg:not([class*='size-'])]:size-3",
				lg: "h-7 pointer-coarse:h-8 gap-1 px-2",
				icon: "size-7",
				"icon-xs": "size-6 rounded-[min(var(--radius-md),10px)] in-data-[slot=button-group]:rounded-lg [&_svg:not([class*='size-'])]:size-3",
				"icon-lg": "size-7 pointer-coarse:size-8",
			},
		},
		defaultVariants: {
			variant: "default",
			size: "default",
		},
	});

	export type ButtonVariant = VariantProps<typeof buttonVariants>["variant"];
	export type ButtonSize = VariantProps<typeof buttonVariants>["size"];

	export type ButtonProps = WithElementRef<HTMLButtonAttributes> &
		WithElementRef<HTMLAnchorAttributes> & {
			variant?: ButtonVariant;
			size?: ButtonSize;
		};
</script>

<script lang="ts">
	let {
		class: className,
		variant = "default",
		size = "default",
		ref = $bindable(null),
		href = undefined,
		type = "button",
		disabled,
		children,
		...restProps
	}: ButtonProps = $props();
</script>

{#if href}
	<a
		bind:this={ref}
		data-slot="button"
		class={cn(buttonVariants({ variant, size }), className)}
		href={disabled ? undefined : href}
		aria-disabled={disabled}
		role={disabled ? "link" : undefined}
		tabindex={disabled ? -1 : undefined}
		{...restProps}
	>
		{@render children?.()}
	</a>
{:else}
	<button
		bind:this={ref}
		data-slot="button"
		class={cn(buttonVariants({ variant, size }), className)}
		{type}
		{disabled}
		{...restProps}
	>
		{@render children?.()}
	</button>
{/if}
