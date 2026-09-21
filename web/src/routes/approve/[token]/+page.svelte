<script lang="ts">
	import { page } from '$app/state';

	import { Button } from '$lib/components/ui/button';
	import * as m from '$lib/paraglide/messages.js';

	type WaitInfo = {
		executionId: string;
		workflowId: string;
		nodeId: string;
		mode: string;
		expiresAt: string;
	};

	type Refusal = {
		code: string;
		message: string;
	};

	const token = $derived(page.params.token ?? '');

	let loading = $state(true);
	let info = $state<WaitInfo | null>(null);
	let refusal = $state<Refusal | null>(null);
	let decidedBy = $state('');
	let note = $state('');
	let submitting = $state(false);
	let done = $state(false);

	$effect(() => {
		if (token) void load(token);
	});

	async function load(activeToken: string) {
		loading = true;
		info = null;
		refusal = null;
		done = false;
		try {
			const response = await fetch(`/resume/${encodeURIComponent(activeToken)}`, {
				headers: { Accept: 'application/json' }
			});
			const body = (await response.json().catch(() => null)) as WaitInfo | Refusal | null;
			if (!response.ok || !body || !('executionId' in body)) {
				refusal = {
					code: (body as Refusal | null)?.code ?? 'wait.not_found',
					message: (body as Refusal | null)?.message ?? m.auth_approval_not_found()
				};
			} else {
				info = body;
			}
		} catch {
			refusal = { code: 'wait.unreachable', message: m.auth_server_unreachable() };
		} finally {
			loading = false;
		}
	}

	async function decide(approved: boolean) {
		if (!info || submitting) return;
		submitting = true;
		refusal = null;
		try {
			const response = await fetch(`/resume/${encodeURIComponent(token)}`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
				body: JSON.stringify({ approved, decidedBy, note })
			});
			if (!response.ok) {
				const body = (await response.json().catch(() => null)) as Refusal | null;
				refusal = {
					code: body?.code ?? 'wait.failed',
					message: body?.message ?? m.auth_decision_failed()
				};
			} else {
				done = true;
			}
		} catch {
			refusal = { code: 'wait.unreachable', message: m.auth_server_unreachable() };
		} finally {
			submitting = false;
		}
	}

	function refusalTitle(code: string): string {
		switch (code) {
			case 'wait.answered':
				return m.auth_refusal_answered();
			case 'wait.expired':
				return m.auth_refusal_expired();
			case 'wait.embed_denied':
				return m.auth_refusal_embed_denied();
			default:
				return m.auth_refusal_not_found();
		}
	}
</script>

<svelte:head>
	<title>{m.auth_approval_page_title()}</title>
</svelte:head>

<section class="mx-auto grid w-full max-w-xl flex-1 place-items-center p-6">
	<div class="w-full rounded-xl border border-border bg-card p-6">
		<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">{m.auth_approval_eyebrow()}</p>
		{#if loading}
			<p aria-live="polite" class="mt-4 text-sm text-muted-foreground">{m.auth_approval_loading()}</p>
		{:else if done && info}
			<h1 class="mt-2 text-base font-semibold tracking-tight">{m.auth_decision_recorded()}</h1>
			<p class="mt-1 text-xs leading-5 text-muted-foreground">
				{m.auth_field_execution()} <span class="font-mono">{info.executionId}</span> {m.auth_decision_resumed_tail()}
			</p>
		{:else if refusal && !info}
			<h1 class="mt-2 text-base font-semibold tracking-tight">{refusalTitle(refusal.code)}</h1>
			<p class="mt-1 text-xs leading-5 text-muted-foreground">{refusal.message}</p>
			<Button class="mt-4" variant="outline" onclick={() => void load(token)}>{m.common_try_again()}</Button>
		{:else if info}
			<h1 class="mt-2 text-base font-semibold tracking-tight">{m.auth_approval_requested()}</h1>
			<dl class="mt-4 grid grid-cols-2 gap-x-6 gap-y-2 text-[0.8125rem]">
				<div>
					<dt class="text-xs text-muted-foreground">{m.auth_field_execution()}</dt>
					<dd class="mt-0.5 font-mono text-xs">{info.executionId}</dd>
				</div>
				<div>
					<dt class="text-xs text-muted-foreground">{m.auth_field_node()}</dt>
					<dd class="mt-0.5 font-mono text-xs">{info.nodeId}</dd>
				</div>
				<div>
					<dt class="text-xs text-muted-foreground">{m.auth_field_expires()}</dt>
					<dd class="mt-0.5">{new Date(info.expiresAt).toLocaleString()}</dd>
				</div>
				<div>
					<dt class="text-xs text-muted-foreground">{m.auth_field_mode()}</dt>
					<dd class="mt-0.5">{info.mode}</dd>
				</div>
			</dl>

			{#if refusal}
				<p role="alert" class="mt-4 text-xs leading-5 text-destructive">{refusal.message}</p>
			{/if}

			<div class="mt-4 grid gap-3">
				<label class="grid gap-1 text-xs font-medium text-muted-foreground" for="approval-decided-by">
					{m.auth_field_decided_by()}
					<input
						id="approval-decided-by"
						class="h-8 rounded-md border border-input bg-background px-2 text-sm font-normal text-foreground"
						bind:value={decidedBy}
						placeholder={m.auth_decided_by_placeholder()}
						autocomplete="name"
					/>
				</label>
				<label class="grid gap-1 text-xs font-medium text-muted-foreground" for="approval-note">
					{m.auth_field_note()}
					<textarea
						id="approval-note"
						class="min-h-16 rounded-md border border-input bg-background px-2 py-1.5 text-sm font-normal text-foreground"
						bind:value={note}
						placeholder={m.auth_note_placeholder()}
					></textarea>
				</label>
			</div>

			<div class="mt-4 flex gap-2">
				<Button onclick={() => void decide(true)} disabled={submitting}>
					{submitting ? m.auth_recording() : m.auth_approve()}
				</Button>
				<Button variant="outline" onclick={() => void decide(false)} disabled={submitting}>
					{submitting ? m.auth_recording() : m.auth_reject()}
				</Button>
			</div>
		{/if}
	</div>
</section>
