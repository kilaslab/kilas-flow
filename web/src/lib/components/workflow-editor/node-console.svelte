<script lang="ts">
	import { consoleTone, formatTimestamp, type ConsoleLine } from '$lib/workflow-editor/execution';
	import * as m from '$lib/paraglide/messages.js';

	let { lines, truncated }: { lines: ConsoleLine[]; truncated: boolean } = $props();
</script>

<!-- One rendering for both a still-streaming manual run and a persisted
     execution: the caller decides which lines to hand over (live events while
     a run is in flight, the trace's own `console` once it is fetched), this
     only ever draws whatever list it is given. `aria-live` matters only for
     the live case — for a persisted execution it renders once and stays put. -->
{#if lines.length === 0}
	<p class="text-sm leading-6 text-muted-foreground">{m.executions_console_empty()}</p>
{:else}
	<ol aria-live="polite" class="max-h-72 overflow-auto rounded-lg bg-muted p-3 font-mono text-[0.6875rem] leading-5">
		{#each lines as line, index (index)}
			<li class={`flex gap-2 ${consoleTone(line.level)}`} title={line.at ? formatTimestamp(line.at) : undefined}>
				<span class="w-10 shrink-0 opacity-70 select-none">{line.level}</span>
				<span class="min-w-0 break-all whitespace-pre-wrap">{line.text}</span>
			</li>
		{/each}
	</ol>
{/if}
{#if truncated}
	<p class="mt-1.5 text-xs text-muted-foreground">{m.executions_console_truncated()}</p>
{/if}
