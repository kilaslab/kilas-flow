<script lang="ts">
	import * as m from '$lib/paraglide/messages.js';
	import { currentLocale, localeOptions, rememberLocale, setLocale } from '$lib/i18n/locale.svelte';

	/**
	 * The language switcher.
	 *
	 * It sits in the sidebar rather than the dashboard header because that
	 * header is `lg:hidden` on the editor route — a control there would be
	 * missing from the one screen where a user reads the most copy. The mobile
	 * sheet renders the same component, so small viewports reach it too.
	 *
	 * A native `<select>` is the pattern the editor already uses for its
	 * pickers, and it needs no scripting to open: the choice applies on change,
	 * through the one module that owns the locale.
	 */
</script>

<select
	aria-label={m.common_language()}
	value={currentLocale()}
	onchange={(event) => {
		const next = event.currentTarget.value;
		if (setLocale(next)) rememberLocale(next);
	}}
	class="h-7 w-full rounded-md border border-sidebar-border bg-sidebar px-1.5 text-xs text-sidebar-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-sidebar-ring"
>
	{#each localeOptions() as option (option.value)}
		<option value={option.value}>{option.label}</option>
	{/each}
</select>
