import { describe, expect, it } from 'vitest';

import fixture from '../../../../internal/property/testdata/visibility.json';
import { visible } from './visibility';

type VisibilityCase = {
	name: string;
	visibility: Parameters<typeof visible>[0];
	parameters: Record<string, unknown>;
	typeVersion?: string;
	visible: boolean;
};

/**
 * The other half of the anti-drift mechanism.
 *
 * Go reads this same file. Two implementations of one rule drift — that is not
 * a risk, it is a certainty — and a shared fixture is the only thing that keeps
 * the panel and the compiler agreeing about whether a workflow can be saved.
 */
describe('visibility, against the shared fixture', () => {
	const cases = (fixture as { cases: VisibilityCase[] }).cases;

	it('has cases to run', () => {
		expect(cases.length).toBeGreaterThan(0);
	});

	for (const testCase of cases) {
		it(testCase.name, () => {
			expect(visible(testCase.visibility, testCase.parameters, testCase.typeVersion ?? '')).toBe(
				testCase.visible
			);
		});
	}
});
