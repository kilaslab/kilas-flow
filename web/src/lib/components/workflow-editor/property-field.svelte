<script lang="ts" module>
	import type { MapperColumn as MapperColumnShape } from '$lib/api/generated/models';

	// Shared across every field of every node on the page, and bounded inside
	// `loadSoon`: the same loader with the same dependencies and the same
	// credential has one answer, so flicking between two nodes of a type must
	// not re-ask the server for it.
	const optionsCache = new Map<string, { options: { label: string; value: string }[]; reason: string }>();
	const schemaCache = new Map<string, { fields: MapperColumnShape[]; reason: string }>();
</script>

<script lang="ts">
	import { untrack } from 'svelte';

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
import { validateExpressionShape } from '$lib/workflow-editor/expression-grammar';
import { expressionCompletions, previewStep, type CompletionCandidate } from '$lib/workflow-editor/expression-assist';
	import {
		VALUELESS_OPERATORS,
		moveCondition,
		newCondition,
		readFilterValue,
		removeCondition,
		updateCondition,
		writeFilterValue,
		type Condition,
		type ConditionOperator,
		type ConditionValueType
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
	import {
		addOption,
		addableOptions,
		collectionValue,
		removeOption,
		setOption,
		setOptions,
		strandedOptions,
		unreadable
	} from '$lib/workflow-editor/collection';
import { asExpression, asFixed, expressionTemplate, isExpression, needsMultiline } from '$lib/workflow-editor/parameter';
import { loadSoon, loaderSignature } from '$lib/workflow-editor/loader-cache';

	let {
		property,
		value,
		siblings = {},
		onChange,
		loadOptions,
		loadSchema,
		contextKey = '',
		upstreamNodeNames = [],
		upstreamFieldPaths = [],
		resolvedValues = []
	}: {
		property: PropertyDefinition;
		value: unknown;
		siblings?: Record<string, unknown>;
		onChange: (value: unknown) => void;
		loadOptions?: (property: PropertyDefinition, mode?: string) => Promise<{ options: { label: string; value: string }[]; reason: string }>;
		loadSchema?: (property: PropertyDefinition) => Promise<{ fields: MapperColumn[]; reason: string }>;
		/**
		 * What outside the node's parameters a loader's answer depends on — the
		 * selected credential, today.
		 *
		 * Passed in rather than read from the node: this component must not track
		 * the whole node, or every keystroke elsewhere re-fires its loader.
		 */
		contextKey?: string;
		/** Names of upstream nodes, for `$('Name')` completions. */
		upstreamNodeNames?: string[];
		/** Dotted `$json` paths from the last run, for field completions. */
		upstreamFieldPaths?: string[];
		/** Server-resolved values of this template per item, when the host has them. */
		resolvedValues?: string[];
	} = $props();

	// Every free-text control can carry an expression; only a checkbox and a
	// nested collection lack a surface to show a template in. Selects,
	// locators, key-value rows, assignment rows and condition operands all get
	// the toggle — n8n reaches the same shape through noDataExpression.
	const expressionCapable = $derived(
		property.kind === 'string' ||
			property.kind === 'number' ||
			property.kind === 'json' ||
			property.kind === 'dateTime' ||
			property.kind === 'options' ||
			property.kind === 'resourceLocator' ||
			property.kind === 'keyValue' ||
			property.kind === 'assignmentCollection' ||
			property.kind === 'conditions'
	);

	/** Every kind this panel knows how to render. */
	const RENDERED = new Set([
		'string', 'number', 'boolean', 'options', 'multiOptions',
		'collection', 'fixedCollection', 'notice', 'json', 'dateTime',
		'keyValue', 'conditions', 'assignmentCollection', 'resourceLocator', 'resourceMapper'
	]);

	const typeOptions = $derived(property.typeOptions ?? {});

	/**
	 * A unique suffix for this field's controls.
	 *
	 * `property-{key}` was reused by every recursive instance, so two rows of a
	 * fixed collection shared one id and every label pointed at the first of
	 * them.
	 */
	const fieldID = $props.id();

	/**
	 * Kinds that render one element carrying the field's id.
	 *
	 * The others — collections, key/value rows, a mapper — are a group of
	 * controls with no single target, and a `<label for>` pointing at an id
	 * nothing renders is a label that does not label anything.
	 */
	const CONTROL_KINDS: Record<string, true> = {
		string: true,
		number: true,
		boolean: true,
		options: true,
		dateTime: true,
		json: true,
		resourceLocator: true
	};
	const labelTarget = $derived(CONTROL_KINDS[property.kind] ? `property-${fieldID}` : null);

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

	/** A resource mapper's columns depend on the parameters it declared, as above. */
	const schemaKey = $derived(property.kind === 'resourceMapper' ? loaderSignature(property.mapper?.schema, siblings, contextKey) : '');

	$effect(() => {
		const key = schemaKey;
		if (key === '' || !loadSchema) return;
		return loadSoon({
			key: `schema:${key}`,
			load: () => untrack(() => loadSchema(property)),
			apply: (result) => (schemaState = result),
			fail: (reason) => ({ fields: [], reason }),
			cache: schemaCache
		});
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

	/**
	 * What a loader's answer depends on: the parameters it declared, and the
	 * credential the call is made with.
	 *
	 * Keying the effect on anything else — the whole node, as it was — means a
	 * keystroke in an unrelated field sends a new request, and for a model list,
	 * one to the customer's own AI provider.
	 */
	const loaderKey = $derived(loaderSignature(activeLoader, siblings, contextKey));

	$effect(() => {
		const key = loaderKey;
		const loader = untrack(() => activeLoader);
		if (!loader || !loadOptions) return;
		const mode = untrack(() => (property.kind === 'resourceLocator' ? locator.mode : undefined));
		return loadSoon({
			key,
			// The request itself reads the node's parameters to build its body, so
			// the call is untracked: the key above is the whole dependency.
			load: () => untrack(() => loadOptions(property, mode)),
			apply: (result) => (loadState = result),
			fail: (reason) => ({ options: [], reason }),
			cache: optionsCache
		});
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
	// Completions read the served grammar plus the run context the host
	// passes down: `$json` fields from the last execution, upstream node
	// names, and the engine's function list. Nothing here evaluates — it
	// only names what the server will accept.
	const assistPrefix = $derived.by(() => {
		const cursor = template.match(/(\$[A-Za-z0-9_.(']*)$/);
		return cursor?.[1] ?? '';
	});
	const assistCandidates = $derived<CompletionCandidate[]>(
		expressionCompletions(assistPrefix, { nodeNames: upstreamNodeNames, fieldPaths: upstreamFieldPaths })
	);
	let assistOpen = $state(false);
	let previewIndex = $state(0);
	const preview = $derived(previewStep(resolvedValues, previewIndex));
	const stringValue = $derived(typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value));

	// The three views of an options collection: what is set, what can still be
	// added, and what is set but no longer applies.
	const chosenOptions = $derived(property.kind === 'collection' ? setOptions(property, value, siblings) : []);
	const addable = $derived(property.kind === 'collection' ? addableOptions(property, value, siblings) : []);
	const strandedKeys = $derived(property.kind === 'collection' ? strandedOptions(property, value, siblings) : []);
	const unreadableCollection = $derived(property.kind === 'collection' ? unreadable(value) : null);
	// Structured values are pretty-printed rather than String()'d: String on an
	// object is "[object Object]", and the first keystroke wrote that text back.
	function jsonText(input: unknown): string {
		if (typeof input === 'string') return input;
		if (input === undefined || input === null) return '';
		try {
			return JSON.stringify(input, null, 2);
		} catch {
			return String(input);
		}
	}

	let jsonError = $state<string | null>(null);

	function tryParseJson(text: string): { ok: true; value: unknown } | { ok: false } {
		const trimmed = text.trim();
		if (trimmed === '') return { ok: true, value: '' };
		try {
			return { ok: true, value: JSON.parse(text) };
		} catch {
			return { ok: false };
		}
	}
	const objectValue = $derived(isObject(value) ? value : {});
	// Rows, never a string. The whole list is handed back on every edit, so
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

	/** What a row's value editor shows: the template text for expressions. */
	function assignmentText(row: Assignment): string {
		if (isExpression(row.value)) return expressionTemplate(row.value);
		if (row.type === 'array' || row.type === 'object') {
			return typeof row.value === 'string' ? row.value : JSON.stringify(row.value ?? (row.type === 'array' ? [] : {}));
		}
		return displayValue(row.value);
	}

	function assignmentIsExpression(row: Assignment): boolean {
		return isExpression(row.value);
	}

	function toggleAssignmentExpression(index: number) {
		const row = assignments[index];
		const next = assignmentIsExpression(row) ? asFixed(row.value) : asExpression(row.value);
		updateAssignment(index, { value: next });
	}

	function assignmentInput(index: number, row: Assignment, text: string) {
		// Typing {{ switches the row to expression mode, as n8n does; clearing
		// the braces back out returns it to a literal.
		if (isExpression(row.value) || text.includes('{{')) {
			updateAssignment(index, { value: { mode: 'expression', value: text } });
			return;
		}
		updateAssignment(index, { value: row.type === 'number' ? text : text });
	}

	function keyValueText(item: unknown): string {
		return isExpression(item) ? expressionTemplate(item) : displayValue(item);
	}

	function toggleKeyValueExpression(key: string) {
		const item = objectValue[key];
		onChange({ ...objectValue, [key]: isExpression(item) ? asFixed(item) : asExpression(item) });
	}

	function keyValueInput(key: string, item: unknown, text: string) {
		if (isExpression(item) || text.includes('{{')) {
			onChange({ ...objectValue, [key]: { mode: 'expression', value: text } });
			return;
		}
		onChange({ ...objectValue, [key]: parseValue(text) });
	}

	function renameKey(previousKey: string, nextKey: string) {
		onChange(renameKeyValue(objectValue, previousKey, nextKey));
	}

	function removeKeyValue(key: string) {
		const next = { ...objectValue };
		delete next[key];
		onChange(next);
	}

	function addKeyValue() {
		// A suffixed key rather than '': adding twice must yield two rows, and
		// renaming onto an existing key must not silently overwrite it.
		let candidate = `field${Object.keys(objectValue).length + 1}`;
		let suffix = Object.keys(objectValue).length + 1;
		while (Object.prototype.hasOwnProperty.call(objectValue, candidate)) {
			suffix += 1;
			candidate = `field${suffix}`;
		}
		onChange({ ...objectValue, [candidate]: '' });
	}

	const conditionFilter = $derived(readFilterValue(value));

	function writeFilter(next: { combinator?: 'and' | 'or'; conditions?: Condition[] }) {
		onChange(
			writeFilterValue({
				combinator: next.combinator ?? conditionFilter.combinator,
				conditions: next.conditions ?? conditionFilter.conditions,
				options: conditionFilter.options
			})
		);
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
	 * never claims a value. The check itself lives in expression-grammar next
	 * to the served allowlist it reads, so there is exactly one copy.
	 */
	function expressionHint(text: string): string | null {
		return validateExpressionShape(text);
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

<div class="grid gap-1" role={labelTarget ? undefined : 'group'} aria-labelledby={labelTarget ? undefined : `property-label-${fieldID}`}>
	<div class="flex items-baseline justify-between gap-2">
		{#if labelTarget}
			<label class="text-xs font-medium leading-tight" for={labelTarget}>{property.label}{#if property.required}<span aria-hidden="true" class="text-destructive"> *</span>{/if}</label>
		{:else}
			<span id={`property-label-${fieldID}`} class="text-xs font-medium leading-tight">{property.label}{#if property.required}<span aria-hidden="true" class="text-destructive"> *</span>{/if}</span>
		{/if}
		{#if expressionCapable}
			<button type="button" role="switch" aria-checked={expressionMode} aria-label={`${property.label}: expression mode`} class="shrink-0 rounded border border-border px-1 py-0.5 font-mono text-[0.625rem] leading-4 text-muted-foreground transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring aria-checked:border-primary/40 aria-checked:bg-primary/10 aria-checked:text-primary" onclick={toggleExpression}>
				{expressionMode ? 'expr' : 'fixed'}
			</button>
		{/if}
	</div>
	{#if expressionMode}
		<textarea id={`property-${fieldID}`} value={template} spellcheck="false" rows={Math.min(12, Math.max(3, template.split('\n').length))} class="rounded-md border border-primary/40 bg-primary/5 px-2 py-1.5 font-mono text-xs" aria-describedby={`property-${fieldID}-hint`} oninput={(event) => {
			const next = event.currentTarget.value;
			assistOpen = true;
			onChange(next.includes('{{') ? { mode: 'expression', value: next } : next);
		}} onfocus={() => (assistOpen = true)} onblur={() => setTimeout(() => (assistOpen = false), 120)}></textarea>
		{#if assistOpen && assistCandidates.length > 0 && assistPrefix}
			<ul role="listbox" aria-label={`${property.label} expression suggestions`} class="max-h-36 overflow-y-auto rounded-md border border-border bg-popover p-1 shadow-md">
				{#each assistCandidates.slice(0, 8) as candidate (candidate.insert)}
					<li role="option" aria-selected="false">
						<button type="button" class="flex w-full items-center gap-2 rounded px-1.5 py-1 text-left font-mono text-[0.6875rem] hover:bg-muted" onmousedown={(event) => {
							event.preventDefault();
							const next = `${template}${candidate.insert.trim()} }}`;
							assistOpen = false;
							onChange({ mode: 'expression', value: next });
						}}>
							<span class="min-w-0 flex-1 truncate">{candidate.insert.trim()}</span>
							<span class="shrink-0 text-muted-foreground">{candidate.detail}</span>
						</button>
					</li>
				{/each}
			</ul>
		{/if}
		{#if preview.total > 0}
			<div class="flex items-center gap-1.5 rounded-md border border-border bg-muted/40 px-2 py-1">
				<span class="text-[0.6875rem] text-muted-foreground">Result{preview.total > 1 ? ` ${preview.index + 1}/${preview.total}` : ''}:</span>
				<code class="min-w-0 flex-1 truncate font-mono text-[0.6875rem]" title={preview.value}>{preview.value}</code>
				{#if preview.total > 1}
					<button type="button" class="shrink-0 rounded border border-border px-1 text-[0.6875rem]" disabled={preview.index <= 0} onclick={() => (previewIndex = preview.index - 1)} aria-label="Previous item">‹</button>
					<button type="button" class="shrink-0 rounded border border-border px-1 text-[0.6875rem]" disabled={preview.index >= preview.total - 1} onclick={() => (previewIndex = preview.index + 1)} aria-label="Next item">›</button>
				{/if}
			</div>
		{/if}
		<p id={`property-${fieldID}-hint`} class="text-[0.6875rem] leading-4 {expressionHint(template) ? 'text-destructive' : 'text-muted-foreground'}">
			{expressionHint(template) ?? 'Resolved per item on the server, for example {{ $json.id }}.'}
		</p>
	{:else if property.kind === 'boolean'}
		<label class="flex h-7 items-center gap-2 rounded-md border border-input px-2 text-xs">
			<input id={`property-${fieldID}`} type="checkbox" class="size-3.5" checked={Boolean(value)} onchange={(event) => onChange(event.currentTarget.checked)} />
			<span>{Boolean(value) ? 'Enabled' : 'Disabled'}</span>
		</label>
{:else if property.kind === 'number'}
	<!-- Raw text while typing, coerced on blur: Number() on every keystroke
	     turns a leading '-' into NaN (cloned to null), clearing the field
	     before a negative value can be finished. -->
	<input id={`property-${fieldID}`} type="text" inputmode="decimal" value={stringValue} class="h-7 rounded-md border border-input bg-background px-2 text-xs" oninput={(event) => {
		const text = event.currentTarget.value;
		if (text === '' || text === '-' || text === '.' || text === '-.') onChange(text);
		else {
			const numeric = Number(text);
			onChange(Number.isNaN(numeric) ? text : numeric);
		}
	}} onblur={(event) => {
		const text = event.currentTarget.value.trim();
		if (text === '') onChange(undefined);
		else {
			const numeric = Number(text);
			if (!Number.isNaN(numeric) && typeof value !== 'string') onChange(numeric);
		}
	}} />
	{:else if property.kind === 'notice'}
		<!-- A notice holds no value: it is never stored, never required, and
		     never sends an onChange. One that round-tripped into the document
		     would fail validation on the next save. -->
		<p class="rounded-md border border-border bg-muted/40 px-2 py-1.5 text-[0.6875rem] leading-4 text-muted-foreground">
			{property.description || property.label}
		</p>
	{:else if property.kind === 'dateTime'}
		<input id={`property-${fieldID}`} type="datetime-local" value={stringValue} class="h-7 rounded-md border border-input bg-background px-2 text-xs" oninput={(event) => onChange(event.currentTarget.value)} />
	{:else if property.kind === 'json'}
		<textarea id={`property-${fieldID}`} value={jsonText(value)} spellcheck="false" rows={typeOptions.rows || 4} class="rounded-md border border-input bg-background px-2 py-1.5 font-mono text-[0.6875rem]" aria-describedby={jsonError ? `property-${fieldID}-json-error` : undefined} oninput={(event) => {
			jsonError = null;
		}} onblur={(event) => {
			const parsed = tryParseJson(event.currentTarget.value);
			if (parsed.ok) {
				jsonError = null;
				onChange(parsed.value);
			} else {
				jsonError = 'This is not valid JSON yet — fix it before saving.';
			}
		}}></textarea>
		{#if jsonError}<p id={`property-${fieldID}-json-error`} role="alert" class="text-[0.6875rem] leading-4 text-destructive">{jsonError}</p>{/if}
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
							{contextKey}
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
	{:else if property.kind === 'collection'}
		<!-- Added one at a time, the way n8n presents the same control. A flat
		     panel of every option would read as a form to fill in, when what
		     the document means is "the ones named here, and the server's own
		     default for everything else". -->
		<div class="grid gap-1.5">
			{#if unreadableCollection}
				<!-- Named rather than silently replaced: this is a node
				     somebody configured, and showing it as empty would look
				     like the settings had been lost — which, on the next save,
				     they would have been. -->
				<div class="grid gap-1 rounded-md border border-destructive/40 bg-destructive/5 p-1.5">
					<p class="text-[0.6875rem] leading-4 text-destructive">These options were stored as text and cannot be read back as settings. Add them again, or clear the field.</p>
					<code class="overflow-x-auto rounded border border-input bg-background px-1.5 py-1 font-mono text-[0.625rem] text-muted-foreground">{unreadableCollection}</code>
					<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={() => onChange({})}>
						<X aria-hidden="true" class="size-3" />Clear
					</button>
				</div>
			{/if}
			{#each chosenOptions as field (field.key)}
				<div class="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-1 rounded-md border border-input p-1.5">
					<PropertyField
						property={field}
						value={collectionValue(value)[field.key]}
						siblings={{ ...siblings, ...collectionValue(value) }}
						{contextKey}
						onChange={(next: unknown) => onChange(setOption(value, field.key, next))}
						{loadOptions}
					/>
					<button type="button" aria-label={`Remove ${field.label}`} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => onChange(removeOption(value, field.key))}>
						<X aria-hidden="true" class="size-3.5" />
					</button>
				</div>
			{/each}
			{#each strandedKeys as key (key)}
				<!-- Kept in the document, because switching the operation back
				     must find it, and named here because a node behaving in a
				     way nothing on screen explains is worse than a crowded
				     panel. -->
				<div class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-1 rounded-md border border-dashed border-input px-1.5 py-1">
					<span class="text-[0.6875rem] leading-4 text-muted-foreground">
						<code class="font-mono">{key}</code> is set but does not apply to this operation.
					</span>
					<button type="button" aria-label={`Remove ${key}`} class="grid size-6 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => onChange(removeOption(value, key))}>
						<X aria-hidden="true" class="size-3" />
					</button>
				</div>
			{/each}
			{#if addable.length > 0}
				<select
					aria-label={`Add ${property.label}`}
					value=""
					class="h-7 justify-self-start rounded-md border border-input bg-background px-1.5 text-xs"
					onchange={(event) => {
						const field = addable.find((candidate) => candidate.key === event.currentTarget.value);
						if (field) onChange(addOption(value, field));
						event.currentTarget.value = '';
					}}
				>
					<option value="">+ Add option</option>
					{#each addable as field (field.key)}
						<option value={field.key}>{field.label}</option>
					{/each}
				</select>
			{:else if chosenOptions.length === 0 && strandedKeys.length === 0}
				<p class="text-[0.6875rem] leading-4 text-muted-foreground">No options apply to this operation.</p>
			{/if}
		</div>
	{:else if property.kind === 'fixedCollection'}
		<!-- The multi-group fixed collection, which has no row builder yet for
		     several groups: shown as what is configured without pretending to
		     edit a shape there is no control for. -->
		<div class="grid gap-1 rounded-md border border-dashed border-input p-1.5 text-[0.6875rem] text-muted-foreground">
			<span>{(property.fields ?? []).length || (property.groups ?? []).length} nested field(s)</span>
			<textarea aria-label={`${property.label} value`} value={jsonText(value)} spellcheck="false" rows={3} class="rounded border border-input bg-background px-1.5 py-1 font-mono text-[0.6875rem]" onblur={(event) => {
				const parsed = tryParseJson(event.currentTarget.value);
				if (parsed.ok) onChange(parsed.value);
			}}></textarea>
		</div>
	{:else if property.kind === 'keyValue'}
		<div class="grid gap-1.5 rounded-md border border-input p-1.5">
			{#each Object.entries(objectValue) as [key, item] (key)}
				<div class="grid gap-1 rounded border border-border/70 p-1.5">
					<div class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-1">
						<input aria-label={`${property.label} field name`} value={key} class="h-7 min-w-0 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" onchange={(event) => renameKey(key, event.currentTarget.value)} />
						<button type="button" role="switch" aria-checked={isExpression(item)} aria-label={`${property.label} ${key}: expression mode`} class="shrink-0 rounded border border-border px-1 py-0.5 font-mono text-[0.625rem] text-muted-foreground transition-colors hover:bg-muted aria-checked:border-primary/40 aria-checked:bg-primary/10 aria-checked:text-primary" onclick={() => toggleKeyValueExpression(key)}>
							{isExpression(item) ? 'expr' : 'fixed'}
						</button>
					</div>
					<div class="grid grid-cols-[minmax(0,1fr)_auto] gap-1">
						{#if !isExpression(item) && typeof item === 'string' && item.includes('\n')}
							<textarea aria-label={`${property.label} field value`} value={keyValueText(item)} rows={Math.min(8, Math.max(2, String(item).split('\n').length))} class="min-w-0 rounded border border-input bg-background px-1.5 py-1 font-mono text-[0.6875rem]" oninput={(event) => keyValueInput(key, item, event.currentTarget.value)}></textarea>
						{:else}
							<input aria-label={`${property.label} field value`} value={keyValueText(item)} class="h-7 min-w-0 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => keyValueInput(key, item, event.currentTarget.value)} />
						{/if}
						<button type="button" aria-label={`Remove ${key || 'assignment'}`} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => removeKeyValue(key)}>
							<X aria-hidden="true" class="size-3.5" />
						</button>
					</div>
				</div>
			{/each}
			<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={addKeyValue}>
				<Plus aria-hidden="true" class="size-3" />Add field
			</button>
		</div>
	{:else if property.kind === 'assignmentCollection'}
		<div class="grid gap-1.5 rounded-md border border-input p-1.5">
			{#each assignments as row, index (row.id)}
				<div class="grid gap-1 rounded border border-border/70 p-1.5">
					<div class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-1">
						<input aria-label={`${property.label} field name`} value={row.name} placeholder="fieldName" class="h-7 min-w-0 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => updateAssignment(index, { name: event.currentTarget.value })} />
						<button type="button" role="switch" aria-checked={assignmentIsExpression(row)} aria-label={`${property.label} ${row.name || 'field'}: expression mode`} class="shrink-0 rounded border border-border px-1 py-0.5 font-mono text-[0.625rem] text-muted-foreground transition-colors hover:bg-muted aria-checked:border-primary/40 aria-checked:bg-primary/10 aria-checked:text-primary" onclick={() => toggleAssignmentExpression(index)}>
							{assignmentIsExpression(row) ? 'expr' : 'fixed'}
						</button>
					</div>
					<div class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1.2fr)_auto] gap-1">
						<select aria-label={`${property.label} field type`} value={row.type} class="h-7 rounded border border-input bg-background px-1 text-[0.6875rem]" onchange={(event) => retypeAssignment(index, event.currentTarget.value as AssignmentType)}>
							{#each ASSIGNMENT_TYPES as type (type)}
								<option value={type}>{type}</option>
							{/each}
						</select>
						<span class="sr-only">value</span>
						{#if row.type === 'boolean' && !assignmentIsExpression(row)}
							<select aria-label={`${property.label} field value`} value={row.value === true ? 'true' : 'false'} class="h-7 rounded border border-input bg-background px-1 text-[0.6875rem]" onchange={(event) => updateAssignment(index, { value: event.currentTarget.value === 'true' })}>
								<option value="true">true</option>
								<option value="false">false</option>
							</select>
						{:else if typeof row.value === 'string' && (row.value as string).includes('\n')}
							<!-- Same rule as top-level strings: a multi-line row value
							     gets a textarea, never an input that would flatten it. -->
							<textarea aria-label={`${property.label} field value`} value={assignmentText(row)} rows={Math.min(8, Math.max(2, (row.value as string).split('\n').length))} class="min-w-0 rounded border border-input bg-background px-1.5 py-1 font-mono text-[0.6875rem]" oninput={(event) => assignmentInput(index, row, event.currentTarget.value)}></textarea>
						{:else}
							<input aria-label={`${property.label} field value`} value={assignmentText(row)} class="h-7 min-w-0 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => assignmentInput(index, row, event.currentTarget.value)} />
						{/if}
						<button type="button" aria-label={`Remove ${row.name || 'field'}`} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => removeAssignment(index)}>
							<X aria-hidden="true" class="size-3.5" />
						</button>
					</div>
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
					<select id={`property-${fieldID}`} value={displayValue(locator.value)} class="h-7 min-w-0 flex-1 rounded-md border border-input bg-background px-1.5 text-xs" onchange={(event) => onChange(writeLocator({ ...locator, value: event.currentTarget.value, cachedResultName: selectedLabel(event.currentTarget.value) }))}>
						<option value="">{locatorMode.placeholder || 'Choose…'}</option>
						{#each selectableOptions as option (option.value)}
							<option value={option.value}>{option.label}</option>
						{/each}
					</select>
				{:else}
					<input id={`property-${fieldID}`} value={displayValue(locator.value)} placeholder={locatorMode.placeholder ?? ''} class="h-7 min-w-0 flex-1 rounded-md border border-input bg-background px-2 text-xs" oninput={(event) => onChange(writeLocator({ ...locator, value: event.currentTarget.value }))} />
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
			<div class="flex items-center gap-2">
				<span class="text-[0.6875rem] text-muted-foreground">Match</span>
				<select aria-label={`${property.label} combinator`} value={conditionFilter.combinator} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => writeFilter({ combinator: event.currentTarget.value === 'or' ? 'or' : 'and' })}>
					<option value="and">All (AND)</option>
					<option value="or">Any (OR)</option>
				</select>
			</div>
			{#each conditionFilter.conditions as row, index (index)}
				<div class="grid gap-1 rounded border border-border/70 p-1.5">
					<div class="flex items-center gap-1">
						<input aria-label={`${property.label} left value ${index + 1}`} value={String(row.leftValue ?? '')} placeholder={'{{ $json.field }}'} class="h-7 min-w-0 flex-1 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => writeFilter({ conditions: updateCondition(conditionFilter.conditions, index, { leftValue: event.currentTarget.value }) })} />
						<!-- Order is meaningful: the rules read top to bottom. -->
						<button type="button" aria-label={`Move condition ${index + 1} up`} disabled={index === 0} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted disabled:opacity-40" onclick={() => writeFilter({ conditions: moveCondition(conditionFilter.conditions, index, -1) })}>
							<ChevronUp aria-hidden="true" class="size-3.5" />
						</button>
						<button type="button" aria-label={`Move condition ${index + 1} down`} disabled={index === conditionFilter.conditions.length - 1} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted disabled:opacity-40" onclick={() => writeFilter({ conditions: moveCondition(conditionFilter.conditions, index, 1) })}>
							<ChevronDown aria-hidden="true" class="size-3.5" />
						</button>
						<button type="button" aria-label={`Remove condition ${index + 1}`} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => writeFilter({ conditions: removeCondition(conditionFilter.conditions, index) })}>
							<X aria-hidden="true" class="size-3.5" />
						</button>
					</div>
					<div class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-1">
						<select aria-label={`${property.label} type ${index + 1}`} value={row.operator.type} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => writeFilter({ conditions: updateCondition(conditionFilter.conditions, index, { operator: { type: event.currentTarget.value as ConditionValueType, operation: row.operator.operation } }) })}>
							<option value="string">String</option>
							<option value="number">Number</option>
							<option value="boolean">Boolean</option>
							<option value="dateTime">Date &amp; Time</option>
							<option value="array">Array</option>
							<option value="object">Object</option>
						</select>
						<select aria-label={`${property.label} operator ${index + 1}`} value={row.operator.operation} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => writeFilter({ conditions: updateCondition(conditionFilter.conditions, index, { operator: { type: row.operator.type, operation: event.currentTarget.value as ConditionOperator } }) })}>
							<option value="equals">equals</option>
							<option value="notEquals">does not equal</option>
							<option value="contains">contains</option>
							<option value="notContains">does not contain</option>
							<option value="startsWith">starts with</option>
							<option value="notStartsWith">does not start with</option>
							<option value="endsWith">ends with</option>
							<option value="notEndsWith">does not end with</option>
							<option value="regex">matches regex</option>
							<option value="notRegex">does not match regex</option>
							<option value="empty">is empty</option>
							<option value="notEmpty">is not empty</option>
							<option value="exists">exists</option>
							<option value="notExists">does not exist</option>
							<option value="larger">larger than</option>
							<option value="largerEqual">larger or equal</option>
							<option value="smaller">smaller than</option>
							<option value="smallerEqual">smaller or equal</option>
							<option value="true">is true</option>
							<option value="false">is false</option>
							<option value="after">after</option>
							<option value="before">before</option>
						</select>
					</div>
					{#if !VALUELESS_OPERATORS.includes(row.operator.operation)}
						<input aria-label={`${property.label} right value ${index + 1}`} value={String(row.rightValue ?? '')} class="h-7 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => writeFilter({ conditions: updateCondition(conditionFilter.conditions, index, { rightValue: event.currentTarget.value }) })} />
					{/if}
				</div>
			{/each}
			<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={() => writeFilter({ conditions: [...conditionFilter.conditions, newCondition()] })}>
				<Plus aria-hidden="true" class="size-3" />Add condition
			</button>
		</div>
{:else if RENDERED.has(property.kind) && needsMultiline(value, typeOptions.rows)}
	<!-- Multi-line is a different element, not an attribute: rows has no
	     meaning on an input, and an input strips the newlines of a value
	     that arrived multi-line. -->
	<textarea id={`property-${fieldID}`} value={stringValue} rows={Math.max(typeOptions.rows ?? 3, stringValue.split('\n').length)} class="rounded-md border border-input bg-background px-2 py-1.5 text-xs" oninput={(event) => onChange(event.currentTarget.value)}></textarea>
	{:else if RENDERED.has(property.kind)}
		<input id={`property-${fieldID}`} value={stringValue} type={typeOptions.password ? 'password' : 'text'} class="h-7 rounded-md border border-input bg-background px-2 text-xs" oninput={(event) => onChange(event.currentTarget.value)} />
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
