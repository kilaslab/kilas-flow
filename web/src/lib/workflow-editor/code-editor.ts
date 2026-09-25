import {
	autocompletion,
	closeBrackets,
	closeBracketsKeymap,
	completeFromList,
	completionKeymap,
	ifNotIn,
	type Completion,
	type CompletionContext,
	type CompletionResult,
	type CompletionSource
} from '@codemirror/autocomplete';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { go, goLanguage } from '@codemirror/lang-go';
import { javascript, javascriptLanguage } from '@codemirror/lang-javascript';
import { json } from '@codemirror/lang-json';
import { python } from '@codemirror/lang-python';
import { bracketMatching, HighlightStyle, indentOnInput, indentUnit, syntaxHighlighting, type LanguageSupport } from '@codemirror/language';
import { Annotation, EditorState, type Extension } from '@codemirror/state';
import {
	drawSelection,
	EditorView,
	highlightActiveLine,
	highlightActiveLineGutter,
	highlightSpecialChars,
	keymap,
	lineNumbers,
	placeholder as placeholderText
} from '@codemirror/view';
import { tags } from '@lezer/highlight';

import type { TypeOptionsEditorLanguage } from '$lib/api/generated/models';

/**
 * The source editor a code parameter renders as (`typeOptions.editor: "code"`).
 *
 * The extensions are assembled here rather than in the component so the
 * choices that decide behaviour — what a language highlights, what completes,
 * which keys indent — can be tested without a DOM, and so the component is
 * left with the one thing only it can do: keep the view and the prop in step.
 */
export type CodeLanguage = TypeOptionsEditorLanguage;

export type CodeEditorOptions = {
	language: CodeLanguage;
	readOnly?: boolean;
	/** Accessible name of the editing surface, which is a contenteditable rather than a labelled input. */
	label: string;
	/** The id a `<label for>` points at, carried by the editing surface. */
	id?: string;
	/** Upstream node names, offered as `$('Name')` in JavaScript. */
	nodeNames?: readonly string[];
	placeholder?: string;
	onChange: (text: string) => void;
};

/**
 * What a Code node's JavaScript can reach besides its own variables.
 *
 * Only what the runtime actually answers: `$prevNode`, `$secrets`,
 * `$jmespath` and `$evaluateExpression` exist in n8n but are refused here with
 * a named error, and offering them would complete a line that cannot run.
 * The per-mode roots are listed for both modes, since which one applies is
 * another field of the same node.
 */
export const CODE_NODE_GLOBALS: readonly Completion[] = [
	{ label: 'items', type: 'variable', detail: 'all items', info: 'The input items, in "Run once for all items" mode.' },
	{ label: 'item', type: 'variable', detail: 'current item', info: 'The current input item, in "Run once for each item" mode.' },
	{ label: '$input', type: 'variable', detail: 'input', info: 'The input: .all(), .first(), .last() and, per item, .item.' },
	{ label: '$input.all()', type: 'function', detail: 'every input item' },
	{ label: '$input.first()', type: 'function', detail: 'first input item' },
	{ label: '$input.last()', type: 'function', detail: 'last input item' },
	{ label: '$input.item', type: 'property', detail: 'current input item' },
	{ label: '$json', type: 'variable', detail: 'item JSON', info: "The current item's JSON." },
	{ label: '$binary', type: 'variable', detail: 'item binary', info: "The current item's binary attachments." },
	{ label: '$itemIndex', type: 'variable', detail: 'item index' },
	{ label: '$node', type: 'variable', detail: 'earlier nodes by name', info: "$node['Name'].json reads a node that ran earlier." },
	{ label: '$items', type: 'function', detail: '(name, output?, run?)', info: "n8n's older way to read a node's items." },
	{ label: '$workflow', type: 'variable', detail: 'id, name, active' },
	{ label: '$execution', type: 'variable', detail: 'id, mode, resumeUrl' },
	{ label: '$env', type: 'variable', detail: 'allowlisted environment' },
	{ label: '$vars', type: 'variable', detail: 'variables' },
	{ label: '$runIndex', type: 'variable', detail: 'run number of this node' },
	{ label: '$nodeVersion', type: 'variable', detail: 'type version of this node' },
	{ label: '$now', type: 'variable', detail: 'Luxon DateTime', info: "Now, in the workflow's time zone." },
	{ label: '$today', type: 'variable', detail: 'Luxon DateTime', info: "Midnight today, in the workflow's time zone." },
	{ label: 'DateTime', type: 'class', detail: 'Luxon' },
	{ label: 'Duration', type: 'class', detail: 'Luxon' },
	{ label: 'Interval', type: 'class', detail: 'Luxon' },
	{ label: '$getWorkflowStaticData', type: 'function', detail: "('global' | 'node')" },
	{ label: 'this.helpers.httpRequest', type: 'function', detail: '(options)', info: "Sent by the server under its egress policy." },
	{ label: 'require', type: 'function', detail: 'crypto, lodash, luxon, util, buffer, url' },
	{ label: 'console.log', type: 'function', detail: 'kept with the node run' }
];

