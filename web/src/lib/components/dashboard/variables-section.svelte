<script lang="ts">
	import { onMount } from 'svelte';
	import Braces from '@lucide/svelte/icons/braces';

	import { message } from '$lib/api/http';
	import { createVariable, deleteVariable, listVariables, updateVariable } from '$lib/api/generated/variables/variables';
	import type { VariableResource } from '$lib/api/generated/models';
	import ListStates from '$lib/components/dashboard/list-states.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { RequestGuard } from '$lib/dashboard/request-guard';
	import * as m from '$lib/paraglide/messages.js';

	/**
	 * Settings → Variables (FEAT-m7aw75): the tenant's key/value pairs every
	 * workflow reads as `$vars.KEY`. Values are plain text on the server, so the
	 * section says plainly that secrets belong in a credential.
	 */
	const KEY_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/;

	let rows = $state<VariableResource[]>([]);
	let loading = $state(true);
	let listFailure = $state<unknown>(null);
	const listGuard = new RequestGuard();

	let newKey = $state('');
	let newValue = $state('');
	let adding = $state(false);
	let addError = $state<string | null>(null);

	let editing = $state<VariableResource | null>(null);
	let editKey = $state('');
	let editValue = $state('');
	let saving = $state(false);
	let editError = $state<string | null>(null);

	let removing = $state<VariableResource | null>(null);
	let removingBusy = $state(false);

	async function load() {
		const token = listGuard.start();
		listFailure = null;
		loading = rows.length === 0;
		try {
			const response = await listVariables();
			if (response.status !== 200) throw new Error(m.settings_variables_error_unexpected());
			if (!listGuard.holds(token)) return;
			rows = response.data ?? [];
		} catch (cause) {
			if (!listGuard.holds(token)) return;
			listFailure = cause;
		} finally {
			if (listGuard.holds(token)) loading = false;
		}
	}

	onMount(() => void load());

	/** The client-side half of the server's key rule, so a typo is caught before a round trip. */
	function keyProblem(key: string): string | null {
		if (!key) return m.settings_variables_key_required();
		if (key.length > 64 || !KEY_PATTERN.test(key)) return m.settings_variables_key_invalid();
		return null;
	}

	async function submitAdd() {
		const key = newKey.trim();
		addError = keyProblem(key);
		if (addError) return;
		adding = true;
		try {
			const response = await createVariable({ key, value: newValue });
			if (response.status !== 201) throw new Error(m.settings_variables_error_unexpected());
			newKey = '';
			newValue = '';
			await load();
		} catch (error) {
			addError = message(error);
		} finally {
			adding = false;
		}
	}

	function openEdit(variable: VariableResource) {
		editing = variable;
		editKey = variable.key;
		editValue = variable.value;
		editError = null;
	}

	async function submitEdit() {
		if (!editing) return;
		const key = editKey.trim();
		editError = keyProblem(key);
		if (editError) return;
		saving = true;
		try {
			const response = await updateVariable(editing.id, { key, value: editValue });
			if (response.status !== 200) throw new Error(m.settings_variables_error_unexpected());
			editing = null;
			await load();
		} catch (error) {
			editError = message(error);
		} finally {
			saving = false;
		}
	}

	async function confirmRemove() {
		if (!removing) return;
		removingBusy = true;
		try {
			const response = await deleteVariable(removing.id);
			if (response.status !== 204) throw new Error(m.settings_variables_error_unexpected());
			removing = null;
			await load();
		} catch (error) {
			listFailure = error;
			removing = null;
		} finally {
			removingBusy = false;
		}
	}
</script>

