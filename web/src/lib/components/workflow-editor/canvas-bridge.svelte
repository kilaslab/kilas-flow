<script lang="ts" module>
	/**
	 * The subset of Svelte Flow's own API the editor calls.
	 *
	 * Declared here rather than imported whole: the editor only needs viewport
	 * movement and two lookups, and naming them keeps the flow library's
	 * generics out of a component that has its own types to think about.
	 */
	export type CanvasFlow = {
		fitView: (options?: { padding?: number; duration?: number; nodes?: { id: string }[]; maxZoom?: number }) => Promise<boolean>;
		zoomIn: (options?: { duration?: number }) => Promise<boolean>;
		zoomOut: (options?: { duration?: number }) => Promise<boolean>;
		setCenter: (x: number, y: number, options?: { zoom?: number; duration?: number }) => Promise<boolean>;
		setZoom: (zoom: number, options?: { duration?: number }) => Promise<boolean>;
		getViewport: () => { x: number; y: number; zoom: number };
		screenToFlowPosition: (position: { x: number; y: number }, options?: { snapToGrid?: boolean }) => { x: number; y: number };
		getInternalNode: (id: string) => { measured?: { width?: number; height?: number } | null } | undefined;
	};
</script>

<script lang="ts">
	import { useSvelteFlow } from '@xyflow/svelte';

	/**
	 * Publishes Svelte Flow's viewport API to the editor.
	 *
	 * The editor owns the document and the keyboard, and Svelte Flow owns the
	 * viewport, but only a component *inside* `<SvelteFlow>` can read its
	 * context. This is that component and nothing else: it renders no markup
	 * and decides nothing.
	 */
	let { onReady }: { onReady: (flow: CanvasFlow) => void } = $props();

	const flow = useSvelteFlow();

	$effect(() => {
		onReady(flow as unknown as CanvasFlow);
	});
</script>
