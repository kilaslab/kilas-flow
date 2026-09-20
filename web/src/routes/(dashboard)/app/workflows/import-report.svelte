<script lang="ts">
	import Check from '@lucide/svelte/icons/check';
	import Copy from '@lucide/svelte/icons/copy';

	import type { ImportIssue, WebhookRouteResource } from '$lib/api/generated/models';
	import { Button } from '$lib/components/ui/button';

	import DiagnosticsSection from './diagnostics-section.svelte';

	/**
	 * The import report: what an n8n file became, read as a report rather
	 * than a success toast. The diagnostics decide whether the workflow runs
	 * and the webhook URLs decide whether anything reaches it, so neither
	 * belongs in something that disappears after four seconds.
	 *
	 * It is given what it renders rather than the whole import response,
	 * because two surfaces show the same report for different reasons: the
	 * dialog, which still holds the response with its webhook addresses, and
	 * the editor, which reads the report stored with the revision on screen
	 * and has no addresses to show and no workflow to open.
	 */
	let {
		issues,
		workflowName,
		webhooks = null,
		nodeNames = null,
		onOpenWorkflow = null
	}: {
		issues: ImportIssue[];
		workflowName: string;
		/** Null means the caller has no addresses to show; an empty array means there are none. */
		webhooks?: WebhookRouteResource[] | null;
		/** Node names for the addresses' labels, when the caller has a document to read them from. */
		nodeNames?: Map<string, string> | null;
		onOpenWorkflow?: (() => void) | null;
	} = $props();

	const blocking = $derived(issues.filter((issue) => issue.severity === 'blocking').length);

	let copiedURL = $state<string | null>(null);
	let copyFailed = $state(false);
	let resetTimer: ReturnType<typeof setTimeout> | undefined;

	$effect(() => () => clearTimeout(resetTimer));

	function webhookNodeName(nodeID: string): string {
		return nodeNames?.get(nodeID) ?? nodeID;
	}

	async function copyURL(url: string) {
		clearTimeout(resetTimer);
		try {
			await navigator.clipboard.writeText(url);
			copyFailed = false;
		} catch {
			// A page served over plain HTTP has no clipboard API at all. The
			// URL sits beside this button as selectable text for exactly that
			// case, so the failure is worth naming rather than leaving the
			// user to wonder whether their paste holds the old clipboard.
			copyFailed = true;
		}
		copiedURL = url;
		resetTimer = setTimeout(() => {
			copiedURL = null;
			copyFailed = false;
		}, 2500);
	}
</script>

<div class="grid gap-5">
	<!-- The single most consequential sentence first: whether this workflow
	     can be activated as imported. Everything below is the evidence. -->
	{#if blocking > 0}
		<p role="alert" class="rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-2 text-xs leading-5">
			<span class="font-semibold text-destructive">This workflow will not activate until the {blocking} blocking {blocking === 1 ? 'issue is' : 'issues are'} fixed.</span>
			<span class="text-muted-foreground"> Each one names the node to open below.</span>
		</p>
	{:else}
		<p role="status" class="rounded-lg border border-success/25 bg-success/5 px-3 py-2 text-xs leading-5">
			<span class="font-semibold text-success">This workflow will activate as imported.</span>
			<span class="text-muted-foreground"> Read the lossy and dropped entries and decide whether the differences matter.</span>
		</p>
	{/if}

	<!-- Only a caller that has the addresses renders this section. The editor
	     reads a stored report, which carries none, and saying "no triggers
	     needed a public address" there would be a claim it cannot make. -->
	{#if webhooks}
		<section aria-label="Webhook addresses">
			<h3 class="text-xs font-semibold">Webhook addresses · {webhooks.length}</h3>
			<p class="mt-0.5 text-xs leading-5 text-muted-foreground">
				Every webhook URL changes on import — prefix it with your host and point the sending
				system at the new address. The old path will not work.
			</p>
			{#if webhooks.length === 0}
				<p class="mt-1.5 rounded-lg border border-border bg-muted/40 px-3 py-2 text-xs leading-5 text-muted-foreground">
					No triggers in this workflow needed a public address.
				</p>
			{:else}
				<ul class="mt-1.5 divide-y divide-border overflow-hidden rounded-lg border border-border">
					{#each webhooks as webhook (`${webhook.nodeId}-${webhook.url}`)}
						<li class="flex flex-wrap items-center gap-x-2 gap-y-1 px-3 py-2">
							<span class="min-w-0 flex-1 basis-40">
								<span class="block truncate text-xs font-medium">{webhookNodeName(webhook.nodeId)}</span>
								<span class="block truncate font-mono text-[0.6875rem] text-muted-foreground">{webhook.method} · {webhook.path}</span>
							</span>
							<code class="min-w-0 flex-1 basis-56 truncate font-mono text-[0.6875rem] text-foreground select-all">{webhook.url}</code>
							<Button
								type="button"
								variant="outline"
								size="sm"
								class="h-7 shrink-0 px-2 text-[0.6875rem]"
								onclick={() => void copyURL(webhook.url)}
								aria-label={`Copy webhook URL for ${webhookNodeName(webhook.nodeId)}`}
							>
								{#if copiedURL === webhook.url && !copyFailed}
									<Check aria-hidden="true" class="size-3" />Copied
								{:else}
									<Copy aria-hidden="true" class="size-3" />Copy URL
								{/if}
							</Button>
						</li>
					{/each}
				</ul>
				{#if copyFailed}
					<p role="status" class="mt-1.5 text-xs text-muted-foreground">Copying failed in this browser — select the address above and copy it by hand.</p>
				{/if}
			{/if}
		</section>
	{/if}

	<DiagnosticsSection
		{issues}
		emptyNote="No issues — everything in this file carried exactly."
	/>

	{#if onOpenWorkflow}
		<div class="flex justify-end">
			<Button type="button" size="sm" onclick={onOpenWorkflow}>Open {workflowName} in the editor</Button>
		</div>
	{/if}
</div>
