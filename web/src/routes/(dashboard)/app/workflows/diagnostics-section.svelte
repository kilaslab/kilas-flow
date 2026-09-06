<script lang="ts">
	import * as Table from '$lib/components/ui/table';

	/**
	 * One import or export diagnostic, normalised so both envelopes render
	 * through this one section. `ImportIssue` and `ExportIssue` both fit:
	 * everything but severity and reason is optional on each.
	 */
	export type DiagnosticRow = {
		severity: 'blocking' | 'lossy' | 'dropped';
		nodeName?: string;
		nodeId?: string;
		type?: string;
		typeVersion?: number;
		field?: string;
		reason: string;
	};

	let {
		issues,
		emptyNote
	}: {
		issues: DiagnosticRow[];
		emptyNote: string;
	} = $props();

	// Blocking first: it decides whether the workflow runs, the other two
	// decide how closely. The words match the migration guide, which defines
	// this vocabulary once for both the screen and the page.
	const order = ['blocking', 'lossy', 'dropped'] as const;
	const meta: Record<
		(typeof order)[number],
		{ heading: string; meaning: string; action: string; badge: string }
	> = {
		blocking: {
			heading: 'Blocking',
			meaning: 'The workflow cannot run as imported.',
			action: 'Fix it. The workflow will not activate until you do.',
			badge: 'border-destructive/30 bg-destructive/10 text-destructive'
		},
		lossy: {
			heading: 'Lossy',
			meaning: 'The element was carried, but differently.',
			action: 'Read it and decide. The workflow will activate.',
			badge: 'border-warning/40 bg-warning/10 text-warning'
		},
		dropped: {
			heading: 'Dropped',
			meaning: 'The element was not carried at all.',
			action: 'Decide whether you need it.',
			badge: 'border-border bg-muted text-muted-foreground'
		}
	};

	const groups = $derived(
		order
			.map((severity) => ({ severity, rows: issues.filter((issue) => issue.severity === severity) }))
			.filter((group) => group.rows.length > 0)
	);

	function nodeLabel(row: DiagnosticRow): string {
		return row.nodeName || row.nodeId || 'Workflow';
	}

	function typeLabel(row: DiagnosticRow): string {
		const parts = [row.type, row.typeVersion !== undefined ? `v${String(row.typeVersion)}` : '']
			.filter(Boolean)
			.join(' ');
		return parts || '—';
	}
</script>

{#if issues.length === 0}
	<p class="rounded-lg border border-border bg-muted/40 px-3 py-2 text-xs leading-5 text-muted-foreground">{emptyNote}</p>
{:else}
	<div class="grid gap-4">
		{#each groups as group (group.severity)}
			{@const info = meta[group.severity]}
			<section aria-label={`${info.heading} diagnostics`}>
				<div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
					<h3 class="text-xs font-semibold">
						{info.heading}
						<span class="font-normal text-muted-foreground">· {group.rows.length}</span>
					</h3>
					<p class="w-full text-xs leading-5 text-muted-foreground">
						{info.meaning}
						{info.action}
					</p>
				</div>
				<div class="mt-1.5 overflow-hidden rounded-lg border border-border">
					<Table.Root>
						<Table.Caption class="sr-only">{info.heading} import diagnostics</Table.Caption>
						<Table.Header class="[&_th]:h-7 [&_th]:px-3 [&_th]:text-xs [&_th]:uppercase [&_th]:tracking-wide [&_th]:text-muted-foreground">
							<Table.Row>
								<Table.Head scope="col">Severity</Table.Head>
								<Table.Head scope="col">Node</Table.Head>
								<Table.Head scope="col">Source type</Table.Head>
								<Table.Head scope="col">Field</Table.Head>
								<Table.Head scope="col">What happened</Table.Head>
							</Table.Row>
						</Table.Header>
						<Table.Body>
							{#each group.rows as row, index (`${group.severity}-${row.nodeId ?? row.nodeName ?? 'workflow'}-${row.field ?? ''}-${index}`)}
								<Table.Row>
									<Table.Cell class="whitespace-nowrap px-3 py-2">
										<span class={`inline-flex items-center rounded-full border px-2 py-0.5 text-[0.6875rem] font-medium ${info.badge}`}>{row.severity}</span>
									</Table.Cell>
									<Table.Cell class="max-w-40 truncate px-3 py-2 font-medium">{nodeLabel(row)}</Table.Cell>
									<Table.Cell class="max-w-48 truncate px-3 py-2 font-mono text-[0.6875rem] text-muted-foreground">{typeLabel(row)}</Table.Cell>
									<Table.Cell class="max-w-40 truncate px-3 py-2 font-mono text-[0.6875rem] text-muted-foreground">{row.field ?? '—'}</Table.Cell>
									<Table.Cell class="min-w-56 whitespace-normal px-3 py-2 text-xs leading-5 text-muted-foreground">{row.reason}</Table.Cell>
								</Table.Row>
							{/each}
						</Table.Body>
					</Table.Root>
				</div>
			</section>
		{/each}
	</div>
{/if}
