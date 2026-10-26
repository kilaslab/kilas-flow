<!--
	The design-system state matrix (VR-03, BUG-ywbvfa criterion 3): every
	primitive in every interaction state, one section per `?section=`.

	Columns are states. A cell's `data-force` names the pseudo-classes the
	visual suite forces through the DevTools protocol (e2e/visual/force-state.ts)
	so hover, pressed and focus-visible are rendered without a pointer or a
	keyboard; disabled cells use the real `disabled` / `aria-disabled`.

	Dev and harness builds only: the route that loads this file is dropped from
	release bundles (routes/design/states/+page.ts). Labels are English on
	purpose; nothing here is product copy.
-->
<script lang="ts">
	import { Background, BackgroundVariant, SvelteFlow, type Edge } from '@xyflow/svelte';
	import Bell from '@lucide/svelte/icons/bell';
	import Plus from '@lucide/svelte/icons/plus';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	import type { Definition, Port } from '$lib/api/generated/models';
	import DashboardNav from '$lib/components/dashboard/dashboard-nav.svelte';
	import CanvasNode from '$lib/components/workflow-editor/canvas-node.svelte';
	import { Button, type ButtonVariant } from '$lib/components/ui/button';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import { Input } from '$lib/components/ui/input';
	import { ListRow } from '$lib/components/ui/list-row';
	import * as Select from '$lib/components/ui/select';
	import { Switch } from '$lib/components/ui/switch';
	import * as Tabs from '$lib/components/ui/tabs';
	import type { EditorFlowNode } from '$lib/workflow-editor/document';

	let { section = 'button' }: { section?: string } = $props();

	type State = { key: string; label: string; force?: string; disabled?: boolean };
	const STATES: State[] = [
		{ key: 'default', label: 'Default' },
		{ key: 'hover', label: 'Hover', force: 'hover' },
		{ key: 'pressed', label: 'Pressed', force: 'hover active' },
		{ key: 'focus', label: 'Focus visible', force: 'focus focus-visible' },
		{ key: 'disabled', label: 'Disabled', disabled: true }
	];

	const BUTTON_VARIANTS: ButtonVariant[] = ['default', 'outline', 'secondary', 'ghost', 'destructive', 'link'];

	// ---- canvas fixtures -------------------------------------------------
	const MAIN: Port = { name: 'main', kind: 'main' };
	const stepDefinition: Definition = {
		type: 'kilasflow.set',
		version: 1,
		displayName: 'Edit Fields',
		category: 'Core',
		group: ['transform'],
		source: 'builtin',
		icon: { light: 'builtin:pencil' },
		iconColor: '#0ea5e9',
		inputs: [MAIN],
		outputs: [MAIN],
		parameters: [],
		sharedSettings: []
	};
	const triggerDefinition: Definition = {
		...stepDefinition,
		type: 'kilasflow.manual',
		displayName: 'Manual Trigger',
		category: 'Triggers',
		icon: { light: 'builtin:mouse-pointer-click' },
		iconColor: '#6366f1',
		inputs: []
	};

	function flowNode(id: string, name: string, x: number, y: number, extra: Partial<EditorFlowNode> = {}, disabled = false, definition = stepDefinition, validationMessage?: string): EditorFlowNode {
		return {
			id,
			type: 'workflow',
			position: { x, y },
			data: {
				definition,
				workflowNode: { id, name, type: definition.type, typeVersion: 1, position: { x, y }, parameters: {}, disabled },
				validationMessage
			},
			...extra
		};
	}

	const nodeTypes = { workflow: CanvasNode };
	let matrixNodes = $state.raw<EditorFlowNode[]>([
		flowNode('n-default', 'Default', 0, 0),
		flowNode('n-hover', 'Hover', 160, 0, { domAttributes: { 'data-force': 'hover' } as never }),
		flowNode('n-pressed', 'Pressed', 320, 0, { domAttributes: { 'data-force': 'hover active' } as never }),
		flowNode('n-focus', 'Focus visible', 480, 0, { domAttributes: { 'data-force': 'focus focus-visible' } as never }),
		flowNode('n-selected', 'Selected', 640, 0, { selected: true }),
		flowNode('n-disabled', 'Disabled', 800, 0, {}, true),
		flowNode('n-invalid', 'Invalid', 960, 0, {}, false, stepDefinition, 'A required field is empty')
	]);
	let matrixEdges = $state.raw<Edge[]>([]);

	let subtreeNodes = $state.raw<EditorFlowNode[]>([
		flowNode('s-trigger', 'Manual Trigger', 0, 0, {}, false, triggerDefinition),
		flowNode('s-step', 'Edit Fields', 220, 0, { selected: true })
	]);
	let subtreeEdges = $state.raw<Edge[]>([{ id: 's-edge', source: 's-trigger', target: 's-step', sourceHandle: 'main', targetHandle: 'main' }]);

	let switchOn = $state(true);
	let switchOff = $state(false);

	// The sidebar nav renders its own links, so its cells are marked here:
	// item 1 is the current page, then default, hover, pressed, focus-visible.
	const NAV_FORCE = [undefined, undefined, 'hover', 'hover active', 'focus focus-visible'];
	let navPanel = $state<HTMLElement | null>(null);
	$effect(() => {
		if (!navPanel) return;
		navPanel.querySelectorAll('a').forEach((link, index) => {
			const force = NAV_FORCE[index];
			if (force) link.setAttribute('data-force', force);
		});
	});
