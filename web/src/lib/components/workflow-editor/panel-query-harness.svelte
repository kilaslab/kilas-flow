<script lang="ts">
	import { QueryClientProvider, type QueryClient } from '@tanstack/svelte-query';
	import type { ComponentProps } from 'svelte';

	import PropertiesPanel from './properties-panel.svelte';

	/**
	 * Mounts the properties panel under a query client, for tests.
	 *
	 * The panel reads server state through generated queries, which throw
	 * outside a provider, and the context key the provider sets is not exported,
	 * so a test cannot supply it to `render` directly.
	 */
	let { client, panel }: { client: QueryClient; panel: ComponentProps<typeof PropertiesPanel> } = $props();
</script>

<QueryClientProvider {client}>
	<PropertiesPanel {...panel} />
</QueryClientProvider>
