<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		small = false,
		onclick,
		disabled = false,
		children
	}: { small?: boolean; onclick?: () => void; disabled?: boolean; children: Snippet } = $props();
</script>

<button class:small {onclick} {disabled}>{@render children()}</button>

<style>
	/* The primary button's size (see there). */
	button {
		height: 44px;
		padding: 0 20px;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 8px;
		border: 1px solid var(--border);
		border-radius: 12px;
		color: var(--accent);
		font-size: var(--text-body);
		font-weight: 600;
		white-space: nowrap;
	}
	button:hover:not(:disabled) {
		background: var(--soft);
	}
	/* As the primary one: greyed when it cannot be done now. */
	button:disabled {
		opacity: 0.4;
	}
	/* The titlebar's size of the same button. */
	.small {
		height: 32px;
		padding: 0 12px;
		border-radius: 8px;
		font-size: var(--text-callout);
	}
</style>
