<script lang="ts">
	import { tick } from 'svelte';

	import * as m from '$lib/paraglide/messages.js';
	import { MAX_WORKFLOW_NAME_LENGTH, type RenameProblem } from '$lib/workflow-editor/workflow-settings';

	/**
	 * The workflow's name in the editor header, editable in place.
	 *
	 * Committing hands the new name to the editor, which writes it into the
	 * draft, so a rename is undoable and saves with the rest of the document
	 * rather than through a call of its own. A canvas that cannot be written
	 * (a read-only embed session, a revision preview) passes `editable={false}`
	 * and gets the plain name with no way to start editing.
	 */
	let {
		name,
		editable,
		onRename
	}: {
		name: string;
		editable: boolean;
		/** Applies the name to the draft; answers what is wrong with it, or null once it took. */
		onRename: (name: string) => RenameProblem | null;
	} = $props();

	let editing = $state(false);
	let value = $state('');
	let problem = $state<RenameProblem | null>(null);
	let input = $state<HTMLInputElement | null>(null);

	const problemText = $derived(
		problem === 'required' ? m.editor_title_error_required() : problem === 'too_long' ? m.editor_title_error_too_long() : ''
	);

	async function start() {
		value = name;
		problem = null;
		editing = true;
		await tick();
		input?.focus();
		input?.select();
	}

	function commit() {
		const result = onRename(value);
		problem = result;
		if (result === null) editing = false;
	}

	function cancel() {
		problem = null;
		editing = false;
	}

	function onkeydown(event: KeyboardEvent) {
		if (event.key === 'Enter') {
			event.preventDefault();
			commit();
		} else if (event.key === 'Escape') {
			event.preventDefault();
			event.stopPropagation();
			cancel();
		}
	}

	// Leaving the field keeps a name that is valid and drops one that is not,
	// instead of trapping focus in a field the user has walked away from.
	function onblur() {
		if (!editing) return;
		if (onRename(value) === null) editing = false;
		else cancel();
	}
</script>

{#if editable && editing}
	<input
		bind:this={input}
		bind:value
		{onkeydown}
		{onblur}
		type="text"
		maxlength={MAX_WORKFLOW_NAME_LENGTH}
		aria-label={m.editor_title_input_aria()}
		aria-invalid={problem !== null}
		data-testid="workflow-title-input"
		class="h-6 w-32 min-w-0 shrink rounded-md border border-input bg-background px-2 text-xs font-medium focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring sm:w-44"
	/>
	{#if problem}<span role="alert" class="shrink-0 whitespace-nowrap text-xs text-destructive">{problemText}</span>{/if}
{:else if editable}
	<button
		type="button"
		title={m.editor_title_rename()}
		aria-label={`${m.editor_title_rename()}: ${name}`}
		data-testid="workflow-title"
		class="min-w-0 max-w-32 flex-1 truncate rounded-md px-1 text-left text-xs font-medium transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring sm:max-w-44"
		onclick={() => void start()}
	>
		{name}
	</button>
{:else}
	<p class="min-w-0 max-w-32 flex-1 truncate text-xs font-medium sm:max-w-44" data-testid="workflow-title">{name}</p>
{/if}
