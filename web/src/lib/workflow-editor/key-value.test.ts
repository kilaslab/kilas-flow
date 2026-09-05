import { describe, expect, it } from 'vitest';

import { renameKeyValue } from './key-value';

describe('key/value editor helpers', () => {
	it('keeps the existing value until a field-name edit is committed', () => {
		expect(renameKeyValue({ '': 'ready' }, '', 'status')).toEqual({ status: 'ready' });
	});

	it('drops blank names instead of creating an invalid assignment key', () => {
		expect(renameKeyValue({ status: 'ready' }, 'status', '   ')).toEqual({});
	});
});
