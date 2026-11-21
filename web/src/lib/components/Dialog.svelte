<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- A question over the window: a title, what it needs, the buttons. -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { pop, swap } from '#lib/motion.js';

	let { title, children, actions }: { title: string; children: Snippet; actions: Snippet } = $props();
	const id = `dialog-${Math.random().toString(36).slice(2)}`;
</script>

<div class="scrim" in:swap out:swap={{ leaving: true }}>
	<div class="dialog" in:pop out:pop={{ leaving: true }} role="dialog" aria-modal="true" aria-labelledby={id}>
		<h2 {id}>{title}</h2>
		{@render children()}
		<div class="actions">{@render actions()}</div>
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
		gap: var(--space-8);
		justify-content: flex-end;
	}
</style>
