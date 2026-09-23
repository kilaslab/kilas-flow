<script lang="ts">
	import { tick } from 'svelte';

	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import CircleAlert from '@lucide/svelte/icons/circle-alert';
	import CircleCheck from '@lucide/svelte/icons/circle-check';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import Maximize2 from '@lucide/svelte/icons/maximize-2';
	import MessageCircle from '@lucide/svelte/icons/message-circle';
	import Minimize2 from '@lucide/svelte/icons/minimize-2';
	import Send from '@lucide/svelte/icons/send';
	import X from '@lucide/svelte/icons/x';

	import type { ExecutionResource } from '$lib/api/generated/models';
	import * as m from '$lib/paraglide/messages.js';
	import { chatReplyFromExecution, newChatSessionId, type ChatSendPayload } from '$lib/workflow-editor/chat';
	import { reduceChatStream, type ChatToolStep } from '$lib/workflow-editor/chat-stream';
	import type { ExecutionEvent } from '$lib/workflow-editor/event-stream.svelte';
	import { validationIssuesFromApiError, withNodeNames, type CanvasValidationIssue } from '$lib/workflow-editor/validation';

	import ChatMarkdown from './chat-markdown.svelte';

	type NamedNode = { id: string; name: string };

	type ChatBubble =
		| { id: string; role: 'user'; text: string }
		| { id: string; role: 'assistant'; text: string; tools: ChatToolStep[]; executionId?: string; seconds?: number }
		| {
				id: string;
				role: 'error';
				text: string;
				node?: string;
				detail?: string;
				executionId?: string;
				issues?: CanvasValidationIssue[];
		  };

	let {
		triggerNodeId,
		open = true,
		nodes = [],
		hasMemory = false,
		dirty = false,
		busy = false,
		onClose,
		onSend,
		onFocusIssue
	}: {
		triggerNodeId: string;
		/** The panel stays mounted while closed so the session survives; this is whether it shows. */
		open?: boolean;
		nodes?: NamedNode[];
		/** A memory sub-node is wired in, so the conversation is remembered between messages. */
		hasMemory?: boolean;
		/** The canvas has unsaved changes; sending saves them first. */
		dirty?: boolean;
		/** Another run of this workflow is in progress. */
		busy?: boolean;
		onClose: () => void;
		onSend: (payload: ChatSendPayload, onEvent: (event: ExecutionEvent) => void) => Promise<ExecutionResource>;
		onFocusIssue?: (issue: CanvasValidationIssue) => void;
	} = $props();

	let sessionId = $state(newChatSessionId());
	let messages = $state<ChatBubble[]>([]);
	let draft = $state('');
	let sending = $state(false);
	let expanded = $state(false);
	let liveEvents = $state<ExecutionEvent[]>([]);
	let list = $state<HTMLDivElement>();
	let input = $state<HTMLTextAreaElement>();

	const live = $derived(reduceChatStream(liveEvents));

	// A local reasoning model can think for a minute before its first word; a
	// ticking counter is what tells the person the run is alive, not frozen.
	let elapsed = $state(0);
	$effect(() => {
		if (!sending) return;
		elapsed = 0;
		const started = Date.now();
		const timer = setInterval(() => (elapsed = Math.floor((Date.now() - started) / 1000)), 1000);
		return () => clearInterval(timer);
	});
	const canSend = $derived(!sending && !busy && draft.trim() !== '');

	// Focus the input whenever the panel is shown, so opening it and typing is
	// one motion — as it is after every reply.
	$effect(() => {
		if (open) void tick().then(() => input?.focus());
	});

	// Keep the newest content in view while the reply streams in, but only if
	// the reader has not scrolled up to read something earlier.
	let pinnedToEnd = true;
	function onScroll() {
		if (!list) return;
		pinnedToEnd = list.scrollHeight - list.scrollTop - list.clientHeight < 32;
	}
	$effect(() => {
		void messages.length;
		void live.text;
		void live.tools.length;
		void sending;
		if (pinnedToEnd) void tick().then(() => list && (list.scrollTop = list.scrollHeight));
	});

	function resetSession() {
		sessionId = newChatSessionId();
		messages = [];
		draft = '';
		input?.focus();
	}

	async function send() {
		const chatInput = draft.trim();
		if (!chatInput || sending || busy) return;
		draft = '';
		messages = [...messages, { id: crypto.randomUUID(), role: 'user', text: chatInput }];
		pinnedToEnd = true;
		sending = true;
		liveEvents = [];
		const started = performance.now();
		try {
			const execution = await onSend({ action: 'sendMessage', sessionId, chatInput }, (event) => {
				liveEvents = [...liveEvents, event];
			});
			const seconds = Math.max(0.1, Math.round((performance.now() - started) / 100) / 10);
			const reply = chatReplyFromExecution(execution, triggerNodeId, nodes);
			const tools = reduceChatStream(liveEvents).tools;
			if (reply.kind === 'reply') {
				messages = [...messages, { id: crypto.randomUUID(), role: 'assistant', text: reply.text, tools, executionId: execution.id, seconds }];
			} else if (reply.kind === 'error') {
				messages = [
					...messages,
					{ id: crypto.randomUUID(), role: 'error', text: reply.text, node: reply.node, detail: reply.detail, executionId: execution.id }
				];
			} else {
				messages = [
					...messages,
					{
						id: crypto.randomUUID(),
						role: 'error',
						text: m.editor_chat_reply_missing({ node: nodes.find((node) => node.id === reply.nodeId)?.name || m.editor_chat_last_node() }),
						executionId: execution.id
					}
				];
			}
		} catch (error) {
			const issues = withNodeNames(validationIssuesFromApiError(error), nodes);
			if (issues.length > 0) {
				messages = [...messages, { id: crypto.randomUUID(), role: 'error', text: m.editor_chat_blocked(), issues }];
			} else {
				const text = error instanceof Error && error.message ? error.message : m.editor_chat_send_failed({ error: String(error) });
				messages = [...messages, { id: crypto.randomUUID(), role: 'error', text }];
			}
		} finally {
			sending = false;
			liveEvents = [];
			await tick();
			input?.focus();
		}
	}

	function onKeydown(event: KeyboardEvent) {
		if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return;
		event.preventDefault();
		void send();
	}

	function onPanelKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape' && !sending) {
			event.stopPropagation();
			onClose();
		}
	}

	function preview(value: unknown): string {
		if (value === undefined || value === null) return '';
		let text: string;
		if (typeof value === 'string') {
			// Tool results arrive JSON-encoded; show the value, not its quoting.
			try {
				const parsed = JSON.parse(value) as unknown;
				text = typeof parsed === 'string' ? parsed : JSON.stringify(parsed, null, 2);
			} catch {
				text = value;
			}
		} else {
			text = JSON.stringify(value, null, 2);
		}
		return text.length > 1200 ? `${text.slice(0, 1200)}…` : text;
	}

	function runningTool(tools: ChatToolStep[]): string {
		for (let index = tools.length - 1; index >= 0; index -= 1) {
			if (tools[index].status === 'running') return tools[index].name;
		}
		return '';
	}

	function toolLabel(name: string): string {
		return name.replace(/[_-]+/g, ' ');
	}
