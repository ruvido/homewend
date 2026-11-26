<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- A question over the window: a title, what it needs, the buttons; none
     while what was asked is being done. -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { pop, reveal, swap } from '#lib/motion.js';

	let { title, children, actions }: { title: string; children: Snippet; actions?: Snippet } = $props();
	let dialog: HTMLElement;
	// A field, where there is one, takes the keyboard; else the first button,
	// Cancel where there is one to choose: a key pressed by habit does nothing.
	$effect(() => {
		if (actions && !dialog.contains(document.activeElement)) (dialog.querySelector<HTMLElement>('input, textarea') ?? dialog.querySelector<HTMLElement>('.actions button'))?.focus();
	});
	const id = `dialog-${Math.random().toString(36).slice(2)}`;
</script>

<div class="scrim" in:swap out:swap={{ leaving: true }}>
	<div class="dialog" bind:this={dialog} in:pop out:pop={{ leaving: true }} role="dialog" aria-modal="true" aria-labelledby={id}>
		<h2 {id}>{title}</h2>
		{@render children()}
		{#if actions}<div class="actions" in:reveal out:reveal={{ leaving: true }}>{@render actions()}</div>{/if}
	</div>
</div>

<style>
	.scrim {
		position: fixed;
		inset: 0;
		z-index: 40;
		display: grid;
		place-items: center;
		background: var(--scrim);
	}
	.dialog {
		width: 440px;
		max-width: calc(100vw - 2 * var(--space-16));
		padding: var(--space-24);
		display: flex;
		flex-direction: column;
		gap: var(--space-16);
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-16);
		box-shadow: var(--shadow-dialog);
	}
	h2 {
		font-size: var(--text-title-2);
		font-weight: var(--weight-bold);
		color: var(--fg);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-8);
		justify-content: flex-end;
	}
</style>
