<script lang="ts">
	import { page } from '$app/state';

	import { Button } from '$lib/components/ui/button';

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
					message: (body as Refusal | null)?.message ?? 'This approval request could not be found.'
				};
			} else {
				info = body;
			}
		} catch {
			refusal = { code: 'wait.unreachable', message: 'The server could not be reached. Try again.' };
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
					message: body?.message ?? 'The decision could not be recorded.'
				};
			} else {
				done = true;
			}
		} catch {
			refusal = { code: 'wait.unreachable', message: 'The server could not be reached. Try again.' };
		} finally {
			submitting = false;
		}
	}

	function refusalTitle(code: string): string {
		switch (code) {
			case 'wait.answered':
				return 'Already answered';
			case 'wait.expired':
				return 'Approval expired';
			case 'wait.embed_denied':
				return 'Not available here';
			default:
				return 'Approval not found';
		}
	}
</script>

<svelte:head>
	<title>Approval · KilasFlow</title>
</svelte:head>

<section class="mx-auto grid w-full max-w-xl flex-1 place-items-center p-6">
	<div class="w-full rounded-xl border border-border bg-card p-6">
		<p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">KilasFlow approval</p>
		{#if loading}
			<p aria-live="polite" class="mt-4 text-sm text-muted-foreground">Loading approval…</p>
		{:else if done && info}
			<h1 class="mt-2 text-base font-semibold tracking-tight">Decision recorded</h1>
			<p class="mt-1 text-xs leading-5 text-muted-foreground">
				Execution <span class="font-mono">{info.executionId}</span> has been resumed. You can close this page.
			</p>
		{:else if refusal && !info}
			<h1 class="mt-2 text-base font-semibold tracking-tight">{refusalTitle(refusal.code)}</h1>
			<p class="mt-1 text-xs leading-5 text-muted-foreground">{refusal.message}</p>
			<Button class="mt-4" variant="outline" onclick={() => void load(token)}>Try again</Button>
		{:else if info}
			<h1 class="mt-2 text-base font-semibold tracking-tight">Approval requested</h1>
			<dl class="mt-4 grid grid-cols-2 gap-x-6 gap-y-2 text-[0.8125rem]">
				<div>
					<dt class="text-xs text-muted-foreground">Execution</dt>
					<dd class="mt-0.5 font-mono text-xs">{info.executionId}</dd>
				</div>
				<div>
					<dt class="text-xs text-muted-foreground">Node</dt>
					<dd class="mt-0.5 font-mono text-xs">{info.nodeId}</dd>
				</div>
				<div>
					<dt class="text-xs text-muted-foreground">Expires</dt>
					<dd class="mt-0.5">{new Date(info.expiresAt).toLocaleString()}</dd>
				</div>
				<div>
					<dt class="text-xs text-muted-foreground">Mode</dt>
					<dd class="mt-0.5">{info.mode}</dd>
				</div>
			</dl>

			{#if refusal}
				<p role="alert" class="mt-4 text-xs leading-5 text-destructive">{refusal.message}</p>
			{/if}

			<div class="mt-4 grid gap-3">
				<label class="grid gap-1 text-xs font-medium text-muted-foreground" for="approval-decided-by">
					Decided by
					<input
						id="approval-decided-by"
						class="h-8 rounded-md border border-input bg-background px-2 text-sm font-normal text-foreground"
						bind:value={decidedBy}
						placeholder="Your name"
						autocomplete="name"
					/>
				</label>
				<label class="grid gap-1 text-xs font-medium text-muted-foreground" for="approval-note">
					Note
					<textarea
						id="approval-note"
						class="min-h-16 rounded-md border border-input bg-background px-2 py-1.5 text-sm font-normal text-foreground"
						bind:value={note}
						placeholder="Why this decision (optional)"
					></textarea>
				</label>
			</div>

			<div class="mt-4 flex gap-2">
				<Button onclick={() => void decide(true)} disabled={submitting}>
					{submitting ? 'Recording…' : 'Approve'}
				</Button>
				<Button variant="outline" onclick={() => void decide(false)} disabled={submitting}>
					{submitting ? 'Recording…' : 'Reject'}
				</Button>
			</div>
		{/if}
	</div>
</section>
