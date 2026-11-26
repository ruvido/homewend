<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Y2: a person's new name, and under it the folder it becomes; Y3: a name
     another person has, by capitals too, said under the field. -->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { ApiError, getLibrary, renameProfile } from '#lib/api.js';
	import { refresh } from '#lib/app.svelte.js';
	import { text } from '#lib/strings.js';
	import Dialog from './Dialog.svelte';
	import PrimaryButton from './PrimaryButton.svelte';
	import SecondaryButton from './SecondaryButton.svelte';

	let { profile, onclose }: { profile: string; onclose: () => void } = $props();

	let name = $state(untrack(() => profile));
	let dir = $state('');
	let busy = $state(false);
	let failed = $state('');
	onMount(() => {
		getLibrary().then((l) => (dir = l.dir), () => {});
	});
	const sep = $derived(dir.includes('\\') ? '\\' : '/');
	const to = $derived(name.trim());

	async function rename(e?: SubmitEvent) {
		e?.preventDefault();
		if (!to || to === profile || busy) return;
		busy = true;
		failed = '';
		try {
			await renameProfile(profile, to);
			await refresh();
			onclose();
		} catch (e) {
			const status = e instanceof ApiError ? e.status : 0;
			failed =
				{ 400: text.badProfile, 409: text.importBusy, 412: text.profileTaken, 422: text.profileCase, 423: text.renameInUse, 507: text.importNoRoom }[status] ??
				text.markFailed;
		} finally {
			busy = false;
		}
	}
</script>

<!-- Esc is Cancel. -->
<svelte:window
	onkeydowncapture={(e) => {
		if (e.key !== 'Escape') return;
		e.stopPropagation();
		e.preventDefault();
		onclose();
	}}
/>

<Dialog title={text.renameTitle(profile)}>
	<form onsubmit={rename}>
		<label>
			<span>{text.askProfileField}</span>
			<!-- svelte-ignore a11y_autofocus -->
			<input bind:value={name} oninput={() => (failed = '')} autofocus spellcheck="false" autocomplete="off" />
			<small class:err={failed}>{failed || (dir ? `${dir}${dir.endsWith(sep) ? '' : sep}${to}` : '')}</small>
		</label>
	</form>
	{#snippet actions()}
		<SecondaryButton onclick={onclose}>{text.cancel}</SecondaryButton>
		<PrimaryButton disabled={busy || !to || to === profile} onclick={() => rename()}>{text.rename}</PrimaryButton>
	{/snippet}
</Dialog>

<style>
	label {
		display: flex;
		flex-direction: column;
		gap: var(--space-8);
	}
	span {
		font-size: var(--text-callout);
		font-weight: var(--weight-semibold);
		color: var(--fg);
	}
	input {
		height: var(--size-segmented);
		padding: 0 var(--space-12);
		border: 1px solid var(--accent);
		border-radius: var(--radius-8);
		background: var(--surface);
		color: var(--fg);
		font: inherit;
		outline: none;
		user-select: text;
	}
	small {
		font-family: var(--font-mono);
		font-size: var(--text-callout);
		color: var(--muted);
		overflow-wrap: anywhere;
	}
	small.err {
		font-family: inherit;
		color: var(--err);
	}
</style>
