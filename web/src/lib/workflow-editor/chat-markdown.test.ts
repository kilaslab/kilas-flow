import { describe, expect, it } from 'vitest';

import { parseChatMarkdown } from './chat-markdown';

describe('parseChatMarkdown', () => {
	it('keeps plain text as one paragraph', () => {
		expect(parseChatMarkdown('Hello there')).toEqual([{ kind: 'paragraph', inline: [{ kind: 'text', text: 'Hello there' }] }]);
	});

	it('keeps a single newline as a line break instead of collapsing it', () => {
		expect(parseChatMarkdown('one\ntwo')).toEqual([
			{ kind: 'paragraph', inline: [{ kind: 'text', text: 'one' }, { kind: 'break' }, { kind: 'text', text: 'two' }] }
		]);
	});

	it('splits paragraphs on a blank line', () => {
		expect(parseChatMarkdown('one\n\ntwo').map((block) => block.kind)).toEqual(['paragraph', 'paragraph']);
	});

	it('renders bold, italic and inline code', () => {
		expect(parseChatMarkdown('is **7,006,652** and *roughly* `7e6`')).toEqual([
			{
				kind: 'paragraph',
				inline: [
					{ kind: 'text', text: 'is ' },
					{ kind: 'strong', children: [{ kind: 'text', text: '7,006,652' }] },
					{ kind: 'text', text: ' and ' },
					{ kind: 'em', children: [{ kind: 'text', text: 'roughly' }] },
					{ kind: 'text', text: ' ' },
					{ kind: 'code', text: '7e6' }
				]
			}
		]);
	});

	it('does not parse emphasis inside inline code', () => {
		expect(parseChatMarkdown('`**not bold**`')).toEqual([{ kind: 'paragraph', inline: [{ kind: 'code', text: '**not bold**' }] }]);
	});

	it('groups numbered lines into an ordered list that keeps its start', () => {
		expect(parseChatMarkdown('Tips:\n3. First\n4. **Second**')).toEqual([
			{ kind: 'paragraph', inline: [{ kind: 'text', text: 'Tips:' }] },
			{
				kind: 'list',
				ordered: true,
				start: 3,
				items: [[{ kind: 'text', text: 'First' }], [{ kind: 'strong', children: [{ kind: 'text', text: 'Second' }] }]]
			}
		]);
	});

	it('groups dash and star lines into an unordered list', () => {
		expect(parseChatMarkdown('- Rina: pro\n* Budi: free')).toEqual([
			{
				kind: 'list',
				ordered: false,
				start: 1,
				items: [[{ kind: 'text', text: 'Rina: pro' }], [{ kind: 'text', text: 'Budi: free' }]]
			}
		]);
	});

	it('appends an indented continuation line to the previous list item', () => {
		const [list] = parseChatMarkdown('1. First\n   still first\n2. Second');
		expect(list).toEqual({
			kind: 'list',
			ordered: true,
			start: 1,
			items: [
				[{ kind: 'text', text: 'First' }, { kind: 'break' }, { kind: 'text', text: 'still first' }],
				[{ kind: 'text', text: 'Second' }]
			]
		});
	});

	it('reads a fenced code block with its language and keeps the text verbatim', () => {
		expect(parseChatMarkdown('```js\nconst a = **1**;\n```')).toEqual([{ kind: 'code', lang: 'js', text: 'const a = **1**;' }]);
	});

	it('treats an unterminated fence as code, so a streamed reply does not flicker', () => {
		expect(parseChatMarkdown('Here:\n```\nline 1')).toEqual([
			{ kind: 'paragraph', inline: [{ kind: 'text', text: 'Here:' }] },
			{ kind: 'code', lang: '', text: 'line 1' }
		]);
	});

	it('reads headings and quotes', () => {
		expect(parseChatMarkdown('## Summary\n> quoted').map((block) => block.kind)).toEqual(['heading', 'quote']);
		expect(parseChatMarkdown('#### deep')[0]).toMatchObject({ kind: 'heading', level: 3 });
	});

	it('links only http, https and mailto targets', () => {
		expect(parseChatMarkdown('[docs](https://example.com/a)')).toEqual([
			{ kind: 'paragraph', inline: [{ kind: 'link', href: 'https://example.com/a', children: [{ kind: 'text', text: 'docs' }] }] }
		]);
		expect(parseChatMarkdown('[click](javascript:alert(1))')).toEqual([
			{ kind: 'paragraph', inline: [{ kind: 'text', text: '[click](javascript:alert(1))' }] }
		]);
	});

	it('keeps markup as literal text', () => {
		expect(parseChatMarkdown('<img src=x onerror=alert(1)>')).toEqual([
			{ kind: 'paragraph', inline: [{ kind: 'text', text: '<img src=x onerror=alert(1)>' }] }
		]);
	});

	it('leaves a lone asterisk alone', () => {
		expect(parseChatMarkdown('2 * 3 = 6')).toEqual([{ kind: 'paragraph', inline: [{ kind: 'text', text: '2 * 3 = 6' }] }]);
	});
});
