<script lang="ts">
	import { Compartment, Transaction } from '@codemirror/state';
	import { EditorView } from '@codemirror/view';
	import { onMount } from 'svelte';

	import { codeEditorExtensions, syncFromProp, type CodeLanguage } from '$lib/workflow-editor/code-editor';

	/**
	 * A source editor for one code parameter.
	 *
	 * The view owns the text while the user types, so the prop is written back
	 * into it only when it differs from what the view holds — an undo of the
	 * whole canvas, a restored revision. Writing it on every change would reset
	 * the cursor on each keystroke. That write is kept out of the editor's own
	 * undo history: undoing it would hand the host a value the user never typed.
	 *
	 * The host remounts this component when the field starts editing another
	 * node (see `ownerKey` on the property field), so one node's undo history
	 * can never be replayed into another's parameter.
	 */
	let {
		value,
		language,
		label,
		id,
		rows = 12,
		readOnly = false,
		nodeNames = [],
		onChange
	}: {
		value: string;
		language: CodeLanguage;
		label: string;
		id?: string;
		/** Visible height in lines; the frame can be dragged taller. */
		rows?: number;
		readOnly?: boolean;
		nodeNames?: string[];
		onChange: (text: string) => void;
	} = $props();

	let host: HTMLDivElement;
	let view: EditorView | undefined;
	const configuration = new Compartment();

	function extensions() {
		return codeEditorExtensions({ language, readOnly, label, id, nodeNames, onChange: (text) => onChange(text) });
	}

	/**
	 * What the extensions are built from, as one comparable value. The host
	 * hands a fresh array of node names on every edit anywhere on the canvas;
	 * rebuilding on identity would close an open completion list mid-word.
	 */
	const configurationKey = $derived(JSON.stringify([language, readOnly, label, id ?? '', nodeNames]));
	let appliedKey = '';

	onMount(() => {
		appliedKey = configurationKey;
		view = new EditorView({ doc: value, parent: host, extensions: configuration.of(extensions()) });
		return () => view?.destroy();
	});

	$effect(() => {
		const key = configurationKey;
		if (!view || key === appliedKey) return;
		appliedKey = key;
		view.dispatch({ effects: configuration.reconfigure(extensions()) });
	});

	$effect(() => {
		const incoming = value;
		if (!view) return;
		const current = view.state.doc.toString();
		if (incoming !== current) {
			view.dispatch({
				changes: { from: 0, to: current.length, insert: incoming },
				annotations: [syncFromProp.of(true), Transaction.addToHistory.of(false)]
			});
		}
	});
</script>

<!-- 1.25rem per line plus the content padding. resize-y lets a long body be
     read without opening anything else; overflow-hidden keeps the scroller
     inside the frame the user sized. -->
<div
	bind:this={host}
	data-code-editor={language}
	class="min-h-24 resize-y overflow-hidden rounded-md border border-input bg-background focus-within:border-ring focus-within:ring-1 focus-within:ring-ring/40"
	style:height={`calc(${Math.max(rows, 4)} * 1.25rem + 0.75rem + 2px)`}
></div>