</script>

{#snippet header(extra: string[] = [])}
	<div class="contents text-[0.6875rem] font-medium text-muted-foreground">
		<span></span>
		{#each STATES as state (state.key)}<span>{state.label}</span>{/each}
		{#each extra as label (label)}<span>{label}</span>{/each}
	</div>
{/snippet}

{#snippet rowLabel(label: string)}
	<span class="self-center text-xs text-muted-foreground">{label}</span>
{/snippet}

<main class="min-h-dvh bg-background p-6 text-foreground" data-testid="state-matrix" data-section={section}>
	<h1 class="mb-4 text-sm font-semibold">State matrix: {section}</h1>

	{#if section === 'button'}
		<div class="grid w-fit grid-cols-[6rem_repeat(5,8rem)] items-center gap-3" data-testid="matrix">
			{@render header()}
			{#each BUTTON_VARIANTS as variant (variant)}
				{@render rowLabel(variant ?? 'default')}
				{#each STATES as state (state.key)}
					<div><Button {variant} data-force={state.force} disabled={state.disabled}>Button</Button></div>
				{/each}
			{/each}
		</div>
	{:else if section === 'icon-button'}
		<div class="grid w-fit grid-cols-[6rem_repeat(5,6rem)] items-center gap-3" data-testid="matrix">
			{@render header()}
			{#each ['ghost', 'outline'] as const as variant (variant)}
				{@render rowLabel(variant)}
				{#each STATES as state (state.key)}
					<div>
						<Button {variant} size="icon" aria-label="Notifications" data-force={state.force} disabled={state.disabled}><Bell /></Button>
					</div>
				{/each}
			{/each}
		</div>
	{:else if section === 'input'}
		<div class="grid w-fit grid-cols-[6rem_repeat(5,10rem)] items-center gap-3" data-testid="matrix">
			{@render header()}
			{#each [{ label: 'empty', value: '' }, { label: 'filled', value: 'Order intake' }, { label: 'invalid', value: 'x', invalid: true }] as row (row.label)}
				{@render rowLabel(row.label)}
				{#each STATES as state (state.key)}
					<div>
						<Input
							placeholder="Search"
							value={row.value}
							aria-label={`${row.label} ${state.label}`}
							aria-invalid={row.invalid ? 'true' : undefined}
							data-force={state.force}
							disabled={state.disabled}
						/>
					</div>
				{/each}
			{/each}
		</div>
	{:else if section === 'select'}
		<div class="grid w-fit grid-cols-[6rem_repeat(5,10rem)] items-center gap-3" data-testid="matrix">
			{@render header()}
			{#each [{ label: 'placeholder', value: undefined }, { label: 'chosen', value: 'all' }] as row (row.label)}
				{@render rowLabel(row.label)}
				{#each STATES as state (state.key)}
					<div>
						<Select.Root type="single" value={row.value} disabled={state.disabled}>
							<Select.Trigger class="w-36" aria-label={`${row.label} ${state.label}`} data-force={state.force}>
								{row.value ? 'All workflows' : 'Choose…'}
							</Select.Trigger>
							<Select.Content>
								<Select.Item value="all">All workflows</Select.Item>
							</Select.Content>
						</Select.Root>
					</div>
				{/each}
			{/each}
		</div>
	{:else if section === 'switch'}
		<div class="grid w-fit grid-cols-[6rem_repeat(5,6rem)] items-center gap-3" data-testid="matrix">
			{@render header()}
			{@render rowLabel('off')}
			{#each STATES as state (state.key)}
				<div><Switch bind:checked={switchOff} aria-label={`off ${state.label}`} data-force={state.force} disabled={state.disabled} /></div>
			{/each}
			{@render rowLabel('on')}
			{#each STATES as state (state.key)}
				<div><Switch bind:checked={switchOn} aria-label={`on ${state.label}`} data-force={state.force} disabled={state.disabled} /></div>
			{/each}
		</div>
	{:else if section === 'tab'}
		<div class="grid w-fit gap-4" data-testid="matrix">
			{#each ['default', 'line'] as const as variant (variant)}
				<div class="flex items-center gap-3">
					{@render rowLabel(variant)}
					<Tabs.Root value="selected">
						<Tabs.List {variant}>
							<Tabs.Trigger value="selected">Selected</Tabs.Trigger>
							{#each STATES as state (state.key)}
								<Tabs.Trigger value={state.key} data-force={state.force} disabled={state.disabled}>{state.label}</Tabs.Trigger>
							{/each}
						</Tabs.List>
					</Tabs.Root>
				</div>
			{/each}
		</div>
	{:else if section === 'menu-item'}
		<div class="h-72" data-testid="matrix">
			<DropdownMenu.Root open>
				<DropdownMenu.Trigger>
					{#snippet child({ props })}
						<Button {...props} variant="outline">Actions</Button>
					{/snippet}
				</DropdownMenu.Trigger>
				<DropdownMenu.Content align="start" class="w-48" onOpenAutoFocus={(event) => event.preventDefault()} onCloseAutoFocus={(event) => event.preventDefault()} onInteractOutside={(event) => event.preventDefault()}>
					<DropdownMenu.Item><Plus /> Default</DropdownMenu.Item>
					<DropdownMenu.Item data-force="focus hover">Highlighted</DropdownMenu.Item>
					<DropdownMenu.Item data-force="focus hover active">Pressed</DropdownMenu.Item>
					<DropdownMenu.Item data-force="focus focus-visible">Focus visible</DropdownMenu.Item>
					<DropdownMenu.Item disabled>Disabled</DropdownMenu.Item>
					<DropdownMenu.Separator />
					<DropdownMenu.Item variant="destructive" data-force="focus hover"><Trash2 /> Delete</DropdownMenu.Item>
				</DropdownMenu.Content>
			</DropdownMenu.Root>
		</div>
	{:else if section === 'list-row'}
		<div class="grid w-fit grid-cols-[6rem_repeat(5,12rem)] items-center gap-3" data-testid="matrix">
			{@render header()}
			{#each [{ label: 'row' }, { label: 'selected', selected: true }] as row (row.label)}
				{@render rowLabel(row.label)}
				{#each STATES as state (state.key)}
					<div class="overflow-hidden rounded-(--kf-radius-md) border border-border bg-card">
						<ListRow
							tabindex={state.disabled ? -1 : 0}
							aria-selected={row.selected ? 'true' : undefined}
							aria-disabled={state.disabled ? 'true' : undefined}
							role="option"
							data-force={state.force}
						>
							Order intake
						</ListRow>
					</div>
				{/each}
			{/each}
		</div>
	{:else if section === 'nav'}
		<!-- The dashboard sidebar on its panel surface: the current page (pressed
		     step) must stay distinct from a hovered row (hover step) in both
		     themes. -->
		<div class="flex items-start gap-6" data-testid="matrix">
			<div bind:this={navPanel} class="w-52 rounded-(--kf-radius-md) border border-sidebar-border bg-sidebar p-2 text-sidebar-foreground" data-testid="nav-panel">
				<DashboardNav path="/app/workflows" />
			</div>
			<div class="grid gap-1 pt-2 text-[0.6875rem] text-muted-foreground">
				<span class="flex h-8 items-center">current page</span>
				<span class="flex h-8 items-center">default</span>
				<span class="flex h-8 items-center">hover</span>
				<span class="flex h-8 items-center">pressed</span>
				<span class="flex h-8 items-center">focus visible</span>
			</div>
		</div>
	{:else if section === 'canvas-node'}
		<div class="h-56 w-[72rem] overflow-hidden rounded-(--kf-radius-md) border border-border" data-testid="matrix">
			<SvelteFlow
				bind:nodes={matrixNodes}
				bind:edges={matrixEdges}
				{nodeTypes}
				fitView
				fitViewOptions={{ padding: 0.2, maxZoom: 1 }}
				nodesDraggable={false}
				nodesConnectable={false}
				panOnDrag={false}
				zoomOnScroll={false}
				proOptions={{ hideAttribution: true }}
			>
				<Background variant={BackgroundVariant.Dots} gap={16} size={1} patternColor="var(--border)" />
			</SvelteFlow>
		</div>
	{:else if section === 'theme-subtree'}
		<!-- A light subtree inside the dark page (FEAT-p75n7j criterion 5): the
		     aliases re-resolve at this boundary, so surfaces, controls and the
		     canvas render light while <body> stays dark. -->
		<div class="grid w-fit gap-4" data-testid="matrix">
			<div data-theme="light" data-testid="light-subtree" class="grid gap-3 rounded-(--kf-radius-md) border border-border bg-background p-4 text-foreground">
				<div class="flex items-center gap-2">
					<Button>Execute</Button>
					<Button variant="outline">Cancel</Button>
					<Input class="w-40" placeholder="Search" aria-label="Search" />
					<Switch bind:checked={switchOn} aria-label="Active" />
					<a href="#subtree" class="text-sm text-(--kf-accent-text) underline underline-offset-4">Link</a>
				</div>
				<div class="w-80 overflow-hidden rounded-(--kf-radius-md) border border-border bg-card">
					<ListRow aria-selected="true" role="option" tabindex={0}>Selected row</ListRow>
					<ListRow role="option" tabindex={0}>Row</ListRow>
				</div>
				<div class="h-44 w-[32rem] overflow-hidden rounded-(--kf-radius-md) border border-border" data-testid="light-canvas">
					<SvelteFlow
						bind:nodes={subtreeNodes}
						bind:edges={subtreeEdges}
						{nodeTypes}
						fitView
						fitViewOptions={{ padding: 0.3, maxZoom: 1 }}
						nodesDraggable={false}
						nodesConnectable={false}
						panOnDrag={false}
						zoomOnScroll={false}
						proOptions={{ hideAttribution: true }}
					/>
				</div>
			</div>
			<div data-kf-brand style="--kf-accent-src: #0ea5e9" data-testid="brand-subtree" class="flex items-center gap-2 rounded-(--kf-radius-md) border border-border p-4">
				<Button>Execute</Button>
				<Button data-force="hover">Hover</Button>
				<Button variant="outline" data-force="focus focus-visible">Focus</Button>
				<a href="#brand" class="text-sm text-(--kf-accent-text) underline underline-offset-4">Link</a>
			</div>
		</div>
	{/if}
</main>
