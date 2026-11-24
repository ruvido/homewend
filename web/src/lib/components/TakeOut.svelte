<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Taking photos whose file is gone out of the library: a clean-up, so a
     card that still has them brings them in again, unless kept out. -->
<script lang="ts">
	import { ApiError, takeOutMissing, type PhotoKey } from '#lib/api.js';
	import { refresh } from '#lib/app.svelte.js';
	import { text } from '#lib/strings.js';
	import PrimaryButton from './PrimaryButton.svelte';
	import SecondaryButton from './SecondaryButton.svelte';
	import Dialog from './Dialog.svelte';

	let { count, photo, onclose }: { count: number; photo?: PhotoKey; onclose: (taken: boolean) => void } = $props();
	let keepOut = $state(false);
	let busy = $state(false);
	let failed = $state('');

	async function take() {
		busy = true;
		failed = '';
		try {
			await takeOutMissing(photo, keepOut);
			await refresh();
			onclose(true);
		} catch (e) {
			// An import holds the library, or its disk went away: said, the
			// dialog stays to try again.
			failed = e instanceof ApiError && e.status === 409 ? text.libraryBusy : e instanceof ApiError && e.status === 410 ? text.libraryNotAnswering : text.takeOutFailed;
		} finally {
			busy = false;
		}
	}
</script>

<Dialog title={text.takeOutAsk(count)}>
	<p>{text.takeOutAskLead}</p>
	<label>
		<input type="checkbox" bind:checked={keepOut} />
		<span><b>{text.keepOut}</b><small>{text.keepOutDesc}</small></span>
	</label>
	{#if failed}<p class="failed">{failed}</p>{/if}
	{#snippet actions()}
		<SecondaryButton onclick={() => onclose(false)}>{text.cancel}</SecondaryButton>
		<PrimaryButton disabled={busy} onclick={take}>{text.takeOut}</PrimaryButton>
	{/snippet}
</Dialog>

<style>
	p {
		font-size: var(--text-body);
		line-height: 1.5;
		color: var(--muted);
	}
	label {
		display: flex;
		gap: 10px;
		align-items: flex-start;
		font-size: var(--text-body);
		color: var(--fg);
	}
	input {
		margin: 2px 0 0;
		accent-color: var(--accent);
	}
	label span {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	small {
		font-size: var(--text-callout);
		color: var(--muted);
	}
	.failed {
		color: var(--err);
		font-size: var(--text-callout);
	}
</style>
