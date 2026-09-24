import { render } from 'svelte/server';
import { describe, expect, it } from 'vitest';

import NodeConsole from './node-console.svelte';

/**
 * The component's server-rendered markup.
 *
 * Presentational and prop-driven, same as `property-field.test.ts`: SSR needs
 * no DOM and nothing here depends on `$effect`.
 */
function html(props: { lines: { level: string; text: string; at?: string }[]; truncated: boolean }): string {
	const rendered = render(NodeConsole, { props });
	return rendered.body.replace(/<!--.*?-->/g, '');
}

describe('a node that printed nothing', () => {
	it('shows the empty state instead of an empty list', () => {
		const markup = html({ lines: [], truncated: false });

		expect(markup).toContain('printed nothing');
		expect(markup).not.toContain('<ol');
	});
});

describe('a node with console output', () => {
	it('renders every line with its level', () => {
		const markup = html({
			lines: [
				{ level: 'log', text: 'starting up' },
				{ level: 'error', text: 'boom' }
			],
			truncated: false
		});

		expect(markup).toContain('<ol');
		expect(markup).toContain('starting up');
		expect(markup).toContain('boom');
		expect(markup).toContain('>log<');
		expect(markup).toContain('>error<');
	});

	it('gives warn and error a distinct tone from log', () => {
		const markup = html({
			lines: [
				{ level: 'log', text: 'plain' },
				{ level: 'warn', text: 'careful' },
				{ level: 'error', text: 'broken' }
			],
			truncated: false
		});

		const warnLine = markup.split('careful')[0].split('<li').pop() ?? '';
		const errorLine = markup.split('broken')[0].split('<li').pop() ?? '';
		expect(warnLine).toContain('text-warning');
		expect(errorLine).toContain('text-destructive');
	});

	it('notes truncation when the backend marked it', () => {
		const markup = html({ lines: [{ level: 'log', text: 'a' }], truncated: true });

		expect(markup).toContain('dropped');
	});

	it('says nothing about truncation when the backend did not mark it', () => {
		const markup = html({ lines: [{ level: 'log', text: 'a' }], truncated: false });

		expect(markup).not.toContain('dropped');
	});
});
