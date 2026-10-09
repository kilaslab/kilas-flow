<script lang="ts">
	import { page } from '$app/state';

	import * as m from '$lib/paraglide/messages.js';
	import { brandDocument } from '$lib/embed/brand-root';
	import EmbedEditor from '$lib/embed/embed-editor.svelte';
	import { embedSession } from '$lib/embed/session.svelte';

	const workflowID = $derived(page.params.id ?? '');
	// The session module attaches the token the moment it accepts the host's
	// message, before `session` is readable, and clears it when the frame is
	// torn down. That is deliberately not an effect here: an effect runs after
	// its children's, and the editor's first queries go out from those. The
	// locale arrives the same way — the module applies it before publishing
	// the session — so the copy below is already in the host's language.
	const embed = embedSession(() => workflowID);
	const branding = $derived(embed.session?.branding ?? {});
	// Overlays portalled to <body> sit outside the shell below, so the accent
	// also goes on <html> while this route is mounted (removed on destroy).
	$effect(() => brandDocument(document.documentElement, branding.accent));
</script>

<svelte:head>
	<title>{branding.name ? m.embed_page_title_branded({ brand: branding.name }) : m.embed_page_title()}</title>
	<!-- An embedded editor must never be indexed or linked out of its host. -->
	<meta name="robots" content="noindex, nofollow" />
</svelte:head>

<!--
	The embed shell is deliberately bare: no sidebar, no workspace header, no
	navigation to any other page. It shares the canvas with the dashboard but
	none of its chrome, so a host page cannot accidentally expose the internal
	product surface.

	The host's accent is one value: --kf-accent-src on this [data-kf-brand]
	shell. app.css re-derives every accent step here (fill, hover, pressed,
	the text on the fill, links, focus ring, node and row selection), and the
	text on the fill flips between ink and white by the accent's lightness.
	The value is sanitised by sanitizeBranding before it gets here.
-->
<main
	data-embed-shell
	data-kf-brand
	class="flex h-dvh min-h-0 flex-col bg-background"
	style={branding.accent ? `--kf-accent-src: ${branding.accent};` : undefined}
>
	{#if embed.waiting}
		<div aria-live="polite" class="grid flex-1 place-items-center text-sm text-muted-foreground">
			{m.embed_waiting()}
		</div>
	{:else if embed.error || !embed.session}
		<div class="grid flex-1 place-items-center p-6">
			<div role="alert" class="max-w-md rounded-xl border border-destructive/25 bg-destructive/5 p-5 text-center">
				<h1 class="font-semibold">{m.embed_could_not_open()}</h1>
				<p class="mt-1 text-sm leading-6 text-muted-foreground">{embed.error ?? m.embed_no_session()}</p>
			</div>
		</div>
	{:else}
		<!--
			Mounted only once the session exists, so every query inside is created
			with the token already attached.
		-->
		<EmbedEditor session={embed.session} />
	{/if}
</main>
