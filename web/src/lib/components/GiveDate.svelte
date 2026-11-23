<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- K1's Change date…: the photos picked in the library get a date; the
     earliest takes it, the others keep their distance, as before an import. -->
<script lang="ts">
	import { untrack } from 'svelte';
	import { ApiError, giveDates, type GridPhoto } from '#lib/api.js';
	import { refresh } from '#lib/app.svelte.js';
	import { text } from '#lib/strings.js';
	import Dialog from './Dialog.svelte';
	import DatePicker from './DatePicker.svelte';
	import PrimaryButton from './PrimaryButton.svelte';
	import SecondaryButton from './SecondaryButton.svelte';

	let { photos, onclose }: { photos: GridPhoto[]; onclose: (moved: boolean) => void } = $props();

	// The earliest date among them, as the field starts: a clock written as UTC.
	const earliest = untrack(() => photos.map((p) => p.taken).filter((t): t is string => !!t).sort()[0]);
	// None dated: now, by this computer's clock, written as the clock it reads.
	const now = new Date();
	const local = new Date(now.getTime() - now.getTimezoneOffset() * 60000).toISOString();
	let when = $state((earliest ?? local).slice(0, 16));
	let busy = $state(false);
	let failed = $state('');
	async function give() {
		busy = true;
		failed = '';
		try {
			await giveDates(photos.map((p) => p.hash), when + ':00Z');
			await refresh();
			onclose(true);
		} catch (e) {
			failed = e instanceof ApiError && e.status === 409 ? text.changeDateBusy : text.changeDateFailed;
		} finally {
			busy = false;
		}
	}
</script>

<Dialog title={text.changeDateOf(photos.length)}>
	<DatePicker bind:value={when} />
	{#if failed}<p class="failed">{failed}</p>{/if}
	{#snippet actions()}
		<SecondaryButton onclick={() => onclose(false)}>{text.cancel}</SecondaryButton>
		<PrimaryButton disabled={busy || !when} onclick={give}>{text.changeDate}</PrimaryButton>
	{/snippet}
</Dialog>

<style>
	.failed {
		color: var(--err);
		font-size: var(--text-callout);
	}
</style>
