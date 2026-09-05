<script lang="ts">
	import { page } from '$app/state';
</script>

<svelte:head>
	<title>Embedded workflow · KilasFlow</title>
</svelte:head>

<main data-embed-shell class="min-h-dvh bg-background p-4 sm:p-6">
	<p class="text-sm text-muted-foreground">Embed session for {page.params.id}</p>
</main>
