import { CompletionContext, type CompletionResult } from '@codemirror/autocomplete';
import { insertNewlineAndIndent } from '@codemirror/commands';
import { EditorSelection, EditorState, type Transaction } from '@codemirror/state';
import { describe, expect, it } from 'vitest';

import { codeEditorExtensions, codeNodeCompletionSource, CODE_NODE_GLOBALS, nodeReferenceCompletions, type CodeLanguage } from './code-editor';

function state(doc: string, language: CodeLanguage, cursor = doc.length): EditorState {
	return EditorState.create({
		doc,
		selection: EditorSelection.cursor(cursor),
		extensions: codeEditorExtensions({ language, label: 'Code', onChange() {} })
	});
}

/** Presses Enter the way the keymap does, and returns the resulting text. */
function enter(start: EditorState): string {
	let next = start;
	insertNewlineAndIndent({ state: start, dispatch: (transaction: Transaction) => (next = transaction.state) });
	return next.doc.toString();
}

async function complete(doc: string, names: string[] = [], explicit = false): Promise<CompletionResult | null> {
	const source = codeNodeCompletionSource(names);
	const current = state(doc, 'javaScript');
	return source(new CompletionContext(current, doc.length, explicit));
}

describe('indentation', () => {
	it('Enter after an open brace indents JavaScript by two spaces', () => {
		expect(enter(state('for (const item of items) {', 'javaScript'))).toBe('for (const item of items) {\n  ');
	});

	it('Enter after an open brace indents Go with a tab, as gofmt writes it', () => {
		expect(enter(state('for _, it := range items {', 'go'))).toBe('for _, it := range items {\n\t');
	});

	it('a multi-line Go body stays multi-line in the editor', () => {
		const body = 'out := []Item{}\nfor _, it := range items {\n\tout = append(out, it)\n}\nreturn out, nil';
		expect(state(body, 'go').doc.lines).toBe(5);
		expect(state(body, 'go').doc.toString()).toBe(body);
	});
});

describe('Code node completions', () => {
	it('complete the runtime roots as the user types them', async () => {
		const result = await complete('const first = $in');
		expect(result?.from).toBe('const first = '.length);
		expect(result?.options.map((option) => option.label)).toContain('$input.first()');
	});

	it('offer the nodes upstream as $(\'Name\')', async () => {
		const result = await complete('$(', ['Fetch rows', "Bob's node"]);
		const labels = result?.options.map((option) => option.label) ?? [];
		expect(labels).toContain("$('Fetch rows')");
		expect(labels).toContain("$('Bob\\'s node')");
	});

	it('offer nothing inside a string, where $json is only text', async () => {
		expect(await complete("const message = 'see $js")).toBeNull();
	});

	it('never offer a root the runtime refuses', () => {
		const labels = CODE_NODE_GLOBALS.map((option) => option.label);
		for (const refused of ['$prevNode', '$secrets', '$jmespath', '$evaluateExpression']) {
			expect(labels).not.toContain(refused);
		}
	});

	it('escape a quote and a backslash in a node name', () => {
		expect(nodeReferenceCompletions(['a\\b']).map((option) => option.label)).toEqual(["$('a\\\\b')"]);
	});
});

describe('read-only', () => {
	it('a read-only editor refuses edits, and an ordinary one takes them', () => {
		const make = (readOnly: boolean) =>
			EditorState.create({ doc: 'a', extensions: codeEditorExtensions({ language: 'javaScript', label: 'Code', readOnly, onChange() {} }) });
		expect(make(false).readOnly).toBe(false);
		expect(make(true).readOnly).toBe(true);
	});
});

describe('node name completion inside $(', () => {
	it('keeps offering node names while the name is typed inside the quotes', async () => {
		const result = await complete("const rows = $('Fe", ['Fetch rows']);
		expect(result?.from).toBe('const rows = '.length);
		expect(result?.options.map((option) => option.label)).toEqual(["$('Fetch rows')"]);
	});
});
