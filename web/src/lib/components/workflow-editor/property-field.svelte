<script lang="ts">
	import type { PropertyDefinition } from '$lib/api/generated/models';
	import { renameKeyValue } from '$lib/workflow-editor/key-value';

	let { property, value, onChange }: { property: PropertyDefinition; value: unknown; onChange: (value: unknown) => void } = $props();

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

<div class="grid gap-2">
	<label class="text-sm font-medium" for={`property-${property.key}`}>{property.label}{#if property.required}<span aria-hidden="true" class="text-destructive"> *</span>{/if}</label>
	{#if property.description}<p class="-mt-1 text-xs leading-5 text-muted-foreground">{property.description}</p>{/if}

	{#if property.kind === 'boolean'}
		<label class="flex min-h-10 items-center gap-2 rounded-lg border border-input px-3 text-sm">
			<input id={`property-${property.key}`} type="checkbox" checked={Boolean(value)} onchange={(event) => onChange(event.currentTarget.checked)} />
			<span>{Boolean(value) ? 'Enabled' : 'Disabled'}</span>
		</label>
	{:else if property.kind === 'number'}
		<input id={`property-${property.key}`} type="number" value={stringValue} class="h-10 rounded-lg border border-input bg-background px-3 text-sm" oninput={(event) => onChange(event.currentTarget.value === '' ? undefined : Number(event.currentTarget.value))} />
	{:else if property.kind === 'select'}
		<select id={`property-${property.key}`} value={stringValue} class="h-10 rounded-lg border border-input bg-background px-3 text-sm" onchange={(event) => onChange(event.currentTarget.value)}>
			{#each property.options ?? [] as option (option.value)}
				<option value={option.value}>{option.label}</option>
			{/each}
		</select>
	{:else if property.kind === 'keyValue'}
		<div class="grid gap-2 rounded-lg border border-input p-3">
			{#each Object.entries(objectValue) as [key, item] (key)}
				<div class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
					<input aria-label={`${property.label} field name`} value={key} class="h-9 min-w-0 rounded-md border border-input bg-background px-2 text-sm" onchange={(event) => renameKey(key, event.currentTarget.value)} />
					<input aria-label={`${property.label} field value`} value={displayValue(item)} class="h-9 min-w-0 rounded-md border border-input bg-background px-2 text-sm" oninput={(event) => updateKeyValue(key, event.currentTarget.value)} />
					<button type="button" aria-label={`Remove ${key || 'assignment'}`} class="rounded-md px-2 text-sm text-destructive hover:bg-destructive/10" onclick={() => removeKeyValue(key)}>Remove</button>
				</div>
			{/each}
			<button type="button" class="justify-self-start rounded-md border border-border px-2.5 py-1.5 text-sm hover:bg-muted" onclick={addKeyValue}>Add field</button>
		</div>
	{:else if property.kind === 'conditions'}
		<div class="grid gap-2 rounded-lg border border-input p-3">
			<input id={`property-${property.key}`} aria-label={`${property.label} field`} value={conditionValue('field')} placeholder="Field path, e.g. customer.tier" class="h-9 rounded-md border border-input bg-background px-2 text-sm" oninput={(event) => updateCondition({ field: event.currentTarget.value })} />
			<select aria-label={`${property.label} operator`} value={conditionValue('operator')} class="h-9 rounded-md border border-input bg-background px-2 text-sm" onchange={(event) => updateCondition({ operator: event.currentTarget.value })}>
				<option value="equals">equals</option>
				<option value="notEquals">does not equal</option>
				<option value="exists">exists</option>
				<option value="notExists">does not exist</option>
			</select>
			{#if !['exists', 'notExists'].includes(conditionValue('operator'))}
				<input aria-label={`${property.label} value`} value={conditionValue('value')} class="h-9 rounded-md border border-input bg-background px-2 text-sm" oninput={(event) => updateCondition({ value: parseValue(event.currentTarget.value) })} />
			{/if}
		</div>
	{:else}
		<input id={`property-${property.key}`} value={stringValue} class="h-10 rounded-lg border border-input bg-background px-3 text-sm" oninput={(event) => onChange(event.currentTarget.value)} />
	{/if}
</div>