</script>

{#snippet toolSteps(tools: ChatToolStep[], liveList: boolean)}
	<ul class="space-y-1" data-testid="workflow-chat-tools">
		{#each tools as tool (tool.key)}
			<li class="rounded-md border border-border/70 bg-background/40">
				<details class="group" open={liveList && tool.status === 'running'}>
					<summary class="flex cursor-pointer list-none items-center gap-1.5 px-2 py-1 text-[0.6875rem] text-muted-foreground select-none [&::-webkit-details-marker]:hidden">
						<ChevronRight aria-hidden="true" class="size-3 shrink-0 transition-transform duration-150 group-open:rotate-90 motion-reduce:transition-none" />
						{#if tool.status === 'running'}
							<LoaderCircle aria-hidden="true" class="size-3 shrink-0 animate-spin text-primary motion-reduce:animate-none" />
						{:else if tool.status === 'failed'}
							<CircleAlert aria-hidden="true" class="size-3 shrink-0 text-destructive" />
						{:else}
							<CircleCheck aria-hidden="true" class="size-3 shrink-0 text-primary" />
						{/if}
						<span class="min-w-0 truncate font-medium text-foreground">{toolLabel(tool.name)}</span>
						{#if tool.status === 'failed'}<span class="text-destructive">· {m.editor_chat_tool_failed()}</span>{/if}
					</summary>
					<div class="space-y-1 border-t border-border/70 px-2 py-1.5">
						{#if tool.input !== undefined}
							<p class="text-[0.625rem] font-medium tracking-wide text-muted-foreground uppercase">{m.editor_chat_tool_input()}</p>
							<pre class="max-h-32 overflow-auto rounded bg-background/70 px-1.5 py-1 font-mono text-[0.625rem] leading-4 whitespace-pre-wrap">{preview(tool.input)}</pre>
						{/if}
						{#if tool.status === 'failed'}
							<pre class="max-h-32 overflow-auto rounded bg-destructive/10 px-1.5 py-1 font-mono text-[0.625rem] leading-4 whitespace-pre-wrap text-destructive">{tool.error}</pre>
						{:else if tool.output !== undefined}
							<p class="text-[0.625rem] font-medium tracking-wide text-muted-foreground uppercase">{m.editor_chat_tool_output()}</p>
							<pre class="max-h-32 overflow-auto rounded bg-background/70 px-1.5 py-1 font-mono text-[0.625rem] leading-4 whitespace-pre-wrap">{preview(tool.output)}</pre>
						{/if}
					</div>
				</details>
			</li>
		{/each}
	</ul>
{/snippet}

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<section
	data-testid="workflow-chat-panel"
	class="pointer-events-auto flex flex-col overflow-hidden rounded-xl border border-border bg-card shadow-xl shadow-black/25 transition-[width,height] duration-200 ease-out motion-reduce:transition-none {expanded
		? 'h-[min(40rem,calc(100vh-8rem))] w-[min(36rem,calc(100vw-2rem))]'
		: 'h-[min(30rem,calc(100vh-8rem))] w-[min(25rem,calc(100vw-2rem))]'}"
	aria-label={m.editor_chat()}
	onkeydown={onPanelKeydown}
>
	<header class="flex shrink-0 items-center gap-1 border-b border-border px-2 py-1.5">
		<MessageCircle aria-hidden="true" class="size-3.5 text-muted-foreground" />
		<h2 class="min-w-0 flex-1 truncate text-xs font-semibold">{m.editor_chat()}</h2>
		<button
			type="button"
			class="h-7 shrink-0 rounded-md px-2 text-xs font-medium transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1 disabled:opacity-40"
			disabled={sending}
			data-testid="workflow-chat-new"
			onclick={resetSession}
		>
			{m.editor_chat_new()}
		</button>
		<button
			type="button"
			class="grid size-7 shrink-0 place-items-center rounded-md transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1"
			title={expanded ? m.editor_chat_collapse() : m.editor_chat_expand()}
			aria-label={expanded ? m.editor_chat_collapse() : m.editor_chat_expand()}
			aria-pressed={expanded}
			data-testid="workflow-chat-expand"
			onclick={() => (expanded = !expanded)}
		>
			{#if expanded}<Minimize2 aria-hidden="true" class="size-3.5" />{:else}<Maximize2 aria-hidden="true" class="size-3.5" />{/if}
		</button>
		<button
			type="button"
			class="grid size-7 shrink-0 place-items-center rounded-md transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1 disabled:opacity-40"
			title={m.editor_chat_close()}
			aria-label={m.editor_chat_close()}
			data-testid="workflow-chat-close"
			disabled={sending}
			onclick={onClose}
		>
			<X aria-hidden="true" class="size-3.5" />
		</button>
	</header>

	<div
		bind:this={list}
		onscroll={onScroll}
		class="min-h-0 flex-1 space-y-2.5 overflow-y-auto px-2.5 py-2.5"
		data-testid="workflow-chat-messages"
		aria-live="polite"
		aria-busy={sending}
	>
		{#if messages.length === 0 && !sending}
			<div class="flex h-full flex-col items-center justify-center gap-1.5 px-4 text-center">
				<MessageCircle aria-hidden="true" class="size-5 text-muted-foreground/70" />
				<p class="text-xs text-muted-foreground">{m.editor_chat_empty()}</p>
				{#if hasMemory}
					<p class="text-[0.6875rem] leading-4 text-muted-foreground/80" data-testid="workflow-chat-memory-notice">{m.editor_chat_memory_notice()}</p>
				{/if}
			</div>
		{/if}

		{#each messages as message (message.id)}
			{#if message.role === 'user'}
				<div class="ml-auto w-fit max-w-[85%] rounded-lg rounded-br-sm bg-primary px-2.5 py-1.5 text-xs leading-5 whitespace-pre-wrap text-primary-foreground" data-role="user">
					{message.text}
				</div>
			{:else if message.role === 'assistant'}
				<div class="max-w-[92%] space-y-1.5" data-role="assistant">
					{#if message.tools.length > 0}
						{@render toolSteps(message.tools, false)}
					{/if}
					<div class="rounded-lg rounded-bl-sm bg-muted px-2.5 py-1.5 text-xs leading-5 text-foreground">
						<ChatMarkdown text={message.text} />
					</div>
					{#if message.executionId}
						<p class="flex items-center gap-1.5 pl-1 text-[0.625rem] text-muted-foreground">
							{#if message.seconds !== undefined}<span class="tabular-nums">{m.editor_chat_took({ seconds: String(message.seconds) })}</span><span aria-hidden="true">·</span>{/if}
							<a class="underline-offset-2 hover:text-foreground hover:underline" href={`/executions/${message.executionId}`}>{m.editor_view_execution()}</a>
						</p>
					{/if}
				</div>
			{:else}
				<div class="max-w-[92%] space-y-1 rounded-lg border border-destructive/30 bg-destructive/10 px-2.5 py-1.5 text-xs leading-5 text-destructive" data-role="error">
					<p class="flex items-start gap-1.5 font-medium">
						<CircleAlert aria-hidden="true" class="mt-0.5 size-3.5 shrink-0" />
						<span>{message.node ? m.editor_chat_node_failed({ node: message.node }) : message.text}</span>
					</p>
					{#if message.node}
						<p class="text-foreground/90">{message.text}</p>
					{/if}
					{#if message.detail}
						<p class="font-mono text-[0.625rem] leading-4 text-destructive/80">{message.detail}</p>
					{/if}
					{#if message.issues && message.issues.length > 0}
						<ul class="space-y-0.5 pt-0.5">
							{#each message.issues as issue, index (index)}
								<li>
									{#if issue.nodeID && onFocusIssue}
										<button
											type="button"
											class="text-left text-foreground/90 underline decoration-destructive/50 underline-offset-2 hover:decoration-destructive"
											onclick={() => onFocusIssue?.(issue)}
										>
											{issue.message}
										</button>
									{:else}
										<span class="text-foreground/90">{issue.message}</span>
									{/if}
								</li>
							{/each}
						</ul>
					{/if}
					{#if message.executionId}
						<a class="inline-block text-[0.625rem] underline underline-offset-2" href={`/executions/${message.executionId}`}>{m.editor_view_execution()}</a>
					{/if}
				</div>
			{/if}
		{/each}

		{#if sending}
			<div class="max-w-[92%] space-y-1.5" data-testid="workflow-chat-pending">
				{#if live.tools.length > 0}
					{@render toolSteps(live.tools, true)}
				{/if}
				{#if live.text}
					<div class="rounded-lg rounded-bl-sm bg-muted px-2.5 py-1.5 text-xs leading-5 text-foreground" data-testid="workflow-chat-streaming">
						<ChatMarkdown text={live.text} />
					</div>
				{:else}
					<div class="inline-flex items-center gap-2 rounded-lg rounded-bl-sm bg-muted px-2.5 py-2 text-[0.6875rem] text-muted-foreground">
						<span class="flex gap-0.5" aria-hidden="true">
							<span class="size-1.5 animate-bounce rounded-full bg-muted-foreground/70 motion-reduce:animate-none"></span>
							<span class="size-1.5 animate-bounce rounded-full bg-muted-foreground/70 [animation-delay:120ms] motion-reduce:animate-none"></span>
							<span class="size-1.5 animate-bounce rounded-full bg-muted-foreground/70 [animation-delay:240ms] motion-reduce:animate-none"></span>
						</span>
						<span>
							{#if dirty}{m.editor_chat_saving()}{:else if live.phase === 'tool'}{m.editor_chat_using_tool({ tool: toolLabel(runningTool(live.tools)) })}{:else}{m.editor_chat_thinking()}{/if}
						</span>
						{#if elapsed >= 2}<span class="tabular-nums text-muted-foreground/70" data-testid="workflow-chat-elapsed">{m.editor_chat_took({ seconds: String(elapsed) })}</span>{/if}
					</div>
				{/if}
			</div>
		{/if}
	</div>

	{#if dirty && !sending}
		<p class="shrink-0 border-t border-border bg-warning/10 px-2.5 py-1.5 text-[0.6875rem] leading-4 text-foreground/90" data-testid="workflow-chat-unsaved">
			{m.editor_chat_unsaved()}
		</p>
	{:else if busy && !sending}
		<p class="shrink-0 border-t border-border px-2.5 py-1.5 text-[0.6875rem] leading-4 text-muted-foreground">{m.editor_chat_busy()}</p>
	{/if}

	<form
		class="shrink-0 border-t border-border p-2"
		onsubmit={(event) => {
			event.preventDefault();
			void send();
		}}
	>
		<div class="flex items-end gap-1.5 rounded-lg border border-border bg-background px-2 py-1.5 transition-colors focus-within:border-ring">
			<label class="sr-only" for="workflow-chat-input">{m.editor_chat_placeholder()}</label>
			<textarea
				id="workflow-chat-input"
				data-testid="workflow-chat-input"
				bind:this={input}
				bind:value={draft}
				rows="1"
				class="field-sizing-content max-h-32 min-h-5 min-w-0 flex-1 resize-none bg-transparent text-xs leading-5 outline-none placeholder:text-muted-foreground"
				placeholder={m.editor_chat_placeholder()}
				onkeydown={onKeydown}
			></textarea>
			<button
				type="submit"
				data-testid="workflow-chat-send"
				class="grid size-7 shrink-0 place-items-center rounded-md bg-primary text-primary-foreground transition-[opacity,transform] hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2 active:scale-95 disabled:cursor-not-allowed disabled:opacity-40 motion-reduce:transition-none"
				disabled={!canSend}
				aria-label={m.editor_chat_send()}
			>
				{#if sending}<LoaderCircle aria-hidden="true" class="size-3.5 animate-spin motion-reduce:animate-none" />{:else}<Send aria-hidden="true" class="size-3.5" />{/if}
			</button>
		</div>
		<p class="mt-1 px-1 text-[0.625rem] text-muted-foreground/80">{m.editor_chat_hint()}</p>
	</form>
</section>
