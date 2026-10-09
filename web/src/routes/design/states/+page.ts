import { error } from '@sveltejs/kit';

import type { PageLoad } from './$types';

// The design-system state matrix (VR-03): every primitive in every interaction
// state, for the visual regression suite. It is not a product page. Both
// conditions are build-time literals, so a release build compiles this to
// `if (false) { … }` and the dynamic import, and with it the whole harness
// chunk, is dropped from the bundle; the route then answers 404.
export const load: PageLoad = async ({ url }) => {
	if (import.meta.env.DEV || __KF_DESIGN_HARNESS__) {
		const { default: StateMatrix } = await import('$lib/design-system/state-matrix.svelte');
		return { StateMatrix, section: url.searchParams.get('section') ?? 'button' };
	}
	error(404, 'Not found');
};
