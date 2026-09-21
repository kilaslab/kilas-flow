<script lang="ts">
	import { QueryClientProvider } from '@tanstack/svelte-query';

	import '../app.css';
	import { createKilasFlowQueryClient } from '$lib/query-client';
	import { applyDocumentLanguage, currentLocale } from '$lib/i18n/locale.svelte';

	let { children } = $props();
	const queryClient = createKilasFlowQueryClient();

	// The single writer of `<html lang>`. app.html keeps `lang="en"` as the
	// pre-hydration default of the base locale, and this effect corrects it to
	// the locale the reader actually chose. Importing the locale layer here,
	// above every route, is also what installs paraglide's getLocale override
	// before the first message is read.
	$effect(() => {
		const value = currentLocale();
		applyDocumentLanguage(value);
	});
</script>

<QueryClientProvider client={queryClient}>
	<div class="min-h-full">
		{@render children()}
	</div>
</QueryClientProvider>
