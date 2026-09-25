<script lang="ts">
	import Check from '@lucide/svelte/icons/check';
	import Copy from '@lucide/svelte/icons/copy';

	import type { ImportIssue, WebhookRouteResource } from '$lib/api/generated/models';
	import { Button } from '$lib/components/ui/button';
	import * as m from '$lib/paraglide/messages.js';

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
			<span class="font-semibold text-destructive">{m.workflows_report_blocking({ count: blocking })}</span>
			<span class="text-muted-foreground">{' '}{m.workflows_report_blocking_note()}</span>
		</p>
	{:else}
		<p role="status" class="rounded-lg border border-success/25 bg-success/5 px-3 py-2 text-xs leading-5">
			<span class="font-semibold text-success">{m.workflows_report_ok()}</span>
			<span class="text-muted-foreground">{' '}{m.workflows_report_ok_note()}</span>
		</p>
	{/if}

	<!-- Only a caller that has the addresses renders this section. The editor
	     reads a stored report, which carries none, and saying "no triggers
	     needed a public address" there would be a claim it cannot make. -->
	{#if webhooks}
		<section aria-label={m.workflows_webhook_addresses()}>
			<h3 class="text-xs font-semibold">{m.workflows_webhook_addresses_count({ count: webhooks.length })}</h3>
			<p class="mt-0.5 text-xs leading-5 text-muted-foreground">
				{m.workflows_webhook_addresses_note()}
			</p>
			{#if webhooks.length === 0}
				<p class="mt-1.5 rounded-lg border border-border bg-muted/40 px-3 py-2 text-xs leading-5 text-muted-foreground">
					{m.workflows_no_webhooks()}
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
								aria-label={m.workflows_copy_webhook_url_for({ name: webhookNodeName(webhook.nodeId) })}
							>
								{#if copiedURL === webhook.url && !copyFailed}
									<Check aria-hidden="true" class="size-3" />{m.workflows_copied()}
								{:else}
									<Copy aria-hidden="true" class="size-3" />{m.workflows_copy_url()}
								{/if}
							</Button>
						</li>
					{/each}
				</ul>
				{#if copyFailed}
					<p role="status" class="mt-1.5 text-xs text-muted-foreground">{m.workflows_error_copy()}</p>
				{/if}
			{/if}
		</section>
	{/if}

	<DiagnosticsSection
		{issues}
		emptyNote={m.workflows_import_empty_note()}
	/>

	{#if onOpenWorkflow}
		<div class="flex justify-end">
			<Button type="button" size="sm" onclick={onOpenWorkflow}>{m.workflows_open_named_in_editor({ name: workflowName })}</Button>
		</div>
	{/if}
</div>
