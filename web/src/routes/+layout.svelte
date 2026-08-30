<script lang="ts">
	import { QueryClientProvider } from '@tanstack/svelte-query';

	import '../app.css';
	import { createKilasFlowQueryClient } from '$lib/query-client';

	let { children } = $props();
	const queryClient = createKilasFlowQueryClient();
</script>

<QueryClientProvider client={queryClient}>
	<div class="min-h-full">
		{@render children()}
	</div>
</QueryClientProvider>
