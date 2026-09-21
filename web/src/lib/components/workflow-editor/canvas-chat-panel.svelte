<script lang="ts">
	import { tick } from 'svelte';

	import MessageCircle from '@lucide/svelte/icons/message-circle';
	import Send from '@lucide/svelte/icons/send';
	import X from '@lucide/svelte/icons/x';

	import type { ExecutionResource } from '$lib/api/generated/models';
	import * as m from '$lib/paraglide/messages.js';
	import { chatReplyFromExecution, newChatSessionId, type ChatSendPayload } from '$lib/workflow-editor/chat';

	type ChatBubble = { id: string; role: 'user' | 'assistant' | 'error'; text: string };

	let {
		triggerNodeId,
		disabled = false,
		onClose,
		onSend
	}: {
		triggerNodeId: string;
		disabled?: boolean;
		onClose: () => void;
		onSend: (payload: ChatSendPayload) => Promise<ExecutionResource>;
	} = $props();

	let sessionId = $state(newChatSessionId());
	let messages = $state<ChatBubble[]>([]);
	let draft = $state('');
	let sending = $state(false);
	let list = $state<HTMLDivElement>();

	async function scrollToEnd() {
		await tick();
		if (list) list.scrollTop = list.scrollHeight;
	}

	function resetSession() {
		sessionId = newChatSessionId();
		messages = [];
		draft = '';
	}

	async function send() {
		const chatInput = draft.trim();
		if (!chatInput || sending || disabled) return;
		draft = '';
		const userId = crypto.randomUUID();
		messages = [...messages, { id: userId, role: 'user', text: chatInput }];
		await scrollToEnd();
		sending = true;
		try {
			const execution = await onSend({ action: 'sendMessage', sessionId, chatInput });
			const reply = chatReplyFromExecution(execution, triggerNodeId);
			if (reply.kind === 'reply') {
				messages = [...messages, { id: crypto.randomUUID(), role: 'assistant', text: reply.text }];
			} else if (reply.kind === 'error') {
				messages = [...messages, { id: crypto.randomUUID(), role: 'error', text: reply.text }];
			} else {
				messages = [
					...messages,
					{
						id: crypto.randomUUID(),
						role: 'error',
						text: m.editor_chat_reply_missing({ node: reply.nodeId || m.editor_chat_last_node() })
					}
				];
			}
		} catch (error) {
			const text = error instanceof Error && error.message ? error.message : m.editor_chat_send_failed({ error: String(error) });
			messages = [...messages, { id: crypto.randomUUID(), role: 'error', text }];
		} finally {
			sending = false;
			await scrollToEnd();
		}
	}

	function onKeydown(event: KeyboardEvent) {
		if (event.key !== 'Enter' || event.shiftKey) return;
		event.preventDefault();
		void send();
	}
</script>

<section
	data-testid="workflow-chat-panel"
	class="pointer-events-auto flex h-[min(24rem,70vh)] w-[min(22rem,calc(100vw-2rem))] flex-col overflow-hidden rounded-xl border border-border bg-card shadow-lg"
	aria-label={m.editor_chat()}
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
	<p class="shrink-0 border-b border-border px-2.5 py-1.5 text-[0.6875rem] leading-4 text-muted-foreground">{m.editor_chat_memory_notice()}</p>
	<div bind:this={list} class="min-h-0 flex-1 space-y-2 overflow-y-auto px-2.5 py-2" data-testid="workflow-chat-messages">
		{#if messages.length === 0}
			<p class="py-6 text-center text-xs text-muted-foreground">{m.editor_chat_empty()}</p>
		{/if}
		{#each messages as message (message.id)}
			<div
				class="max-w-[90%] rounded-lg px-2.5 py-1.5 text-xs leading-5 {message.role === 'user'
					? 'ml-auto bg-primary text-primary-foreground'
					: message.role === 'error'
						? 'bg-destructive/10 text-destructive'
						: 'bg-muted text-foreground'}"
			>
				{message.text}
			</div>
		{/each}
		{#if sending}
			<p class="text-xs text-muted-foreground">{m.editor_running()}</p>
		{/if}
	</div>
	<form
		class="flex shrink-0 gap-1 border-t border-border p-2"
		onsubmit={(event) => {
			event.preventDefault();
			void send();
		}}
	>
		<label class="sr-only" for="workflow-chat-input">{m.editor_chat_placeholder()}</label>
		<textarea
			id="workflow-chat-input"
			data-testid="workflow-chat-input"
			bind:value={draft}
			rows="2"
			class="min-h-10 min-w-0 flex-1 resize-none rounded-md border border-border bg-background px-2 py-1.5 text-xs leading-5 outline-none focus-visible:border-ring"
			placeholder={m.editor_chat_placeholder()}
			disabled={sending || disabled}
			onkeydown={onKeydown}
		></textarea>
		<button
			type="submit"
			data-testid="workflow-chat-send"
			class="grid size-10 shrink-0 place-items-center self-end rounded-md bg-primary text-primary-foreground transition-opacity hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2 disabled:cursor-not-allowed disabled:opacity-40"
			disabled={sending || disabled || draft.trim() === ''}
			aria-label={m.editor_chat_send()}
		>
			<Send aria-hidden="true" class="size-3.5" />
		</button>
	</form>
</section>
