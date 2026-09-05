/**
 * Reactive `matchMedia` result.
 *
 * The editor uses this to *mount* one properties panel instead of rendering the
 * desktop panel and the narrow-viewport dialog together and hiding one with CSS.
 * Two mounted panels would duplicate every control id and keep an `aria-modal`
 * dialog in the tree on desktop.
 *
 * Returns `false` during SSR, so a server-rendered page never emits the overlay.
 */
export function mediaQuery(query: string): { readonly current: boolean } {
	let matches = $state(false);

	$effect(() => {
		const list = window.matchMedia(query);
		matches = list.matches;
		const onChange = (event: MediaQueryListEvent) => (matches = event.matches);
		list.addEventListener('change', onChange);
		return () => list.removeEventListener('change', onChange);
	});

	return {
		get current() {
			return matches;
		}
	};
}
