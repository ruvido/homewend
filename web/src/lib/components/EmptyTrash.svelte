<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- P4c: the photos the Trash shows deleted for good, those and no others. -->
<script lang="ts">
	import { ApiError, emptyTrash, type PhotoKey } from '#lib/api.js';
	import { refresh } from '#lib/app.svelte.js';
	import { text } from '#lib/strings.js';
	import PrimaryButton from './PrimaryButton.svelte';
	import SecondaryButton from './SecondaryButton.svelte';
	import Dialog from './Dialog.svelte';

	let { photos, busy = $bindable(), onclose, onemptied }: { photos: PhotoKey[]; busy: boolean; onclose: () => void; onemptied: (left: PhotoKey[]) => void } = $props();
	let failed = $state('');

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

<Dialog title={text.emptyTrashAsk}>
	<p>{text.emptyTrashLead(photos.length)}</p>
	{#if failed}<p class="failed">{failed}</p>{/if}
	{#snippet actions()}
		<SecondaryButton disabled={busy} onclick={onclose}>{text.cancel}</SecondaryButton>
		<PrimaryButton danger disabled={busy} onclick={empty}>{text.emptyTrash}</PrimaryButton>
	{/snippet}
</Dialog>

<style>
	p {
		font-size: var(--text-body);
		line-height: 1.5;
		color: var(--muted);
	}
	.failed {
		color: var(--err);
		font-size: var(--text-callout);
	}
</style>
