// Test-only loader for web/src/app.css. Kept apart from css-tokens.ts so the
// parser stays free of node:fs and can be imported anywhere.
import { readFileSync } from 'node:fs';

import { parseTokenSheet, type TokenSheet } from './css-tokens';

export const APP_CSS_URL = new URL('../../app.css', import.meta.url);

export function readAppCss(): string {
	return readFileSync(APP_CSS_URL, 'utf8');
}

export function appTokenSheet(): TokenSheet {
	return parseTokenSheet(readAppCss());
}
