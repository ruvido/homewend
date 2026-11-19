<!-- homewend — Copyright (C) 2026 Ron Alder -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Taking photos whose file is gone out of the library: a clean-up, so a
     card that still has them brings them in again, unless kept out. -->
<script lang="ts">
	import { ApiError, takeOutMissing } from '#lib/api.js';
	import { refresh } from '#lib/app.svelte.js';
	import { text } from '#lib/strings.js';
	import PrimaryButton from './PrimaryButton.svelte';
	import SecondaryButton from './SecondaryButton.svelte';

	let { count, hash, onclose }: { count: number; hash?: string; onclose: (taken: boolean) => void } = $props();
	let keepOut = $state(false);
	let busy = $state(false);
	let failed = $state('');

	async function take() {
		busy = true;
		failed = '';
		try {
			await takeOutMissing(hash, keepOut);
			await refresh();
			onclose(true);
		} catch (e) {
			// An import holds the library, or its disk went away: said, the
			// dialog stays to try again.
			failed = e instanceof ApiError && e.status === 409 ? text.takeOutBusy : e instanceof ApiError && e.status === 410 ? text.libraryNotAnswering : text.takeOutFailed;
		} finally {
			busy = false;
		}
	}
</script>

<div class="scrim">
	<div class="dialog" role="dialog" aria-modal="true" aria-labelledby="take-out-title">
		<h2 id="take-out-title">{text.takeOutAsk(count)}</h2>
		<p>{text.takeOutAskLead}</p>
		<label>
			<input type="checkbox" bind:checked={keepOut} />
			<span><b>{text.keepOut}</b><small>{text.keepOutDesc}</small></span>
		</label>
		{#if failed}<p class="failed">{failed}</p>{/if}
		<div class="actions">
			<SecondaryButton onclick={() => onclose(false)}>{text.cancel}</SecondaryButton>
			<PrimaryButton disabled={busy} onclick={take}>{text.takeOut}</PrimaryButton>
		</div>
	</div>
</div>

<style>
	.scrim {
		position: fixed;
		inset: 0;
		z-index: 40;
		display: grid;
		place-items: center;
		background: #0a0a1a59;
	}
	.dialog {
		width: 440px;
		padding: 24px;
		display: flex;
		flex-direction: column;
		gap: 16px;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-16);
		box-shadow: 0 12px 40px #0a0a1a29;
	}
	h2 {
		font-size: var(--text-title-2);
		font-weight: 700;
		color: var(--fg);
	}
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
	.actions {
		display: flex;
		gap: 8px;
		justify-content: flex-end;
	}
	.failed {
		color: var(--err);
		font-size: var(--text-callout);
	}
</style>
