<script lang="ts">
	import { EditorView } from '@codemirror/view';
	import { onMount } from 'svelte';

	import { codeEditorExtensions, syncFromProp, type CodeLanguage } from '$lib/workflow-editor/code-editor';

	/**
	 * A source editor for one code parameter.
	 *
	 * The view owns the text while the user types, so the prop is written back
	 * into it only when it differs from what the view holds — an undo, a paste
	 * of the whole node, a switch to another node. Writing it on every change
	 * would reset the cursor and the undo history on each keystroke.
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

	onMount(() => {
		view = new EditorView({
			doc: value,
			parent: host,
			extensions: codeEditorExtensions({
				language,
				readOnly,
				label,
				id,
				nodeNames,
				onChange: (text) => onChange(text)
			})
		});
		return () => view?.destroy();
	});

	$effect(() => {
		const incoming = value;
		if (!view) return;
		const current = view.state.doc.toString();
		if (incoming !== current) {
			view.dispatch({ changes: { from: 0, to: current.length, insert: incoming }, annotations: syncFromProp.of(true) });
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
