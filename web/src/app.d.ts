// See https://svelte.dev/docs/kit/types#app
declare global {
	/** True only in builds made with KILASFLOW_DESIGN_HARNESS=1 (see vite.config.ts). */
	const __KF_DESIGN_HARNESS__: boolean;

	namespace App {
		// interface Error {}
		// interface Locals {}
		// interface PageData {}
		// interface Platform {}
	}
}

export {};
