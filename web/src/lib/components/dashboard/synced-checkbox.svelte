<script lang="ts">
	import { Checkbox } from '$lib/components/ui/checkbox';

	/**
	 * A checkbox whose state lives in the parent's collection.
	 *
	 * Two easier shapes fail here. The primitive's `onCheckedChange`
	 * callback never reaches the page — the box toggles visually while the
	 * parent's collection stays empty — and `bind:checked` only accepts
	 * state or props, so `bind:checked={record[key]}` inside an each-block
	 * throws `props_invalid_value` at runtime. This component owns one
	 * boolean, follows the prop on reset, and reports user flips through
	 * `onToggle`, which is the only direction pair that stays consistent.
	 */
	let {
		label,
		checked,
		onToggle
	}: {
		label: string;
		checked: boolean;
		onToggle: (next: boolean) => void;
	} = $props();

	// Seeded false and synced below rather than `$state(checked)`: the
	// initializer would capture the prop's mount-time value only.
	let on = $state(false);

	// Prop → local: a parent reset (new page, cleared selection) moves the box.
	$effect(() => {
		on = checked;
	});

	// A flip the parent has not acknowledged yet. A derivation rather than a
	// comparison inside the effect below: the effect must react to the live
	// prop, not to the value the closure captured on mount.
	const unacknowledged = $derived(on !== checked);

	$effect(() => {
		if (unacknowledged) onToggle(on);
	});
</script>

<Checkbox bind:checked={on} aria-label={label} />
