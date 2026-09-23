<script lang="ts">
	import { parseChatMarkdown, type ChatInline } from '$lib/workflow-editor/chat-markdown';

	// Elements only, never {@html}: whatever a model or a tool put in a reply
	// is rendered as text inside these tags, so it can never become markup.
	let { text }: { text: string } = $props();

	const blocks = $derived(parseChatMarkdown(text));
</script>

<!-- Kept on one line per branch: whitespace between inline tags would render as stray spaces. -->
{#snippet inline(nodes: ChatInline[])}{#each nodes as node, index (index)}{#if node.kind === 'text'}{node.text}{:else if node.kind === 'break'}<br />{:else if node.kind === 'strong'}<strong class="font-semibold">{@render inline(node.children)}</strong>{:else if node.kind === 'em'}<em>{@render inline(node.children)}</em>{:else if node.kind === 'code'}<code class="rounded bg-background/60 px-1 py-px font-mono text-[0.6875rem]">{node.text}</code>{:else if node.kind === 'link'}<a href={node.href} target="_blank" rel="noopener noreferrer nofollow" class="font-medium underline underline-offset-2">{@render inline(node.children)}</a>{/if}{/each}{/snippet}

<div class="space-y-1.5 break-words" data-testid="chat-markdown">
	{#each blocks as block, index (index)}
		{#if block.kind === 'paragraph'}
			<p>{@render inline(block.inline)}</p>
		{:else if block.kind === 'heading'}
			<p class="font-semibold {block.level === 1 ? 'text-[0.8125rem]' : ''}">{@render inline(block.inline)}</p>
		{:else if block.kind === 'quote'}
			<blockquote class="border-l-2 border-border pl-2 text-muted-foreground">{@render inline(block.inline)}</blockquote>
		{:else if block.kind === 'code'}
			<pre class="overflow-x-auto rounded-md bg-background/70 px-2 py-1.5 font-mono text-[0.6875rem] leading-4"><code>{block.text}</code></pre>
		{:else if block.ordered}
			<ol class="list-decimal space-y-0.5 pl-4 marker:text-muted-foreground" start={block.start}>
				{#each block.items as item, itemIndex (itemIndex)}
					<li>{@render inline(item)}</li>
				{/each}
			</ol>
		{:else}
			<ul class="list-disc space-y-0.5 pl-4 marker:text-muted-foreground">
				{#each block.items as item, itemIndex (itemIndex)}
					<li>{@render inline(item)}</li>
				{/each}
			</ul>
		{/if}
	{/each}
</div>