/** `$('Name')` for each node the code can read, the form n8n's own examples use. */
export function nodeReferenceCompletions(names: readonly string[]): Completion[] {
	return names.map((name) => ({
		label: `$('${name.replaceAll('\\', '\\\\').replaceAll("'", "\\'")}')`,
		type: 'function',
		detail: 'node output',
		boost: 1
	}));
}

/**
 * Completion for the Code-node roots.
 *
 * A root starts with `$` or a plain identifier and may carry one member access
 * (`$input.fi`), so the match runs back over word characters, `$` and dots.
 * Inside a string or a comment it offers nothing: `'$json'` in a message is
 * text. The one string that does complete is a node name being typed into
 * `$('…`, which is matched first for exactly that reason.
 */
export function codeNodeCompletionSource(nodeNames: readonly string[] = []): CompletionSource {
	const references = nodeReferenceCompletions(nodeNames);
	const options = [...CODE_NODE_GLOBALS, ...references];
	const roots = ifNotIn(NOT_CODE, (context: CompletionContext): CompletionResult | null => {
		const word = context.matchBefore(/[\w$.]*/);
		if (!word || (word.from === word.to && !context.explicit)) return null;
		return { from: word.from, options, validFor: /^[\w$.]*$/ };
	});
	return (context: CompletionContext) => {
		const call = references.length > 0 ? context.matchBefore(/\$\((['"][^'"()]*)?/) : null;
		if (call) return { from: call.from, options: references, validFor: /^\$\((['"][^'"()]*)?$/ };
		return roots(context);
	};
}

/** Syntax nodes whose text is not code, where no identifier should complete. */
const NOT_CODE = ['String', 'TemplateString', 'LineComment', 'BlockComment'];

/** The language support for one editor language. */
export function languageSupport(language: CodeLanguage, nodeNames: readonly string[] = []): LanguageSupport | Extension[] {
	switch (language) {
		case 'javaScript':
			return [javascript(), javascriptLanguage.data.of({ autocomplete: codeNodeCompletionSource(nodeNames) })];
		case 'go':
			// The body of `func run(items []Item) ([]Item, error)`: its two
			// names are what nearly every line refers to.
			return [
				go(),
				goLanguage.data.of({
					autocomplete: completeFromList([
						{ label: 'items', type: 'variable', detail: '[]Item' },
						{ label: 'Item', type: 'type', detail: 'one item' },
						{ label: 'return items, nil', type: 'keyword', detail: 'pass the items through' }
					])
				})
			];
		case 'python':
			return python();
		case 'json':
			return json();
	}
}

/**
 * Syntax colours read from theme tokens, so the editor follows the light and
 * dark palettes without a second stylesheet: the token values are what change
 * between them.
 */
export const codeHighlightStyle = HighlightStyle.define([
	{ tag: [tags.keyword, tags.controlKeyword, tags.moduleKeyword, tags.operatorKeyword, tags.definitionKeyword], color: 'var(--code-keyword)' },
	{ tag: [tags.string, tags.special(tags.string), tags.regexp], color: 'var(--code-string)' },
	{ tag: [tags.number, tags.bool, tags.null, tags.atom], color: 'var(--code-number)' },
	{ tag: [tags.comment, tags.lineComment, tags.blockComment], color: 'var(--code-comment)', fontStyle: 'italic' },
	{ tag: [tags.function(tags.variableName), tags.function(tags.propertyName)], color: 'var(--code-function)' },
	{ tag: [tags.typeName, tags.className, tags.namespace], color: 'var(--code-type)' },
	{ tag: [tags.propertyName, tags.attributeName], color: 'var(--code-property)' },
	{ tag: [tags.special(tags.variableName), tags.self], color: 'var(--code-keyword)' },
	{ tag: tags.invalid, color: 'var(--destructive)' }
]);

/** Chrome read from the same tokens as every other control. */
export const codeEditorTheme = EditorView.theme({
	'&': {
		fontSize: '0.75rem',
		backgroundColor: 'var(--background)',
		color: 'var(--foreground)',
		height: '100%'
	},
	'&.cm-focused': { outline: 'none' },
	'.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.25rem', overflow: 'auto' },
	'.cm-content': { caretColor: 'var(--foreground)', padding: '0.375rem 0' },
	'.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--foreground)' },
	'&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, ::selection': {
		backgroundColor: 'color-mix(in oklch, var(--primary) 28%, transparent)'
	},
	'.cm-gutters': {
		backgroundColor: 'var(--muted)',
		color: 'var(--muted-foreground)',
		border: 'none',
		borderRight: '1px solid var(--border)'
	},
	'.cm-activeLine': { backgroundColor: 'color-mix(in oklch, var(--muted) 55%, transparent)' },
	'.cm-activeLineGutter': { backgroundColor: 'var(--accent)', color: 'var(--accent-foreground)' },
	'.cm-matchingBracket, &.cm-focused .cm-matchingBracket': {
		backgroundColor: 'color-mix(in oklch, var(--primary) 22%, transparent)',
		outline: '1px solid color-mix(in oklch, var(--primary) 50%, transparent)'
	},
	'.cm-placeholder': { color: 'var(--muted-foreground)' },
	'.cm-tooltip': {
		backgroundColor: 'var(--popover)',
		color: 'var(--popover-foreground)',
		border: '1px solid var(--border)',
		borderRadius: 'calc(var(--radius) - 4px)'
	},
	'.cm-tooltip-autocomplete > ul': { fontFamily: 'var(--font-mono)', fontSize: '0.6875rem' },
	'.cm-tooltip-autocomplete > ul > li[aria-selected]': {
		backgroundColor: 'var(--accent)',
		color: 'var(--accent-foreground)'
	},
	'.cm-completionDetail': { color: 'var(--muted-foreground)', fontStyle: 'normal', marginLeft: '0.5rem' }
});

/**
 * Marks a transaction that brings the view in line with its prop. Such a
 * change is the value the host already holds, so it is not reported back as
 * an edit — which would put a no-op on the host's undo stack.
 */
export const syncFromProp = Annotation.define<boolean>();

/**
 * Every extension of one editor.
 *
 * Tab indents, because in a code editor that is what Tab is for; the view's
 * tab-focus mode (Escape, then Tab — or Ctrl-M) still moves focus out, so the
 * field is not a keyboard trap. Go is indented with a tab character, as gofmt
 * writes it; the others with two spaces, as n8n's own Code node does.
 */
export function codeEditorExtensions(options: CodeEditorOptions): Extension[] {
	const attributes: Record<string, string> = { 'aria-label': options.label, 'aria-multiline': 'true' };
	if (options.id) attributes.id = options.id;
	return [
		lineNumbers(),
		highlightActiveLineGutter(),
		highlightSpecialChars(),
		history(),
		drawSelection(),
		indentOnInput(),
		bracketMatching(),
		closeBrackets(),
		autocompletion({ icons: false }),
		highlightActiveLine(),
		indentUnit.of(options.language === 'go' ? '\t' : '  '),
		EditorState.tabSize.of(options.language === 'go' ? 4 : 2),
		keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, ...completionKeymap, indentWithTab]),
		syntaxHighlighting(codeHighlightStyle),
		codeEditorTheme,
		languageSupport(options.language, options.nodeNames ?? []),
		EditorView.contentAttributes.of(attributes),
		EditorState.readOnly.of(options.readOnly ?? false),
		EditorView.editable.of(!(options.readOnly ?? false)),
		options.placeholder ? placeholderText(options.placeholder) : [],
		EditorView.updateListener.of((update) => {
			if (!update.docChanged) return;
			if (update.transactions.some((transaction) => transaction.annotation(syncFromProp))) return;
			options.onChange(update.state.doc.toString());
		})
	];
}
