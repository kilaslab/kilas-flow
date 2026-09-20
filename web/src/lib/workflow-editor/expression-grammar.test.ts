import { describe, expect, it } from 'vitest';

import {
	setExpressionGrammar,
	unknownExpressionRoot,
	validateExpressionShape
} from './expression-grammar';

describe('unknownExpressionRoot', () => {
	it('says nothing until the server grammar has loaded', () => {
		setExpressionGrammar(undefined);
		expect(unknownExpressionRoot('{{ $whatever.x }}')).toBe(null);
	});

	it('flags a root the server did not list', () => {
		setExpressionGrammar({ roots: ['$json', '$input', '$('], functions: [] });
		expect(unknownExpressionRoot('{{ $json.a }}')).toBe(null);
		expect(unknownExpressionRoot("{{ $('Node').item }}")).toBe(null);
		expect(unknownExpressionRoot('{{ $vars.k }}')).toBe('$vars');
	});
});

describe('validateExpressionShape', () => {
	it('flags unbalanced braces before anything else', () => {
		setExpressionGrammar({ roots: ['$json'], functions: ['$json'] });
		expect(validateExpressionShape('{{ $json.a ')).toContain('Unbalanced');
	});

	it('flags an unknown root using the served list', () => {
		setExpressionGrammar({ roots: ['$json'], functions: [] });
		expect(validateExpressionShape('{{ $foo.a }}')).toContain('$foo');
	});

	it('flags an unknown method and an unknown free call', () => {
		setExpressionGrammar({ roots: ['$json'], functions: ['toUpperCase'] });
		// A method the served list does not carry: the real list is the engine's
		// own allowlist, so this checks the rule rather than a hardcoded surface.
		expect(validateExpressionShape("{{ $json.a.toFormat('x') }}")).toContain('toFormat()');
		// Node-style free call the engine does not carry.
		expect(validateExpressionShape("{{ require('fs') }}")).toContain("require()");
	});

	it('accepts a carried method and a plain field access', () => {
		setExpressionGrammar({ roots: ['$json'], functions: ['toUpperCase'] });
		expect(validateExpressionShape('{{ $json.a.toUpperCase() }}')).toBe(null);
		setExpressionGrammar({ roots: ['$json'], functions: [] });
		expect(validateExpressionShape('{{ $json.a }}')).toBe(null);
	});
});
