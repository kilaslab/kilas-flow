<script lang="ts">
	import ChevronDown from '@lucide/svelte/icons/chevron-down';
	import ChevronUp from '@lucide/svelte/icons/chevron-up';
	import Plus from '@lucide/svelte/icons/plus';
	import X from '@lucide/svelte/icons/x';

	import type { MapperColumn, PropertyDefinition } from '$lib/api/generated/models';
	// Self-import rather than <svelte:self>, which Svelte 5 deprecates.
	import PropertyField from './property-field.svelte';
	import { renameKeyValue } from '$lib/workflow-editor/key-value';
	import {
		ASSIGNMENT_TYPES,
		defaultForType,
		newAssignmentID,
		readAssignments,
		writeAssignments,
		type Assignment,
		type AssignmentType
	} from '$lib/workflow-editor/assignments';
	import { expressionRoots, unknownExpressionRoot } from '$lib/workflow-editor/expression-grammar';
	import {
		VALUELESS_OPERATORS,
		moveCondition,
		newCondition,
		readConditions,
		removeCondition,
		updateCondition,
		type Condition,
		type ConditionOperator
	} from '$lib/workflow-editor/conditions';
	import { currentMode, readLocator, switchMode, writeLocator } from '$lib/workflow-editor/resource-locator';
	import {
		columnControl,
		knownColumnType,
		matchableColumns,
		readMapping,
		writableColumns,
		writeMapping
	} from '$lib/workflow-editor/resource-mapper';
	import {
		groupEntries,
		newGroupEntry,
		repeatedGroup,
		visibleGroupFields,
		writeGroupEntries
	} from '$lib/workflow-editor/fixed-collection';
	import { asExpression, asFixed, expressionTemplate, isExpression } from '$lib/workflow-editor/parameter';

	let {
		property,
		value,
		onChange,
		loadOptions,
		loadSchema
	}: {
		property: PropertyDefinition;
		value: unknown;
		onChange: (value: unknown) => void;
		/**
		 * Fetches a property's selectable values. Supplied by the panel, which
		 * knows the node this property belongs to; this component only knows
		 * the property.
		 */
		loadOptions?: (property: PropertyDefinition, mode?: string) => Promise<{ options: { label: string; value: string }[]; reason: string }>;
		/** Fetches a resource mapper's columns. Its own seam, because a column
		 * carries a type, a required flag and match eligibility, none of which
		 * fit in an option's {label, value}. */
		loadSchema?: (property: PropertyDefinition) => Promise<{ fields: MapperColumn[]; reason: string }>;
	} = $props();

	// Only text-shaped controls can carry an expression: a checkbox or a select
	// has no free-text surface for one, and silently accepting a marker there
	// would produce a value the control could not display.
	// Which kinds may hold an expression. A checkbox, a select and a nested
	// collection have no free-text surface to show a template in, so offering
	// the toggle there would produce a control the user cannot read back. n8n
	// reaches the same conclusion through noDataExpression.
	const expressionCapable = $derived(
		property.kind === 'string' ||
			property.kind === 'number' ||
			property.kind === 'json' ||
			property.kind === 'dateTime'
	);

	/** Every kind this panel knows how to render. */
	const RENDERED = new Set([
		'string', 'number', 'boolean', 'options', 'multiOptions',
		'collection', 'fixedCollection', 'notice', 'json', 'dateTime',
		'keyValue', 'conditions', 'assignmentCollection', 'resourceLocator', 'resourceMapper'
	]);

	const typeOptions = $derived(property.typeOptions ?? {});

	/** The one group of a repeatable fixedCollection, when the property is one. */
	const group = $derived(repeatedGroup(property));
	const entries = $derived(group ? groupEntries(group, value) : []);

	function commitEntries(next: Record<string, unknown>[]): void {
		if (!group) return;
		onChange(writeGroupEntries(group, value, next));
	}

	function addGroupEntry(): void {
		if (!group) return;
		commitEntries([...entries, newGroupEntry(group)]);
	}

	function removeGroupEntry(index: number): void {
		commitEntries(entries.filter((_, position) => position !== index));
	}

	function updateGroupEntry(index: number, key: string, next: unknown): void {
		commitEntries(entries.map((entry, position) => (position === index ? { ...entry, [key]: next } : entry)));
	}

	/**
	 * Options for a property whose valid values live on the customer's own
	 * service. A fixed list is used as-is; a loader is fetched, and until it
	 * answers the current value is shown so the select never looks empty.
	 */
	let loadState = $state<{ options: { label: string; value: string }[]; reason: string }>({
		options: [],
		reason: ''
	});
	/** A resource mapper's columns, and the mapping over them. */
	let schemaState = $state<{ fields: MapperColumn[]; reason: string }>({ fields: [], reason: '' });
	const mapping = $derived(readMapping(property, value));
	// The live columns when they have loaded, the stored copy until then, so
	// the form is not empty on the first render of a saved node.
	const schemaColumns = $derived(schemaState.fields.length > 0 ? schemaState.fields : (mapping.schema ?? []));

	$effect(() => {
		if (property.kind !== 'resourceMapper' || !loadSchema) return;
		let cancelled = false;
		void loadSchema(property).then((result) => {
			if (!cancelled) schemaState = result;
		});
		return () => {
			cancelled = true;
		};
	});

	function setColumn(id: string, next: unknown) {
		onChange(writeMapping({ ...mapping, value: { ...mapping.value, [id]: next } }, schemaColumns));
	}

	function toggleMatch(id: string, on: boolean): string[] {
		// More than one matching column is allowed: a composite key is a key.
		const without = mapping.matchingColumns.filter((column) => column !== id);
		return on ? [...without, id] : without;
	}

	/** The locator this property currently holds, and the mode it names. */
	const locator = $derived(readLocator(property, value));
	const locatorMode = $derived(currentMode(property, locator));

	// A locator's loader lives on the mode it is currently in, so the list is
	// fetched — and discarded — when the mode changes, not only when the
	// property does.
	const activeLoader = $derived(locatorMode?.loadOptions ?? property.loadOptions);
	const selectableOptions = $derived(activeLoader ? loadState.options : (property.options ?? []));

	$effect(() => {
		const loader = activeLoader;
		const mode = property.kind === 'resourceLocator' ? locator.mode : undefined;
		if (!loader || !loadOptions) return;
		let cancelled = false;
		void loadOptions(property, mode).then((result) => {
			if (!cancelled) loadState = result;
		});
		return () => {
			cancelled = true;
		};
	});
	const selected = $derived(Array.isArray(value) ? (value as unknown[]).map(String) : []);

	function toggleOption(option: string, on: boolean): void {
		const next = new Set(selected);
		if (on) next.add(option);
		else next.delete(option);
		onChange([...next]);
	}
	const expressionMode = $derived(isExpression(value));
	const template = $derived(expressionTemplate(value));
	const stringValue = $derived(typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value));
	const objectValue = $derived(isObject(value) ? value : {});
	// Rows, never a string. The whole list is handed back on every edit, so
	// nothing in this component can turn an assignment collection into text.
	const assignments = $derived(readAssignments(value));

	function updateAssignment(index: number, patch: Partial<Assignment>) {
		const next = assignments.map((row, position) => (position === index ? { ...row, ...patch } : row));
		onChange(writeAssignments(next));
	}

	function retypeAssignment(index: number, type: AssignmentType) {
		const current = assignments[index];
		updateAssignment(index, { type, value: defaultForType(type, current?.value) });
	}

	function removeAssignment(index: number) {
		onChange(writeAssignments(assignments.filter((_, position) => position !== index)));
	}

	function addAssignment() {
		onChange(writeAssignments([...assignments, { id: newAssignmentID(assignments), name: '', type: 'string', value: '' }]));
	}

	/** What a row's value editor shows. Structured values are edited as JSON. */
	function assignmentText(row: Assignment): string {
		if (row.type === 'array' || row.type === 'object') {
			return typeof row.value === 'string' ? row.value : JSON.stringify(row.value ?? (row.type === 'array' ? [] : {}));
		}
		return displayValue(row.value);
	}

	function renameKey(previousKey: string, nextKey: string) {
		onChange(renameKeyValue(objectValue, previousKey, nextKey));
	}

	function updateKeyValue(key: string, nextValue: string) {
		onChange({ ...objectValue, [key]: parseValue(nextValue) });
	}

	function removeKeyValue(key: string) {
		const next = { ...objectValue };
		delete next[key];
		onChange(next);
	}

	function addKeyValue() {
		onChange({ ...objectValue, '': '' });
	}

	const conditionRows = $derived(readConditions(value));

	function writeConditions(rows: Condition[]) {
		onChange(rows);
	}

	/** The label of a chosen option, cached so the picker reads back. */
	function selectedLabel(chosen: string): string {
		return selectableOptions.find((option) => option.value === chosen)?.label ?? '';
	}

	function toggleExpression() {
		onChange(expressionMode ? asFixed(value) : asExpression(value));
	}

	/**
	 * Best-effort preview of what an expression references. The authoritative
	 * evaluation happens on the server, so this reports shape problems only and
	 * never claims a value.
	 */
	function expressionHint(text: string): string | null {
		const opens = (text.match(/\{\{/g) ?? []).length;
		const closes = (text.match(/\}\}/g) ?? []).length;
		if (opens === 0) return 'No {{ }} expression yet — this will be sent as literal text.';
		if (opens !== closes) return 'Unbalanced {{ }} — the server will reject this expression.';
		// The allowlist comes from the server. Keeping a copy here meant every
		// root added in Go was rejected in the editor until this file was
		// remembered.
		const unknown = unknownExpressionRoot(text);
		if (unknown) {
			const roots = expressionRoots();
			return roots.length > 0
				? `${unknown} is not an available root. Use ${roots.join(', ')}.`
				: `${unknown} is not an available root.`;
		}
		return null;
	}

	function isObject(candidate: unknown): candidate is Record<string, unknown> {
		return candidate !== null && typeof candidate === 'object' && !Array.isArray(candidate);
	}

	function parseValue(input: string): unknown {
		try {
			return JSON.parse(input);
		} catch {
			return input;
		}
	}

	function displayValue(input: unknown): string {
		if (input === undefined || input === null) return '';
		return typeof input === 'string' ? input : JSON.stringify(input);
	}
