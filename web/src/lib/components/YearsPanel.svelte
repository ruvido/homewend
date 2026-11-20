<!-- homewend — Copyright (C) 2026 Ron Alder -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- The panel beside a download or an import: its years as folders, each
     with its count and where it stands (A4, J3). The rows are the screen's. -->
<script lang="ts">
	import type { Snippet } from 'svelte';

	// A legend of several names heads columns of numbers, one each (J2, J3);
	// spacer keeps them over the numbers when the rows end with a state.
	let {
		title,
		legend,
		spacer = false,
		children
	}: { title: string; legend: string | string[]; spacer?: boolean; children: Snippet } = $props();
</script>

<section class="years">
	<header>
		<h2>{title}</h2>
		{#each [legend].flat() as l (l)}<span class:n={Array.isArray(legend)}>{l}</span>{/each}
		{#if spacer}<span class="spacer"></span>{/if}
	</header>
	{@render children()}
</section>

<style>
	.years {
		width: min(380px, 100%);
		flex-shrink: 0;
		max-height: 100%;
		overflow-y: auto;
		padding: 24px;
		border: 1px solid var(--border);
		border-radius: 16px;
	}
	header {
		display: flex;
		align-items: baseline;
		gap: 12px;
		padding-bottom: 12px;
	}
	h2 {
		flex: 1;
		font-size: var(--text-title-2);
		font-weight: 700;
		color: var(--fg);
	}
	header span {
		font-size: var(--text-callout);
		color: var(--faint);
	}
	.years :global(.year) {
		height: 44px;
		display: flex;
		align-items: center;
		gap: 12px;
		border-bottom: 1px solid var(--line);
	}
	.years :global(.folder) {
		display: grid;
		color: var(--folder);
	}
	.years :global(.name) {
		flex: 1;
		font-weight: 500;
		color: var(--fg);
	}
	.years :global(.count) {
		color: var(--text);
	}
	.years :global(.count.done) {
		color: var(--ok);
		font-weight: 600;
	}
	/* A column of numbers, its figures under each other. */
	.years :global(.n) {
		width: 64px;
		text-align: right;
		font-variant-numeric: tabular-nums;
	}
	.spacer {
		width: 40px;
	}
	.years :global(.state) {
		width: 40px;
		display: flex;
		justify-content: flex-end;
		color: var(--ok);
	}
</style>
