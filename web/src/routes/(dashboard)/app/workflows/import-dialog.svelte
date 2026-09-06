<script lang="ts">
	import { goto } from '$app/navigation';
	import Upload from '@lucide/svelte/icons/upload';

	import { message } from '$lib/api/http';
	import { importWorkflow } from '$lib/api/generated/interop/interop';
	import type { ImportedWorkflowResource } from '$lib/api/generated/models';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';

	import ImportReport from './import-report.svelte';

	/**
	 * n8n import beside workflow creation: a file upload and a paste box that
	 * send the document to the importer exactly as given. The browser parses
	 * the text only to build the request value — it never judges the document,
	 * because the importer is the authority on what is valid n8n JSON and it
	 * produces good diagnostics, while a browser-side pre-check would produce
	 * a second, worse set of error messages for the same input.
	 */
	let { onImported }: { onImported: () => void } = $props();

	let open = $state(false);
	let fileName = $state<string | null>(null);
	let fileText = $state<string | null>(null);
	let pasted = $state('');
	let name = $state('');
	let error = $state<string | null>(null);
	let importing = $state(false);
	let result = $state<ImportedWorkflowResource | null>(null);
	let fileInput = $state<HTMLInputElement | null>(null);

	function reset() {
		fileName = null;
		fileText = null;
		pasted = '';
		name = '';
		error = null;
		importing = false;
		result = null;
		if (fileInput) fileInput.value = '';
	}

	function onOpenChange(next: boolean) {
		open = next;
		if (!next) reset();
	}

	async function onFileChange(event: Event) {
		const input = event.currentTarget as HTMLInputElement;
		const file = input.files?.[0];
		error = null;
		if (!file) {
			fileName = null;
			fileText = null;
			return;
		}
		try {
			fileText = await file.text();
			fileName = file.name;
		} catch {
			fileName = null;
			fileText = null;
			error = 'That file could not be read. Try again or paste the export instead.';
		}
	}

	function clearFile() {
		fileName = null;
		fileText = null;
		error = null;
		if (fileInput) fileInput.value = '';
	}

	async function submitImport() {
		const raw = fileText ?? pasted;
		if (!raw.trim()) {
			error = 'Choose an n8n export file or paste one below to continue.';
			return;
		}
		let document: unknown;
		try {
			document = JSON.parse(raw);
		} catch {
			// Syntax only: whether the parsed value *is* an n8n workflow is
			// the importer's call, answered as a 422 with the exact reason.
			error = fileName
				? `${fileName} is not valid JSON. Export the workflow from n8n again and retry.`
				: 'That text is not valid JSON. Paste the whole n8n export and retry.';
			return;
		}
		importing = true;
		error = null;
		try {
			const trimmedName = name.trim();
			const response = await importWorkflow({
				format: 'n8n',
				workflow: document,
				...(trimmedName ? { name: trimmedName } : {})
			});
			if (response.status !== 201) throw new Error('Unexpected import response');
			result = response.data;
			onImported();
		} catch (thrown) {
			error = message(thrown);
		} finally {
			importing = false;
		}
	}

	async function openWorkflow() {
		if (!result) return;
		open = false;
		const id = result.workflow.id;
		reset();
		await goto(`/app/workflows/${id}`);
	}
</script>

<Dialog.Root {open} {onOpenChange}>
	<Dialog.Trigger>
		{#snippet child({ props })}
			<Button {...props} variant="outline" size="sm">
				<Upload aria-hidden="true" />
				Import n8n
			</Button>
		{/snippet}
	</Dialog.Trigger>
	<Dialog.Content
		aria-describedby="import-n8n-description"
		class={result ? 'max-h-[85vh] overflow-y-auto sm:max-w-3xl' : 'sm:max-w-lg'}
	>
		<Dialog.Header>
			<Dialog.Title>{result ? `Imported ${result.workflow.name}` : 'Import from n8n'}</Dialog.Title>
			<Dialog.Description id="import-n8n-description">
				{#if result}
					What the file became. Read the report, re-point any webhooks, then open the workflow.
				{:else}
					Bring an n8n workflow export. Unsupported nodes arrive as visible placeholders, never silent remaps.
				{/if}
			</Dialog.Description>
		</Dialog.Header>
		{#if result}
			<ImportReport {result} onOpenWorkflow={() => void openWorkflow()} />
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={reset}>Import another</Button>
				<Button type="button" onclick={() => void openWorkflow()}>Open in the editor</Button>
			</Dialog.Footer>
		{:else}
			<form
				class="grid gap-4"
				onsubmit={(event) => {
					event.preventDefault();
					void submitImport();
				}}
			>
				<div class="grid gap-2">
					<label for="import-file" class="text-sm font-medium">Export file</label>
					<Input
						id="import-file"
						type="file"
						accept="application/json,.json"
						bind:ref={fileInput}
						onchange={(event) => void onFileChange(event)}
						aria-describedby={fileName ? 'import-file-name' : undefined}
					/>
					{#if fileName}
						<p id="import-file-name" class="flex items-center gap-2 text-xs text-muted-foreground">
							<span class="min-w-0 flex-1 truncate font-mono">{fileName}</span>
							<Button type="button" variant="ghost" size="sm" class="h-6 shrink-0 px-2 text-xs" onclick={clearFile}>Clear</Button>
						</p>
					{/if}
				</div>
				<div class="grid gap-2">
					<label for="import-paste" class="text-sm font-medium">Or paste the export</label>
					<Textarea
						id="import-paste"
						bind:value={pasted}
						placeholder={'{"name": "My workflow", "nodes": […]}'}
						rows={6}
						class="font-mono text-xs"
						disabled={fileText !== null}
					/>
					{#if fileText !== null}
						<p class="text-xs text-muted-foreground">Pasting is disabled while a file is chosen — clear the file above to paste instead.</p>
					{/if}
				</div>
				<div class="grid gap-2">
					<label for="import-name" class="text-sm font-medium">Workflow name <span class="font-normal text-muted-foreground">(optional)</span></label>
					<Input id="import-name" bind:value={name} placeholder="Defaults to the name in the file" />
				</div>
				{#if error}
					<p role="alert" class="text-sm text-destructive">{error}</p>
				{/if}
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => (open = false)} disabled={importing}>Cancel</Button>
					<Button type="submit" disabled={importing}>{importing ? 'Importing…' : 'Import workflow'}</Button>
				</Dialog.Footer>
			</form>
		{/if}
	</Dialog.Content>
</Dialog.Root>