</script>

<div class="grid gap-1">
	<div class="flex items-baseline justify-between gap-2">
		<label class="text-xs font-medium leading-tight" for={`property-${property.key}`}>{property.label}{#if property.required}<span aria-hidden="true" class="text-destructive"> *</span>{/if}</label>
		{#if expressionCapable}
			<button type="button" role="switch" aria-checked={expressionMode} aria-label={`${property.label}: expression mode`} class="shrink-0 rounded border border-border px-1 py-0.5 font-mono text-[0.625rem] leading-4 text-muted-foreground transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring aria-checked:border-primary/40 aria-checked:bg-primary/10 aria-checked:text-primary" onclick={toggleExpression}>
				{expressionMode ? 'expr' : 'fixed'}
			</button>
		{/if}
	</div>
	{#if property.description}<p class="text-[0.6875rem] leading-4 text-muted-foreground">{property.description}</p>{/if}

	{#if expressionMode}
		<input id={`property-${property.key}`} value={template} spellcheck="false" class="h-7 rounded-md border border-primary/40 bg-primary/5 px-2 font-mono text-xs" aria-describedby={`property-${property.key}-hint`} oninput={(event) => onChange({ mode: 'expression', value: event.currentTarget.value })} />
		<p id={`property-${property.key}-hint`} class="text-[0.6875rem] leading-4 {expressionHint(template) ? 'text-destructive' : 'text-muted-foreground'}">
			{expressionHint(template) ?? 'Resolved per item on the server, for example {{ $json.id }}.'}
		</p>
	{:else if property.kind === 'boolean'}
		<label class="flex h-7 items-center gap-2 rounded-md border border-input px-2 text-xs">
			<input id={`property-${property.key}`} type="checkbox" class="size-3.5" checked={Boolean(value)} onchange={(event) => onChange(event.currentTarget.checked)} />
			<span>{Boolean(value) ? 'Enabled' : 'Disabled'}</span>
		</label>
	{:else if property.kind === 'number'}
		<input id={`property-${property.key}`} type="number" value={stringValue} class="h-7 rounded-md border border-input bg-background px-2 text-xs" oninput={(event) => onChange(event.currentTarget.value === '' ? undefined : Number(event.currentTarget.value))} />
	{:else if property.kind === 'notice'}
		<!-- A notice holds no value: it is never stored, never required, and
		     never sends an onChange. One that round-tripped into the document
		     would fail validation on the next save. -->
		<p class="rounded-md border border-border bg-muted/40 px-2 py-1.5 text-[0.6875rem] leading-4 text-muted-foreground">
			{property.description || property.label}
		</p>
	{:else if property.kind === 'dateTime'}
		<input id={`property-${property.key}`} type="datetime-local" value={stringValue} class="h-7 rounded-md border border-input bg-background px-2 text-xs" oninput={(event) => onChange(event.currentTarget.value)} />
	{:else if property.kind === 'json'}
		<textarea id={`property-${property.key}`} value={stringValue} spellcheck="false" rows={typeOptions.rows || 4} class="rounded-md border border-input bg-background px-2 py-1.5 font-mono text-[0.6875rem]" oninput={(event) => onChange(event.currentTarget.value)}></textarea>
	{:else if property.kind === 'multiOptions'}
		<div class="grid gap-1 rounded-md border border-input p-1.5">
			{#each property.options ?? [] as option (option.value)}
				<label class="flex items-center gap-2 text-[0.6875rem]">
					<input type="checkbox" class="size-3.5" checked={selected.includes(option.value)} onchange={(event) => toggleOption(option.value, event.currentTarget.checked)} />
					<span>{option.label}</span>
				</label>
			{/each}
		</div>
	{:else if property.kind === 'fixedCollection' && group}
		<!-- A repeatable named group — a Schedule Trigger's rules, an HTTP
		     node's query pairs. Rendered as real controls rather than a JSON
		     textarea, because the whole point of the shape is that the user
		     does not have to know what it serialises to. -->
		<div class="grid gap-1.5 rounded-md border border-input p-1.5">
			{#each entries as entry, index (index)}
				<div class="grid gap-1 rounded border border-border/70 p-1.5">
					<div class="flex items-center justify-between">
						<span class="text-[0.6875rem] font-medium text-muted-foreground">{group.label} {index + 1}</span>
						<button type="button" aria-label={`Remove ${group.label} ${index + 1}`} class="grid size-6 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => removeGroupEntry(index)}>
							<X aria-hidden="true" class="size-3.5" />
						</button>
					</div>
					{#each visibleGroupFields(group, entry) as field (field.key)}
						<!-- Self-recursion: a nested field is just another
						     property, so it gets the same control the top level
						     would give it, including its own nesting. -->
						<PropertyField
							property={field}
							value={entry[field.key] ?? field.default}
							onChange={(next: unknown) => updateGroupEntry(index, field.key, next)}
							{loadOptions}
						/>
					{/each}
				</div>
			{/each}
			<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={addGroupEntry}>
				<Plus aria-hidden="true" class="size-3" />{typeOptions.multipleValueButtonText || `Add ${group.label}`}
			</button>
		</div>
	{:else if property.kind === 'collection' || property.kind === 'fixedCollection'}
		<!-- Everything else nested: shown as what is configured without
		     pretending to edit a shape there is no control for yet. -->
		<div class="grid gap-1 rounded-md border border-dashed border-input p-1.5 text-[0.6875rem] text-muted-foreground">
			<span>{(property.fields ?? []).length || (property.groups ?? []).length} nested field(s)</span>
			<textarea aria-label={`${property.label} value`} value={stringValue} spellcheck="false" rows={3} class="rounded border border-input bg-background px-1.5 py-1 font-mono text-[0.6875rem]" oninput={(event) => onChange(event.currentTarget.value)}></textarea>
		</div>
	{:else if property.kind === 'options'}
		<select id={`property-${property.key}`} value={stringValue} class="h-7 rounded-md border border-input bg-background px-1.5 text-xs" onchange={(event) => onChange(event.currentTarget.value)}>
			{#each selectableOptions as option (option.value)}
				<option value={option.value}>{option.label}</option>
			{/each}
		</select>
		{#if loadState.reason}
			<!-- An empty list the user can act on, rather than an empty dropdown
			     that reads as "this service has nothing". -->
			<p class="text-[0.6875rem] leading-4 text-muted-foreground">{loadState.reason}</p>
		{/if}
	{:else if property.kind === 'keyValue'}
		<div class="grid gap-1.5 rounded-md border border-input p-1.5">
			{#each Object.entries(objectValue) as [key, item] (key)}
				<div class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-1">
					<input aria-label={`${property.label} field name`} value={key} class="h-7 min-w-0 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" onchange={(event) => renameKey(key, event.currentTarget.value)} />
					<input aria-label={`${property.label} field value`} value={displayValue(item)} class="h-7 min-w-0 rounded border border-input bg-background px-1.5 text-[0.6875rem]" oninput={(event) => updateKeyValue(key, event.currentTarget.value)} />
					<button type="button" aria-label={`Remove ${key || 'assignment'}`} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => removeKeyValue(key)}>
						<X aria-hidden="true" class="size-3.5" />
					</button>
				</div>
			{/each}
			<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={addKeyValue}>
				<Plus aria-hidden="true" class="size-3" />Add field
			</button>
		</div>
	{:else if property.kind === 'assignmentCollection'}
		<div class="grid gap-1.5 rounded-md border border-input p-1.5">
			{#each assignments as row, index (row.id)}
				<div class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1.2fr)_auto] gap-1">
					<input aria-label={`${property.label} field name`} value={row.name} placeholder="fieldName" class="h-7 min-w-0 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => updateAssignment(index, { name: event.currentTarget.value })} />
					<select aria-label={`${property.label} field type`} value={row.type} class="h-7 rounded border border-input bg-background px-1 text-[0.6875rem]" onchange={(event) => retypeAssignment(index, event.currentTarget.value as AssignmentType)}>
						{#each ASSIGNMENT_TYPES as type (type)}
							<option value={type}>{type}</option>
						{/each}
					</select>
					{#if row.type === 'boolean'}
						<select aria-label={`${property.label} field value`} value={row.value === true ? 'true' : 'false'} class="h-7 rounded border border-input bg-background px-1 text-[0.6875rem]" onchange={(event) => updateAssignment(index, { value: event.currentTarget.value === 'true' })}>
							<option value="true">true</option>
							<option value="false">false</option>
						</select>
					{:else}
						<input aria-label={`${property.label} field value`} value={assignmentText(row)} class="h-7 min-w-0 rounded border border-input bg-background px-1.5 text-[0.6875rem]" oninput={(event) => updateAssignment(index, { value: row.type === 'number' ? Number(event.currentTarget.value) : event.currentTarget.value })} />
					{/if}
					<button type="button" aria-label={`Remove ${row.name || 'field'}`} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => removeAssignment(index)}>
						<X aria-hidden="true" class="size-3.5" />
					</button>
				</div>
			{/each}
			<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={addAssignment}>
				<Plus aria-hidden="true" class="size-3" />Add field
			</button>
		</div>
	{:else if property.kind === 'resourceLocator'}
		<!-- A narrow mode select beside the mode's own control, which is the
		     shape n8n uses: the mode is a property of the value, so it sits with
		     it rather than above it as a separate field. -->
		<div class="grid gap-1">
			<div class="flex items-start gap-1">
				<select aria-label={`${property.label} mode`} value={locator.mode} class="h-7 w-28 shrink-0 rounded-md border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => onChange(writeLocator(switchMode(property, locator, event.currentTarget.value)))}>
					{#each property.modes ?? [] as mode (mode.name)}
						<option value={mode.name}>{mode.label}</option>
					{/each}
					{#if !locatorMode}
						<option value={locator.mode}>{locator.mode || 'unknown'}</option>
					{/if}
				</select>
				{#if !locatorMode}
					<!-- A mode this build does not know. Read-only and named,
					     rather than an empty control that reads as "this field
					     has no value". -->
					<p class="min-w-0 flex-1 rounded-md border border-dashed border-destructive/40 px-2 py-1.5 text-[0.6875rem] leading-4 text-destructive">
						This editor does not know the mode “{locator.mode}”. Its value is {displayValue(locator.value) || 'empty'} and cannot be edited here.
					</p>
				{:else if locatorMode.kind === 'options'}
					<select id={`property-${property.key}`} value={displayValue(locator.value)} class="h-7 min-w-0 flex-1 rounded-md border border-input bg-background px-1.5 text-xs" onchange={(event) => onChange(writeLocator({ ...locator, value: event.currentTarget.value, cachedResultName: selectedLabel(event.currentTarget.value) }))}>
						<option value="">{locatorMode.placeholder || 'Choose…'}</option>
						{#each selectableOptions as option (option.value)}
							<option value={option.value}>{option.label}</option>
						{/each}
					</select>
				{:else}
					<input id={`property-${property.key}`} value={displayValue(locator.value)} placeholder={locatorMode.placeholder ?? ''} class="h-7 min-w-0 flex-1 rounded-md border border-input bg-background px-2 text-xs" oninput={(event) => onChange(writeLocator({ ...locator, value: event.currentTarget.value }))} />
				{/if}
			</div>
			{#if locatorMode?.hint}
				<p class="text-[0.6875rem] leading-4 text-muted-foreground">{locatorMode.hint}</p>
			{/if}
			{#if loadState.reason && locatorMode?.kind === 'options'}
				<p class="text-[0.6875rem] leading-4 text-muted-foreground">{loadState.reason}</p>
			{/if}
		</div>
	{:else if property.kind === 'resourceMapper'}
		<div class="grid gap-1.5 rounded-md border border-input p-1.5">
			{#if property.mapper?.supportsAutoMap}
				<label class="grid gap-1 text-[0.6875rem]">
					<span class="text-muted-foreground">Mapping Column Mode</span>
					<select aria-label={`${property.label} mapping mode`} value={mapping.mappingMode} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => onChange(writeMapping({ ...mapping, mappingMode: event.currentTarget.value }, schemaColumns))}>
						<option value="defineBelow">Map Each Column Manually</option>
						<option value="autoMapInputData">Map Automatically</option>
					</select>
				</label>
			{/if}
			{#if matchableColumns(schemaColumns).length > 0}
				<div class="grid gap-1 text-[0.6875rem]">
					<span class="text-muted-foreground">Columns to match on</span>
					<div class="grid gap-1 rounded border border-input p-1.5">
						{#each matchableColumns(schemaColumns) as column (column.id)}
							<label class="flex items-center gap-2">
								<input type="checkbox" class="size-3.5" checked={mapping.matchingColumns.includes(column.id)} onchange={(event) => onChange(writeMapping({ ...mapping, matchingColumns: toggleMatch(column.id, event.currentTarget.checked) }, schemaColumns))} />
								<span>{column.displayName}{column.required ? ' *' : ''}</span>
							</label>
						{/each}
					</div>
				</div>
			{/if}
			{#if mapping.mappingMode === 'autoMapInputData'}
				<!-- A read-only summary of what will be sent, rather than a form
				     nobody fills in. The write is the intersection of the item's
				     keys and these columns, so an absent column is omitted and
				     never sent as an explicit null. -->
				<p class="rounded border border-dashed border-input px-2 py-1.5 text-[0.6875rem] leading-4 text-muted-foreground">
					{#if schemaColumns.length === 0}
						{schemaState.reason || 'The columns have not loaded yet.'}
					{:else}
						Incoming fields are matched to these columns by name: {writableColumns(schemaColumns, mapping).map((column) => column.displayName).join(', ') || 'none'}. A field with no matching column is not sent.
					{/if}
				</p>
			{:else if schemaColumns.length === 0}
				<p class="rounded border border-dashed border-input px-2 py-1.5 text-[0.6875rem] leading-4 text-muted-foreground">
					{schemaState.reason || 'The columns have not loaded yet.'}
				</p>
			{:else}
				{#each writableColumns(schemaColumns, mapping) as column (column.id)}
					<label class="grid gap-1 text-[0.6875rem]">
						<span class="text-muted-foreground">{column.displayName}{column.required ? ' *' : ''}</span>
						{#if columnControl(column.type) === 'boolean'}
							<select aria-label={column.displayName} value={String(mapping.value[column.id] ?? 'false')} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => setColumn(column.id, event.currentTarget.value === 'true')}>
								<option value="true">true</option>
								<option value="false">false</option>
							</select>
						{:else if column.options && column.options.length > 0}
							<select aria-label={column.displayName} value={displayValue(mapping.value[column.id])} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => setColumn(column.id, event.currentTarget.value)}>
								<option value="">Choose…</option>
								{#each column.options as option (option.value)}
									<option value={option.value}>{option.label}</option>
								{/each}
							</select>
						{:else}
							<input aria-label={column.displayName} type={columnControl(column.type) === 'number' ? 'number' : 'text'} value={displayValue(mapping.value[column.id])} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" oninput={(event) => setColumn(column.id, columnControl(column.type) === 'number' ? Number(event.currentTarget.value) : event.currentTarget.value)} />
						{/if}
						{#if !knownColumnType(column.type)}
							<!-- Named rather than dropped: a column missing from
							     the form reads as "this table has no such
							     column", which is a worse lie. -->
							<span class="text-destructive">This editor does not know the column type “{column.type}”, so it is edited as text.</span>
						{/if}
					</label>
				{/each}
			{/if}
		</div>
	{:else if property.kind === 'conditions'}
		<div class="grid gap-1.5 rounded-md border border-input p-1.5">
			{#each conditionRows as row, index (index)}
				<div class="grid gap-1 rounded border border-border/70 p-1.5">
					<div class="flex items-center gap-1">
						<input aria-label={`${property.label} field ${index + 1}`} value={row.field} placeholder="customer.tier" class="h-7 min-w-0 flex-1 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => writeConditions(updateCondition(conditionRows, index, { field: event.currentTarget.value }))} />
						<!-- Order is meaningful: the rules read top to bottom. -->
						<button type="button" aria-label={`Move condition ${index + 1} up`} disabled={index === 0} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted disabled:opacity-40" onclick={() => writeConditions(moveCondition(conditionRows, index, -1))}>
							<ChevronUp aria-hidden="true" class="size-3.5" />
						</button>
						<button type="button" aria-label={`Move condition ${index + 1} down`} disabled={index === conditionRows.length - 1} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted disabled:opacity-40" onclick={() => writeConditions(moveCondition(conditionRows, index, 1))}>
							<ChevronDown aria-hidden="true" class="size-3.5" />
						</button>
						<button type="button" aria-label={`Remove condition ${index + 1}`} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => writeConditions(removeCondition(conditionRows, index))}>
							<X aria-hidden="true" class="size-3.5" />
						</button>
					</div>
					<select aria-label={`${property.label} operator ${index + 1}`} value={row.operator} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => writeConditions(updateCondition(conditionRows, index, { operator: event.currentTarget.value as ConditionOperator }))}>
						<option value="equals">equals</option>
						<option value="notEquals">does not equal</option>
						<option value="exists">exists</option>
						<option value="notExists">does not exist</option>
					</select>
					{#if !VALUELESS_OPERATORS.includes(row.operator)}
						<input aria-label={`${property.label} value ${index + 1}`} value={displayValue(row.value)} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" oninput={(event) => writeConditions(updateCondition(conditionRows, index, { value: parseValue(event.currentTarget.value) }))} />
					{/if}
				</div>
			{/each}
			<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={() => writeConditions([...conditionRows, newCondition()])}>
				<Plus aria-hidden="true" class="size-3" />Add condition
			</button>
		</div>
	{:else if RENDERED.has(property.kind) && (typeOptions.rows ?? 0) > 1}
		<!-- Multi-line is a different element, not an attribute: rows has no
		     meaning on an input. -->
		<textarea id={`property-${property.key}`} value={stringValue} rows={typeOptions.rows} class="rounded-md border border-input bg-background px-2 py-1.5 text-xs" oninput={(event) => onChange(event.currentTarget.value)}></textarea>
	{:else if RENDERED.has(property.kind)}
		<input id={`property-${property.key}`} value={stringValue} type={typeOptions.password ? 'password' : 'text'} class="h-7 rounded-md border border-input bg-background px-2 text-xs" oninput={(event) => onChange(event.currentTarget.value)} />
	{:else}
		<!-- A kind this build does not know. Degrading to a named read-only JSON
		     view is what stops a newer pack's field rendering as nothing at all,
		     which reads as "this node has no such setting" rather than "this
		     editor is older than this node". -->
		<div class="grid gap-1 rounded-md border border-dashed border-destructive/40 p-1.5">
			<p class="text-[0.6875rem] leading-4 text-destructive">
				This editor does not know the field type “{property.kind}”. Its value is shown as JSON and cannot be edited here.
			</p>
			<pre class="overflow-x-auto rounded bg-muted/40 px-1.5 py-1 font-mono text-[0.6875rem]">{stringValue}</pre>
		</div>
	{/if}
</div>
