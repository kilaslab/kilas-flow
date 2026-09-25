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
import { assistKey, completionInsertion, expressionCompletions, previewStep, type CompletionCandidate } from '$lib/workflow-editor/expression-assist';
	import {
		VALUELESS_OPERATORS,
		moveCondition,
		newCondition,
		removeCondition,
		readFilterValue,
		typeForOperation,
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
	import CodeEditor from './code-editor.svelte';
import { loadSoon, loaderSignature } from '$lib/workflow-editor/loader-cache';
	import * as m from '$lib/paraglide/messages.js';

	let {
		property,
		value,
		siblings = {},
		onChange,
		loadOptions,
		loadSchema,
		contextKey = '',
		ownerKey = '',
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
		/**
		 * Which node this field edits. The panel reuses a field across node
		 * selections when two nodes share a parameter key, so a control that keeps
		 * its own state — a code editor's undo history — remounts on a change.
		 */
		ownerKey?: string;
		/** Names of upstream nodes, for `$('Name')` completions. */
		upstreamNodeNames?: string[];
		/** Dotted `$json` paths from the last run, for field completions. */
		upstreamFieldPaths?: string[];
		/** Server-resolved values of this template per item, when the host has them. */
		resolvedValues?: string[];
	} = $props();

	// Every free-text control can carry an expression; only a checkbox and a
	// nested collection lack a surface to show a template in, and a code
	// editor holds a program, where `{{ }}` or a leading `=` is program text. Selects,
	// locators, key-value rows, assignment rows and condition operands all get
	// the toggle — n8n reaches the same shape through noDataExpression.
	const expressionCapable = $derived(
		(property.kind === 'string' && property.typeOptions?.editor !== 'code') ||
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
	const codeEditor = $derived(property.kind === 'string' && typeOptions.editor === 'code' && !!typeOptions.editorLanguage);

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
	// A code editor's surface is a contenteditable, which a <label for> cannot
	// name; it carries the label as its own aria-label instead.
	const labelTarget = $derived(CONTROL_KINDS[property.kind] && !codeEditor ? `property-${fieldID}` : null);

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
	/** The locator's value as a reader sees it: JSON for anything structured. */
	const locatorDisplay = $derived(displayValue(locator.value));

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
	/**
	 * The engine's own placeholder syntax, shown as the fallback hint.
	 *
	 * A literal and not a catalog value: it is the shape the evaluator accepts,
	 * the same in every locale, and a catalog value cannot hold `{{ … }}` —
	 * paraglide reads that as a variable reference of its own.
	 */
	const expressionExample = '{{ $json.id }}';
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
	// Only the first screen of candidates is offered, and the highlight is kept
	// as a raw index so a narrower prefix cannot leave it past the end: what is
	// shown is derived and clamped, the keystroke moves the raw value.
	const assistOptions = $derived(assistCandidates.slice(0, 8));
	const assistVisible = $derived(assistOpen && assistPrefix !== '' && assistOptions.length > 0);
	let assistIndex = $state(0);
	const assistActive = $derived(Math.min(Math.max(assistIndex, 0), Math.max(assistOptions.length - 1, 0)));
	let assistList = $state<HTMLElement>();
	const assistOptionID = (index: number) => `property-${fieldID}-suggestion-${index}`;

	/** Inserts a candidate, closing the braces the prefix left open. */
	function acceptSuggestion(candidate: CompletionCandidate) {
		assistOpen = false;
		const body = completionInsertion(template, assistPrefix, candidate.insert);
		onChange({ mode: 'expression', value: `${body} }}` });
	}

	/**
	 * The keyboard path for the suggestion list. The textarea keeps focus — it
	 * is where the user is typing — and the arrows move the highlight that
	 * aria-activedescendant points at, so this stops the caret moving instead
	 * and, for Enter, stops the newline the textarea would otherwise insert.
	 */
	function onAssistKeydown(event: KeyboardEvent) {
		if (!assistVisible) return;
		const decision = assistKey(assistIndex, assistOptions.length, event.key);
		if (decision.action === null) return;
		event.preventDefault();
		assistIndex = decision.index;
		if (decision.action === 'move') return;
		if (decision.action === 'dismiss') {
			assistOpen = false;
			return;
		}
		acceptSuggestion(assistOptions[decision.index]);
	}

	// The list scrolls, and eight candidates do not fit in it: without this the
	// arrows could walk the highlight off the bottom with nothing on screen
	// moving to say which row Enter would insert.
	$effect(() => {
		if (!assistVisible) return;
		assistList?.querySelector(`#${assistOptionID(assistActive)}`)?.scrollIntoView({ block: 'nearest' });
	});

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

	/**
	 * The operator picker's labels, keyed by the wire operation.
	 *
	 * A record rather than a hand-written list of `<option>`s: the type makes
	 * the compiler demand a label for every operation the evaluator knows, so a
	 * new one cannot join `ConditionOperator` and go unnamed here. Key order is
	 * the order the picker shows.
	 */
	const operatorLabels: Record<ConditionOperator, () => string> = {
		equals: m.properties_operator_equals,
		notEquals: m.properties_operator_not_equals,
		contains: m.properties_operator_contains,
		notContains: m.properties_operator_not_contains,
		startsWith: m.properties_operator_starts_with,
		notStartsWith: m.properties_operator_not_starts_with,
		endsWith: m.properties_operator_ends_with,
		notEndsWith: m.properties_operator_not_ends_with,
		regex: m.properties_operator_matches_regex,
		notRegex: m.properties_operator_not_matches_regex,
		empty: m.properties_operator_is_empty,
		notEmpty: m.properties_operator_is_not_empty,
		exists: m.properties_operator_exists,
		notExists: m.properties_operator_not_exists,
		gt: m.properties_operator_larger_than,
		gte: m.properties_operator_larger_or_equal,
		lt: m.properties_operator_smaller_than,
		lte: m.properties_operator_smaller_or_equal,
		true: m.properties_operator_is_true,
		false: m.properties_operator_is_false,
		after: m.properties_operator_after,
		before: m.properties_operator_before
	};
	const operators = Object.entries(operatorLabels) as [ConditionOperator, () => string][];

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
			<button type="button" role="switch" aria-checked={expressionMode} aria-label={m.properties_expression_mode_aria({ label: property.label })} class="shrink-0 rounded border border-border px-1 py-0.5 font-mono text-[0.625rem] leading-4 text-muted-foreground transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring aria-checked:border-primary/40 aria-checked:bg-primary/10 aria-checked:text-primary" onclick={toggleExpression}>
				{expressionMode ? m.properties_mode_expr() : m.properties_mode_fixed()}
			</button>
		{/if}
	</div>
	{#if expressionMode}
		<textarea id={`property-${fieldID}`} value={template} spellcheck="false" rows={Math.min(12, Math.max(3, template.split('\n').length))} class="rounded-md border border-primary/40 bg-primary/5 px-2 py-1.5 font-mono text-xs" aria-describedby={`property-${fieldID}-hint`} role="combobox" aria-expanded={assistVisible} aria-controls={`property-${fieldID}-suggestions`} aria-autocomplete="list" aria-activedescendant={assistVisible ? assistOptionID(assistActive) : undefined} onkeydown={onAssistKeydown} oninput={(event) => {
			const next = event.currentTarget.value;
			// A new prefix is a new list: the old highlight pointed into the
			// candidates that are about to be replaced.
			assistIndex = 0;
			assistOpen = true;
			onChange(next.includes('{{') ? { mode: 'expression', value: next } : next);
		}} onfocus={() => (assistOpen = true)} onblur={() => setTimeout(() => (assistOpen = false), 120)}></textarea>
		{#if assistVisible}
			<div
				id={`property-${fieldID}-suggestions`}
				bind:this={assistList}
				role="listbox"
				aria-label={m.properties_expression_suggestions_aria({ label: property.label })}
				class="max-h-36 overflow-y-auto rounded-md border border-border bg-popover p-1 shadow-md"
			>
				{#each assistOptions as candidate, index (candidate.insert)}
					<button
						type="button"
						id={assistOptionID(index)}
						role="option"
						tabindex="-1"
						aria-selected={index === assistActive}
						class="flex w-full items-center gap-2 rounded px-1.5 py-1 text-left font-mono text-[0.6875rem] hover:bg-muted aria-selected:bg-accent focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring"
						onmousedown={(event) => {
							event.preventDefault();
							acceptSuggestion(candidate);
						}}
						onmousemove={() => (assistIndex = index)}
					>
						<span class="min-w-0 flex-1 truncate">{candidate.insert.trim()}</span>
						<span class="shrink-0 text-muted-foreground">{candidate.detail}</span>
					</button>
				{/each}
			</div>
		{/if}
		{#if preview.total > 0}
			<div class="flex items-center gap-1.5 rounded-md border border-border bg-muted/40 px-2 py-1">
				<span class="text-[0.6875rem] text-muted-foreground">{preview.total > 1 ? m.properties_result_position({ position: `${preview.index + 1}/${preview.total}` }) : m.properties_result()}</span>
				<code class="min-w-0 flex-1 truncate font-mono text-[0.6875rem]" title={preview.value}>{preview.value}</code>
				{#if preview.total > 1}
					<button type="button" class="shrink-0 rounded border border-border px-1 text-[0.6875rem]" disabled={preview.index <= 0} onclick={() => (previewIndex = preview.index - 1)} aria-label={m.properties_previous_item()}>‹</button>
					<button type="button" class="shrink-0 rounded border border-border px-1 text-[0.6875rem]" disabled={preview.index >= preview.total - 1} onclick={() => (previewIndex = preview.index + 1)} aria-label={m.properties_next_item()}>›</button>
				{/if}
			</div>
		{/if}
		<p id={`property-${fieldID}-hint`} class="text-[0.6875rem] leading-4 {expressionHint(template) ? 'text-destructive' : 'text-muted-foreground'}">
			{expressionHint(template) ?? m.properties_expression_hint_default({ example: expressionExample })}
		</p>
	{:else if property.kind === 'boolean'}
		<label class="flex h-7 items-center gap-2 rounded-md border border-input px-2 text-xs">
			<input id={`property-${fieldID}`} type="checkbox" class="size-3.5" checked={Boolean(value)} onchange={(event) => onChange(event.currentTarget.checked)} />
			<span>{Boolean(value) ? m.properties_enabled() : m.properties_disabled()}</span>
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
				jsonError = m.properties_error_invalid_json();
			}
		}}></textarea>
		{#if jsonError}<p id={`property-${fieldID}-json-error`} role="alert" class="text-[0.6875rem] leading-4 text-destructive">{jsonError}</p>{/if}
	{:else if property.kind === 'options'}
		<!-- A property whose valid values are declared (or fetched) is a
		     choice, not free text: 40cc8db dropped this branch, so every one
		     of them fell through to the text input below and the customer
		     could type a value the node would refuse. The value is carried
		     even when it is not in the list — a saved node, a loader still
		     pending, or a loader that failed must not render as the first
		     option and read back as a silent change. -->
		<select id={`property-${fieldID}`} value={stringValue} class="h-7 rounded-md border border-input bg-background px-1.5 text-xs" onchange={(event) => onChange(event.currentTarget.value)}>
			{#if stringValue !== '' && !selectableOptions.some((option) => option.value === stringValue)}
				<option value={stringValue}>{stringValue}</option>
			{/if}
			{#each selectableOptions as option (option.value)}
				<option value={option.value}>{option.label}</option>
			{/each}
		</select>
		{#if loadState.reason}
			<p class="text-[0.6875rem] leading-4 text-muted-foreground">{loadState.reason}</p>
		{/if}
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
						<button type="button" aria-label={m.properties_remove_group({ group: group.label, index: index + 1 })} class="grid size-6 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => removeGroupEntry(index)}>
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
							{ownerKey}
							{upstreamNodeNames}
							{upstreamFieldPaths}
							onChange={(next: unknown) => updateGroupEntry(index, field.key, next)}
							{loadOptions}
						/>
					{/each}
				</div>
			{/each}
			<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={addGroupEntry}>
				<Plus aria-hidden="true" class="size-3" />{typeOptions.multipleValueButtonText || m.properties_add_group({ group: group.label })}
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
					<p class="text-[0.6875rem] leading-4 text-destructive">{m.properties_collection_unreadable()}</p>
					<code class="overflow-x-auto rounded border border-input bg-background px-1.5 py-1 font-mono text-[0.625rem] text-muted-foreground">{unreadableCollection}</code>
					<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={() => onChange({})}>
						<X aria-hidden="true" class="size-3" />{m.workflows_clear()}
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
						{ownerKey}
						{upstreamNodeNames}
						{upstreamFieldPaths}
						onChange={(next: unknown) => onChange(setOption(value, field.key, next))}
						{loadOptions}
					/>
					<button type="button" aria-label={m.properties_remove_property({ label: field.label })} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => onChange(removeOption(value, field.key))}>
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
						{m.properties_stranded_option({ key })}
					</span>
					<button type="button" aria-label={`Remove ${key}`} class="grid size-6 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => onChange(removeOption(value, key))}>
						<X aria-hidden="true" class="size-3" />
					</button>
				</div>
			{/each}
			{#if addable.length > 0}
				<select
					aria-label={m.properties_add_property({ label: property.label })}
					value=""
					class="h-7 justify-self-start rounded-md border border-input bg-background px-1.5 text-xs"
					onchange={(event) => {
						const field = addable.find((candidate) => candidate.key === event.currentTarget.value);
						if (field) onChange(addOption(value, field));
						event.currentTarget.value = '';
					}}
				>
					<option value="">{m.properties_add_option()}</option>
					{#each addable as field (field.key)}
						<option value={field.key}>{field.label}</option>
					{/each}
				</select>
			{:else if chosenOptions.length === 0 && strandedKeys.length === 0}
				<p class="text-[0.6875rem] leading-4 text-muted-foreground">{m.properties_no_applicable_options()}</p>
			{/if}
		</div>
	{:else if property.kind === 'fixedCollection'}
		<!-- The multi-group fixed collection, which has no row builder yet for
		     several groups: shown as what is configured without pretending to
		     edit a shape there is no control for. -->
		<div class="grid gap-1 rounded-md border border-dashed border-input p-1.5 text-[0.6875rem] text-muted-foreground">
			<span>{m.properties_nested_fields({ count: (property.fields ?? []).length || (property.groups ?? []).length })}</span>
			<textarea aria-label={m.properties_property_value_aria({ label: property.label })} value={jsonText(value)} spellcheck="false" rows={3} class="rounded border border-input bg-background px-1.5 py-1 font-mono text-[0.6875rem]" onblur={(event) => {
				const parsed = tryParseJson(event.currentTarget.value);
				if (parsed.ok) onChange(parsed.value);
			}}></textarea>
		</div>
	{:else if property.kind === 'keyValue'}
		<div class="grid gap-1.5 rounded-md border border-input p-1.5">
			{#each Object.entries(objectValue) as [key, item] (key)}
				<div class="grid gap-1 rounded border border-border/70 p-1.5">
					<div class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-1">
						<input aria-label={m.properties_field_name_aria({ label: property.label })} value={key} class="h-7 min-w-0 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" onchange={(event) => renameKey(key, event.currentTarget.value)} />
						<button type="button" role="switch" aria-checked={isExpression(item)} aria-label={m.properties_key_expression_mode_aria({ label: property.label, key })} class="shrink-0 rounded border border-border px-1 py-0.5 font-mono text-[0.625rem] text-muted-foreground transition-colors hover:bg-muted aria-checked:border-primary/40 aria-checked:bg-primary/10 aria-checked:text-primary" onclick={() => toggleKeyValueExpression(key)}>
							{isExpression(item) ? m.properties_mode_expr() : m.properties_mode_fixed()}
						</button>
					</div>
					<div class="grid grid-cols-[minmax(0,1fr)_auto] gap-1">
						{#if !isExpression(item) && typeof item === 'string' && item.includes('\n')}
							<textarea aria-label={m.properties_field_value_aria({ label: property.label })} value={keyValueText(item)} rows={Math.min(8, Math.max(2, String(item).split('\n').length))} class="min-w-0 rounded border border-input bg-background px-1.5 py-1 font-mono text-[0.6875rem]" oninput={(event) => keyValueInput(key, item, event.currentTarget.value)}></textarea>
						{:else}
							<input aria-label={m.properties_field_value_aria({ label: property.label })} value={keyValueText(item)} class="h-7 min-w-0 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => keyValueInput(key, item, event.currentTarget.value)} />
						{/if}
						<button type="button" aria-label={m.properties_remove_property({ label: key || m.properties_assignment_fallback() })} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => removeKeyValue(key)}>
							<X aria-hidden="true" class="size-3.5" />
						</button>
					</div>
				</div>
			{/each}
			<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={addKeyValue}>
				<Plus aria-hidden="true" class="size-3" />{m.properties_add_field()}
			</button>
		</div>
	{:else if property.kind === 'assignmentCollection'}
		<div class="grid gap-1.5 rounded-md border border-input p-1.5">
			{#each assignments as row, index (row.id)}
				<div class="grid gap-1 rounded border border-border/70 p-1.5">
					<div class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-1">
						<input aria-label={m.properties_field_name_aria({ label: property.label })} value={row.name} placeholder={m.properties_field_name_placeholder()} class="h-7 min-w-0 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => updateAssignment(index, { name: event.currentTarget.value })} />
						<button type="button" role="switch" aria-checked={assignmentIsExpression(row)} aria-label={m.properties_key_expression_mode_aria({ label: property.label, key: row.name || m.properties_field_fallback() })} class="shrink-0 rounded border border-border px-1 py-0.5 font-mono text-[0.625rem] text-muted-foreground transition-colors hover:bg-muted aria-checked:border-primary/40 aria-checked:bg-primary/10 aria-checked:text-primary" onclick={() => toggleAssignmentExpression(index)}>
							{assignmentIsExpression(row) ? m.properties_mode_expr() : m.properties_mode_fixed()}
						</button>
					</div>
					<div class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1.2fr)_auto] gap-1">
						<select aria-label={`${property.label} field type`} value={row.type} class="h-7 rounded border border-input bg-background px-1 text-[0.6875rem]" onchange={(event) => retypeAssignment(index, event.currentTarget.value as AssignmentType)}>
							{#each ASSIGNMENT_TYPES as type (type)}
								<option value={type}>{type}</option>
							{/each}
						</select>
						<span class="sr-only">{m.properties_value_label()}</span>
						{#if row.type === 'boolean' && !assignmentIsExpression(row)}
							<select aria-label={m.properties_field_value_aria({ label: property.label })} value={row.value === true ? 'true' : 'false'} class="h-7 rounded border border-input bg-background px-1 text-[0.6875rem]" onchange={(event) => updateAssignment(index, { value: event.currentTarget.value === 'true' })}>
								<option value="true">true</option>
								<option value="false">false</option>
							</select>
						{:else if typeof row.value === 'string' && (row.value as string).includes('\n')}
							<!-- Same rule as top-level strings: a multi-line row value
							     gets a textarea, never an input that would flatten it. -->
							<textarea aria-label={m.properties_field_value_aria({ label: property.label })} value={assignmentText(row)} rows={Math.min(8, Math.max(2, (row.value as string).split('\n').length))} class="min-w-0 rounded border border-input bg-background px-1.5 py-1 font-mono text-[0.6875rem]" oninput={(event) => assignmentInput(index, row, event.currentTarget.value)}></textarea>
						{:else}
							<input aria-label={m.properties_field_value_aria({ label: property.label })} value={assignmentText(row)} class="h-7 min-w-0 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => assignmentInput(index, row, event.currentTarget.value)} />
						{/if}
						<button type="button" aria-label={m.properties_remove_property({ label: row.name || m.properties_field_fallback() })} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => removeAssignment(index)}>
							<X aria-hidden="true" class="size-3.5" />
						</button>
					</div>
				</div>
			{/each}
			<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={addAssignment}>
				<Plus aria-hidden="true" class="size-3" />{m.properties_add_field()}
			</button>
		</div>
	{:else if property.kind === 'resourceLocator'}
		<!-- A narrow mode select beside the mode's own control, which is the
		     shape n8n uses: the mode is a property of the value, so it sits with
		     it rather than above it as a separate field. -->
		<div class="grid gap-1">
			<div class="flex items-start gap-1">
				<select aria-label={m.properties_locator_mode_aria({ label: property.label })} value={locator.mode} class="h-7 w-28 shrink-0 rounded-md border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => onChange(writeLocator(switchMode(property, locator, event.currentTarget.value)))}>
					{#each property.modes ?? [] as mode (mode.name)}
						<option value={mode.name}>{mode.label}</option>
					{/each}
					{#if !locatorMode}
						<option value={locator.mode}>{locator.mode || m.properties_unknown_locator_mode()}</option>
					{/if}
				</select>
				{#if !locatorMode}
					<!-- A mode this build does not know. Read-only and named,
					     rather than an empty control that reads as "this field
					     has no value". -->
					<p class="min-w-0 flex-1 rounded-md border border-dashed border-destructive/40 px-2 py-1.5 text-[0.6875rem] leading-4 text-destructive">
						{locatorDisplay ? m.properties_unknown_mode({ mode: locator.mode, value: locatorDisplay }) : m.properties_unknown_mode_empty({ mode: locator.mode })}
					</p>
				{:else if locatorMode.kind === 'options'}
					<select id={`property-${fieldID}`} value={displayValue(locator.value)} class="h-7 min-w-0 flex-1 rounded-md border border-input bg-background px-1.5 text-xs" onchange={(event) => onChange(writeLocator({ ...locator, value: event.currentTarget.value, cachedResultName: selectedLabel(event.currentTarget.value) }))}>
						<option value="">{locatorMode.placeholder || m.properties_choose()}</option>
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
					<span class="text-muted-foreground">{m.properties_mapping_column_mode()}</span>
					<select aria-label={m.properties_mapping_mode_aria({ label: property.label })} value={mapping.mappingMode} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => onChange(writeMapping({ ...mapping, mappingMode: event.currentTarget.value }, schemaColumns))}>
						<option value="defineBelow">{m.properties_map_manually()}</option>
						<option value="autoMapInputData">{m.properties_map_automatically()}</option>
					</select>
				</label>
			{/if}
			{#if matchableColumns(schemaColumns).length > 0}
				<div class="grid gap-1 text-[0.6875rem]">
					<span class="text-muted-foreground">{m.properties_columns_to_match()}</span>
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
						{schemaState.reason || m.properties_columns_not_loaded()}
					{:else}
						{@const matched = writableColumns(schemaColumns, mapping).map((column) => column.displayName).join(', ')}
						{matched ? m.properties_automap_summary({ columns: matched }) : m.properties_automap_summary_none()}
					{/if}
				</p>
			{:else if schemaColumns.length === 0}
				<p class="rounded border border-dashed border-input px-2 py-1.5 text-[0.6875rem] leading-4 text-muted-foreground">
					{schemaState.reason || m.properties_columns_not_loaded()}
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
								<option value="">{m.properties_choose()}</option>
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
							<span class="text-destructive">{m.properties_unknown_column_type({ type: column.type ?? '' })}</span>
						{/if}
					</label>
				{/each}
			{/if}
		</div>
	{:else if property.kind === 'conditions'}
		<div class="grid gap-1.5 rounded-md border border-input p-1.5">
			<div class="flex items-center gap-2">
				<span class="text-[0.6875rem] text-muted-foreground">{m.properties_conditions_match()}</span>
				<select aria-label={m.properties_combinator_aria({ label: property.label })} value={conditionFilter.combinator} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => writeFilter({ combinator: event.currentTarget.value === 'or' ? 'or' : 'and' })}>
					<option value="and">{m.properties_all_and()}</option>
					<option value="or">{m.properties_any_or()}</option>
				</select>
			</div>
			{#each conditionFilter.conditions as row, index (index)}
				<div class="grid gap-1 rounded border border-border/70 p-1.5">
					<div class="flex items-center gap-1">
						<input aria-label={m.properties_condition_left_aria({ label: property.label, index: index + 1 })} value={String(row.leftValue ?? '')} placeholder={'{{ $json.field }}'} class="h-7 min-w-0 flex-1 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => writeFilter({ conditions: updateCondition(conditionFilter.conditions, index, { leftValue: event.currentTarget.value }) })} />
						<!-- Order is meaningful: the rules read top to bottom. -->
						<button type="button" aria-label={m.properties_move_condition_up({ index: index + 1 })} disabled={index === 0} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted disabled:opacity-40" onclick={() => writeFilter({ conditions: moveCondition(conditionFilter.conditions, index, -1) })}>
							<ChevronUp aria-hidden="true" class="size-3.5" />
						</button>
						<button type="button" aria-label={m.properties_move_condition_down({ index: index + 1 })} disabled={index === conditionFilter.conditions.length - 1} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted disabled:opacity-40" onclick={() => writeFilter({ conditions: moveCondition(conditionFilter.conditions, index, 1) })}>
							<ChevronDown aria-hidden="true" class="size-3.5" />
						</button>
						<button type="button" aria-label={m.properties_remove_condition({ index: index + 1 })} class="grid size-7 place-items-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive" onclick={() => writeFilter({ conditions: removeCondition(conditionFilter.conditions, index) })}>
							<X aria-hidden="true" class="size-3.5" />
						</button>
					</div>
					<div class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-1">
						<select aria-label={m.properties_condition_type_aria({ label: property.label, index: index + 1 })} value={row.operator.type} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => writeFilter({ conditions: updateCondition(conditionFilter.conditions, index, { operator: { type: event.currentTarget.value as ConditionValueType, operation: row.operator.operation } }) })}>
							<option value="string">{m.properties_type_string()}</option>
							<option value="number">{m.properties_type_number()}</option>
							<option value="boolean">{m.properties_type_boolean()}</option>
							<option value="dateTime">{m.properties_type_datetime()}</option>
							<option value="array">{m.properties_type_array()}</option>
							<option value="object">{m.properties_type_object()}</option>
						</select>
						<select aria-label={m.properties_condition_operator_aria({ label: property.label, index: index + 1 })} value={row.operator.operation} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => {
							const operation = event.currentTarget.value as ConditionOperator;
							const coerced = typeForOperation(operation);
							writeFilter({ conditions: updateCondition(conditionFilter.conditions, index, { operator: { type: coerced ?? row.operator.type, operation } }) });
						}}>
							{#each operators as [operation, label] (operation)}
								<option value={operation}>{label()}</option>
							{/each}
						</select>
					</div>
					{#if !VALUELESS_OPERATORS.includes(row.operator.operation)}
						<input aria-label={m.properties_condition_right_aria({ label: property.label, index: index + 1 })} value={String(row.rightValue ?? '')} class="h-7 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => writeFilter({ conditions: updateCondition(conditionFilter.conditions, index, { rightValue: event.currentTarget.value }) })} />
					{/if}
				</div>
			{/each}
			<button type="button" class="inline-flex h-6 items-center gap-1 justify-self-start rounded border border-border px-1.5 text-[0.6875rem] transition-colors hover:bg-muted" onclick={() => writeFilter({ conditions: [...conditionFilter.conditions, newCondition()] })}>
				<Plus aria-hidden="true" class="size-3" />{m.properties_add_condition()}
			</button>
		</div>
{:else if codeEditor && typeOptions.editorLanguage}
	{#key `${ownerKey}\u0000${property.key}`}
		<CodeEditor value={stringValue} language={typeOptions.editorLanguage} label={property.label} rows={typeOptions.rows ?? 12} nodeNames={upstreamNodeNames} onChange={(text) => onChange(text)} />
	{/key}
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
				{m.properties_unknown_field_type({ kind: property.kind })}
			</p>
			<pre class="overflow-x-auto rounded bg-muted/40 px-1.5 py-1 font-mono text-[0.6875rem]">{stringValue}</pre>
		</div>
	{/if}
</div>
