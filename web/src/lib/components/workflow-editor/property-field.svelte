<script lang="ts">
	import Plus from '@lucide/svelte/icons/plus';
	import X from '@lucide/svelte/icons/x';

	import type { PropertyDefinition } from '$lib/api/generated/models';
	import { renameKeyValue } from '$lib/workflow-editor/key-value';
	import { expressionRoots, unknownExpressionRoot } from '$lib/workflow-editor/expression-grammar';
	import { asExpression, asFixed, expressionTemplate, isExpression } from '$lib/workflow-editor/parameter';

	let { property, value, onChange }: { property: PropertyDefinition; value: unknown; onChange: (value: unknown) => void } = $props();

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
		'keyValue', 'conditions'
	]);

	const typeOptions = $derived(property.typeOptions ?? {});
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
	const conditions = $derived(Array.isArray(value) ? value : []);

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

	function updateCondition(patch: Record<string, unknown>) {
		const current = isObject(conditions[0]) ? conditions[0] : { field: '', operator: 'equals', value: '' };
		const next = { ...current, ...patch };
		if (next.operator === 'exists' || next.operator === 'notExists') delete next.value;
		onChange([next]);
	}

	function conditionValue(key: string): string {
		const condition = conditions[0];
		if (!isObject(condition)) return key === 'operator' ? 'equals' : '';
		if (key === 'value') return displayValue(condition.value);
		return typeof condition[key] === 'string' ? condition[key] : key === 'operator' ? 'equals' : '';
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
	{:else if property.kind === 'collection' || property.kind === 'fixedCollection'}
		<!-- Nested groups are rendered by the panel that owns them; this field
		     shows what is configured without pretending to edit a shape it does
		     not yet have an editor for. -->
		<div class="grid gap-1 rounded-md border border-dashed border-input p-1.5 text-[0.6875rem] text-muted-foreground">
			<span>{(property.fields ?? []).length || (property.groups ?? []).length} nested field(s)</span>
			<textarea aria-label={`${property.label} value`} value={stringValue} spellcheck="false" rows={3} class="rounded border border-input bg-background px-1.5 py-1 font-mono text-[0.6875rem]" oninput={(event) => onChange(event.currentTarget.value)}></textarea>
		</div>
	{:else if property.kind === 'options'}
		<select id={`property-${property.key}`} value={stringValue} class="h-7 rounded-md border border-input bg-background px-1.5 text-xs" onchange={(event) => onChange(event.currentTarget.value)}>
			{#each property.options ?? [] as option (option.value)}
				<option value={option.value}>{option.label}</option>
			{/each}
		</select>
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
	{:else if property.kind === 'conditions'}
		<div class="grid gap-1.5 rounded-md border border-input p-1.5">
			<input id={`property-${property.key}`} aria-label={`${property.label} field`} value={conditionValue('field')} placeholder="customer.tier" class="h-7 rounded border border-input bg-background px-1.5 font-mono text-[0.6875rem]" oninput={(event) => updateCondition({ field: event.currentTarget.value })} />
			<select aria-label={`${property.label} operator`} value={conditionValue('operator')} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" onchange={(event) => updateCondition({ operator: event.currentTarget.value })}>
				<option value="equals">equals</option>
				<option value="notEquals">does not equal</option>
				<option value="exists">exists</option>
				<option value="notExists">does not exist</option>
			</select>
			{#if !['exists', 'notExists'].includes(conditionValue('operator'))}
				<input aria-label={`${property.label} value`} value={conditionValue('value')} class="h-7 rounded border border-input bg-background px-1.5 text-[0.6875rem]" oninput={(event) => updateCondition({ value: parseValue(event.currentTarget.value) })} />
			{/if}
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
