<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- P4c: the photos the Trash shows deleted for good, those and no others.
     P4d: while it runs, how far it is; it closes by itself at the end. -->
<script lang="ts">
	import { ApiError, emptyTrash, type PhotoKey } from '#lib/api.js';
	import { app, refresh } from '#lib/app.svelte.js';
	import { reveal } from '#lib/motion.js';
	import { text } from '#lib/strings.js';
	import PrimaryButton from './PrimaryButton.svelte';
	import SecondaryButton from './SecondaryButton.svelte';
	import Dialog from './Dialog.svelte';

	let { photos, busy = $bindable(), onclose, onemptied }: { photos: PhotoKey[]; busy: boolean; onclose: () => void; onemptied: (left: PhotoKey[]) => void } = $props();
	let failed = $state('');
	// Until the engine says how far it is, none done.
	const far = $derived(app.emptying ?? { done: 0, of: photos.length });

	async function empty() {
		busy = true;
		failed = '';
		try {
			const { left } = await emptyTrash(photos);
			await refresh();
			if (left.length > 0) failed = text.markFailed;
			onemptied(left);
		} catch (e) {
			failed = e instanceof ApiError && e.status === 409 ? text.libraryBusy : e instanceof ApiError && e.status === 410 ? text.libraryNotAnswering : text.markFailed;
		} finally {
			busy = false;
		}
	}
</script>

{#snippet buttons()}
	<SecondaryButton onclick={onclose}>{text.cancel}</SecondaryButton>
	<PrimaryButton danger onclick={empty}>{text.emptyTrash}</PrimaryButton>
{/snippet}

<Dialog title={busy ? text.emptyingTrash : text.emptyTrashAsk} actions={busy ? undefined : buttons}>
	{#if busy}
		<p>{text.emptyingTrashDone(far.done, far.of)}</p>
		<div class="track" in:reveal out:reveal={{ leaving: true }} role="progressbar" aria-label={text.emptyingTrash} aria-valuemin={0} aria-valuenow={far.done} aria-valuemax={far.of}>
			<b style:width="{(far.done / far.of) * 100}%"></b>
		</div>
	{:else}
		<p>{text.emptyTrashLead(photos.length)}</p>
		{#if failed}<p class="failed">{failed}</p>{/if}
	{/if}
</Dialog>

<style>
	p {
		font-size: var(--text-body);
		line-height: 1.5;
		color: var(--muted);
	}
	/* The app's progress bar, as the sidebar's thumbnails show it. */
	.track {
		height: var(--space-8);
		border-radius: var(--radius-full);
		background: var(--field);
		overflow: hidden;
	}
	.track b {
		display: block;
		height: 100%;
		border-radius: var(--radius-full);
		background: var(--accent);
		transition: width var(--motion-ambient) var(--ease-out);
	}
	.failed {
		color: var(--err);
		font-size: var(--text-callout);
	}
</style>
