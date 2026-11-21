<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- The photos that arrived, and the way to the library (A4, J2, J3). Six
     tiles fill one by one in the order the photos came; once full, every 5–8 s
     one tile at random fades to a newer photo (the canvas note "View 4
     behaviour"). A tile shows its photo only once loaded: never a hole. -->
<script lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import { goto } from '$app/navigation';
	import { onMount, untrack } from 'svelte';
	import { turned, type GridPhoto } from '#lib/api.js';
	import { text } from '#lib/strings.js';

	let { label, photos }: { label: string; photos: GridPhoto[] } = $props();

	const TILES = 6;
	// Each tile: the photo on it, and the one it fades from.
	let tiles = $state<{ now?: GridPhoto; was?: GridPhoto }[]>(Array.from({ length: TILES }, () => ({})));
	// Photos seen, and those still to show, oldest first.
	const seen = new Set<string>();
	let waiting: GridPhoto[] = [];

	// The engine lists the newest first: what is new joins the line oldest first.
	$effect(() => {
		const fresh = photos.filter((p) => !seen.has(p.hash)).reverse();
		for (const p of fresh) seen.add(p.hash);
		waiting = [...waiting, ...fresh];
		// The tiles are written, not followed: only new photos run this again.
		untrack(fill);
	});
	function fill() {
		tiles = tiles.map((t) => (t.now || waiting.length === 0 ? t : { now: waiting.shift() }));
	}

	// Once full, one tile at a time takes a newer photo; none with less motion asked.
	onMount(() => {
		if (matchMedia('(prefers-reduced-motion: reduce)').matches) return;
		let timer: ReturnType<typeof setTimeout>;
		const next = () => {
			timer = setTimeout(() => {
				if (waiting.length > 0 && tiles.every((t) => t.now)) {
					const i = Math.floor(Math.random() * TILES);
					tiles[i] = { now: waiting.shift(), was: tiles[i].now };
				}
				next();
			}, 5000 + Math.random() * 3000);
		};
		next();
		return () => clearTimeout(timer);
	});

	let loaded = $state(new Set<string>());
	const shown = (hash: string) => (loaded = new Set(loaded).add(hash));
</script>

<div class="strip">
	<div class="head">
		<span>{label}</span>
		<button class="link" onclick={() => goto('/library')}>{text.seeLibrary}<Icon name="arrow-right" size={14} /></button>
	</div>
	<div class="tiles">
		{#each tiles as t, i (i)}
			<div class="tile">
				{#if t.was}<img src="/api/thumb/{t.was.hash}" alt="" style:transform={turned(t.was)} />{/if}
				{#if t.now}
					{#key t.now.hash}
						<img src="/api/thumb/{t.now.hash}" alt={t.now.name} style:transform={turned(t.now)} class:in={loaded.has(t.now.hash)} onload={() => shown(t.now!.hash)} />
					{/key}
				{/if}
				{#if !t.now || (!t.was && !loaded.has(t.now.hash))}<span class="spin"><Icon name="arrow-repeat" size={16} /></span>{/if}
			</div>
		{/each}
	</div>
</div>

<style>
	.strip {
		width: 100%;
		display: flex;
		flex-direction: column;
		gap: var(--space-12);
		margin-top: var(--space-8);
	}
	.head {
		display: flex;
		justify-content: space-between;
		font-size: var(--text-callout);
		color: var(--muted);
	}
	.link {
		display: inline-flex;
		align-items: center;
		gap: var(--space-6);
		color: var(--accent);
		font-weight: var(--weight-semibold);
		font-size: var(--text-callout);
	}
	.tiles {
		display: grid;
		grid-template-columns: repeat(6, 1fr);
		gap: var(--space-8);
	}
	.tile {
		position: relative;
		aspect-ratio: 1;
		border-radius: var(--radius-6);
		overflow: hidden;
		background: var(--line);
		display: grid;
		place-items: center;
		color: var(--faint);
	}
	.tile img {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		object-fit: cover;
	}
	/* The newer photo, over the one it replaces, fades in once loaded. */
	.tile img + img,
	.tile img:only-of-type {
		opacity: 0;
		transition: opacity 600ms ease;
	}
	.tile img.in {
		opacity: 1;
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