<section aria-label={m.settings_variables()} class="rounded-xl border border-border bg-card p-4">
	<h2 class="text-sm font-semibold">{m.settings_variables()}</h2>
	<p class="mt-1 text-xs leading-5 text-muted-foreground">{m.settings_variables_description()}</p>
	<p role="note" class="mt-1 text-xs leading-5 text-muted-foreground">{m.settings_variables_secret_note()}</p>

	<form
		class="mt-3 grid gap-2 sm:grid-cols-[minmax(0,12rem)_minmax(0,1fr)_auto] sm:items-start"
		onsubmit={(event) => {
			event.preventDefault();
			void submitAdd();
		}}
	>
		<div class="grid gap-1">
			<label for="variable-new-key" class="text-xs font-medium">{m.settings_variables_key()}</label>
			<Input id="variable-new-key" bind:value={newKey} maxlength={64} autocomplete="off" spellcheck={false} class="font-mono" placeholder="API_BASE" />
		</div>
		<div class="grid gap-1">
			<label for="variable-new-value" class="text-xs font-medium">{m.settings_variables_value()}</label>
			<Input id="variable-new-value" bind:value={newValue} maxlength={4096} autocomplete="off" spellcheck={false} />
		</div>
		<Button type="submit" class="sm:mt-5" disabled={adding}>{adding ? m.settings_variables_adding() : m.settings_variables_add()}</Button>
		{#if addError}<p role="alert" class="text-sm text-destructive sm:col-span-3">{addError}</p>{/if}
	</form>

	<div class="mt-4">
		<ListStates
			label={m.settings_variables()}
			{loading}
			failed={listFailure !== null}
			error={listFailure}
			count={rows.length}
			rows={2}
			rowClass="h-(--kf-row-h-2line)"
			onRetry={() => void load()}
			emptyIcon={Braces}
			emptyTitle={m.settings_variables_empty_title()}
			emptyBody={m.settings_variables_empty_body()}
		>
			<ul aria-label={m.settings_variables()} class="divide-y divide-border overflow-hidden rounded-lg border border-border">
				{#each rows as variable (variable.id)}
					<li class="flex min-h-(--kf-row-h-2line) items-center gap-3 px-3 py-1">
						<div class="min-w-0 flex-1">
							<p class="truncate font-mono text-sm">{variable.key}</p>
							<p class="truncate text-xs text-muted-foreground" title={variable.value}>{variable.value || '—'}</p>
						</div>
						<Button variant="ghost" onclick={() => openEdit(variable)}>{m.settings_variables_edit()}</Button>
						<Button variant="ghost" class="text-destructive" onclick={() => (removing = variable)}>{m.settings_variables_delete()}</Button>
					</li>
				{/each}
			</ul>
		</ListStates>
	</div>

	<Dialog.Root open={editing !== null} onOpenChange={(open) => !open && (editing = null)}>
		<Dialog.Content>
			<Dialog.Header>
				<Dialog.Title>{m.settings_variables_edit_title({ key: editing?.key ?? '' })}</Dialog.Title>
			</Dialog.Header>
			<form
				class="grid gap-4"
				onsubmit={(event) => {
					event.preventDefault();
					void submitEdit();
				}}
			>
				<div class="grid gap-2">
					<label for="variable-edit-key" class="text-sm font-medium">{m.settings_variables_key()}</label>
					<Input id="variable-edit-key" bind:value={editKey} maxlength={64} autocomplete="off" spellcheck={false} class="font-mono" />
				</div>
				<div class="grid gap-2">
					<label for="variable-edit-value" class="text-sm font-medium">{m.settings_variables_value()}</label>
					<Input id="variable-edit-value" bind:value={editValue} maxlength={4096} autocomplete="off" spellcheck={false} />
					{#if editError}<p role="alert" class="text-sm text-destructive">{editError}</p>{/if}
				</div>
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => (editing = null)} disabled={saving}>{m.settings_cancel()}</Button>
					<Button type="submit" disabled={saving}>{saving ? m.settings_variables_saving() : m.settings_variables_save()}</Button>
				</Dialog.Footer>
			</form>
		</Dialog.Content>
	</Dialog.Root>

	<Dialog.Root open={removing !== null} onOpenChange={(open) => !open && (removing = null)}>
		<Dialog.Content aria-describedby="delete-variable-description">
			<Dialog.Header>
				<Dialog.Title>{m.settings_variables_delete_title({ key: removing?.key ?? '' })}</Dialog.Title>
				<Dialog.Description id="delete-variable-description">{m.settings_variables_delete_description()}</Dialog.Description>
			</Dialog.Header>
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (removing = null)} disabled={removingBusy}>{m.settings_cancel()}</Button>
				<Button type="button" variant="destructive" onclick={() => void confirmRemove()} disabled={removingBusy}>
					{removingBusy ? m.settings_variables_deleting() : m.settings_variables_delete()}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
</section>
