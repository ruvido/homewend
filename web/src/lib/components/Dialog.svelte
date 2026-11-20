<!-- homewend — Copyright (C) 2026 Ron Alder -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- A question over the window: a title, what it needs, the buttons. -->
<script lang="ts">
	import type { Snippet } from 'svelte';

	let { title, children, actions }: { title: string; children: Snippet; actions: Snippet } = $props();
	const id = `dialog-${Math.random().toString(36).slice(2)}`;
</script>

<div class="scrim">
	<div class="dialog" role="dialog" aria-modal="true" aria-labelledby={id}>
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
	.actions {
		display: flex;
		gap: 8px;
		justify-content: flex-end;
	}
</style>
