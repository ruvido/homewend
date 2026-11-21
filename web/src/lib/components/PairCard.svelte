<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- P2: the code a phone scans to pair, while it waits for the phone. -->
<script lang="ts">
	import QRCode from 'qrcode';
	import Icon from '#lib/components/Icon.svelte';
	import SecondaryButton from '#lib/components/SecondaryButton.svelte';
	import { text } from '#lib/strings.js';

	let { code, oncancel }: { code: string; oncancel: () => void } = $props();
	const qr = $derived(QRCode.create(code, { errorCorrectionLevel: 'M' }).modules);
	// The dark modules, one rect each, in the code's own units.
	const dark = $derived.by(() => {
		const cells: { x: number; y: number }[] = [];
		for (let y = 0; y < qr.size; y++) for (let x = 0; x < qr.size; x++) if (qr.get(y, x)) cells.push({ x, y });
		return cells;
	});
</script>

<div class="pair">
	<svg viewBox="-2 -2 {qr.size + 4} {qr.size + 4}" shape-rendering="crispEdges" aria-hidden="true">
		{#each dark as c (c.y * qr.size + c.x)}<rect x={c.x} y={c.y} width="1" height="1" />{/each}
	</svg>
	<div class="text">
		<h2>{text.scanThis}</h2>
		<p>{text.scanHow}</p>
		<span class="wait"><span class="spin"><Icon name="arrow-repeat" size={16} /></span>{text.waitingPhone}</span>
		<span class="cancel"><SecondaryButton small onclick={oncancel}>{text.cancel}</SecondaryButton></span>
	</div>
</div>

<style>
	.pair {
		display: flex;
		align-items: center;
		gap: var(--space-40);
		padding: var(--space-24);
		border-radius: var(--radius-16);
		border: 1px solid var(--line);
		background: var(--surface);
	}
	svg {
		width: 160px;
		height: 160px;
		flex-shrink: 0;
		display: block;
		fill: var(--fg);
	}
	.text {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: var(--space-8);
	}
	h2 {
		font-size: var(--text-title-1);
		font-weight: 700;
		color: var(--fg);
	}
	p {
		color: var(--muted);
	}
	.wait {
		display: inline-flex;
		align-items: center;
		gap: var(--space-8);
		color: var(--accent);
		font-weight: 600;
	}
	.cancel {
		padding-top: var(--space-8);
	}
	.spin {
		display: grid;
		animation: spin 1s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
