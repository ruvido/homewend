<!-- homewend — Copyright (C) 2026 Ron Alder -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- The photos, by month, newest first. Only the rows on screen exist: the
     grid has to scroll through hundreds of thousands of tiles. -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { Circle, CircleCheck, ImageOff, Play } from '@lucide/svelte';
	import { pathOf, turned, type GridPhoto } from '#lib/api.js';
	import ContextMenu, { folderItems } from './ContextMenu.svelte';
	import { Opener } from '#lib/menu.svelte.js';
	import { text } from '#lib/strings.js';

	let {
		photos,
		header,
		onopen,
		selected,
		onselect,
		withYear = true
	}: {
		photos: GridPhoto[];
		header?: Snippet;
		onopen?: (index: number) => void;
		/** The photos picked (K1): while there are some, a click picks too. */
		selected?: Set<string>;
		/** A photo picked or let go; range: with Shift, from the last one. */
		onselect?: (index: number, range: boolean) => void;
		/** On a year's page the months need no year. */
		withYear?: boolean;
	} = $props();

	// The design's tile: at least 104px, as many columns as fit, gap 8.
	const MIN_TILE = 104;
	const GAP = 8;
	const MONTH = 40; // a month's heading, with the space above it
	const OVERSCAN = 600; // pixels drawn beyond the screen, both ways

	let width = $state(0);
	let height = $state(0);
	let scrollTop = $state(0);
	let headerHeight = $state(0);

	// A photo's menu, at the end of its tile: its folder, its path.
	const opener = new Opener<{ path: string }>();
	const context = (e: MouseEvent, hash: string) => opener.open(e, () => pathOf(hash));
	// Dragged out of the window, a photo is its file, handed to the system's
	// drag as the file manager hands one: the original goes where it is dropped.
	function drag(e: DragEvent, hash: string) {
		e.preventDefault();
		pathOf(hash).then(({ path }) => window.shell?.dragFile(path), () => {});
	}

	const selecting = $derived(!!selected && selected.size > 0);
	// A click opens the photo; on its circle, or while picking, or with
	// Shift, it picks it instead.
	function click(e: MouseEvent, p: GridPhoto) {
		const i = photos.indexOf(p);
		if (onselect && (selecting || e.shiftKey || (e.target as Element).closest('.pick'))) onselect(i, e.shiftKey);
		else onopen?.(i);
	}

	const cols = $derived(Math.max(1, Math.floor((width + GAP) / (MIN_TILE + GAP))));
	const tile = $derived(width > 0 ? (width - GAP * (cols - 1)) / cols : MIN_TILE);

	type Row =
		| { kind: 'month'; label: string; count: number; top: number }
		| { kind: 'photos'; items: GridPhoto[]; top: number };

	const monthName = $derived(
		new Intl.DateTimeFormat('en', withYear ? { month: 'long', year: 'numeric', timeZone: 'UTC' } : { month: 'long', timeZone: 'UTC' })
	);

	function monthOf(p: GridPhoto): string {
		return p.taken ? monthName.format(new Date(p.taken)) : text.noDate;
	}

	const layout = $derived.by(() => {
		const rows: Row[] = [];
		let top = 0;
		let i = 0;
		while (i < photos.length) {
			const label = monthOf(photos[i]);
			let j = i;
			while (j < photos.length && monthOf(photos[j]) === label) j++;
			rows.push({ kind: 'month', label, count: j - i, top });
			top += MONTH;
			for (let k = i; k < j; k += cols) {
				rows.push({ kind: 'photos', items: photos.slice(k, Math.min(k + cols, j)), top });
				top += tile + GAP;
			}
			i = j;
		}
		return { rows, total: top };
	});

	// The rows on screen: found by halving, not by walking the list.
	const visible = $derived.by(() => {
		const rows = layout.rows;
		const from = scrollTop - headerHeight - OVERSCAN;
		const to = scrollTop - headerHeight + height + OVERSCAN;
		let lo = 0;
		let hi = rows.length;
		while (lo < hi) {
			const mid = (lo + hi) >> 1;
			if (rows[mid].top + MONTH + tile < from) lo = mid + 1;
			else hi = mid;
		}
		const out: Row[] = [];
		for (let r = lo; r < rows.length && rows[r].top < to; r++) out.push(rows[r]);
		return out;
	});
</script>

<div
	class="scroller"
	bind:clientHeight={height}
	onscroll={(e) => (scrollTop = (e.currentTarget as HTMLElement).scrollTop)}
>
	<div class="head" bind:clientHeight={headerHeight}>{@render header?.()}</div>
	<div class="grid" class:selecting bind:clientWidth={width} style:height="{layout.total}px">
		{#each visible as row (row.top)}
			{#if row.kind === 'month'}
				<h3 style:transform="translateY({row.top}px)">
					{row.label} <span>{row.count.toLocaleString('en')}</span>
				</h3>
			{:else}
				{#each row.items as p, c (p.hash)}
					<button
						class="tile"
						class:on={selected?.has(p.hash)}
						style:width="{tile}px"
						style:height="{tile}px"
						style:transform="translate({c * (tile + GAP)}px, {row.top}px)"
						onclick={(e) => click(e, p)}
						oncontextmenu={(e) => context(e, p.hash)}
						draggable={!!window.shell}
						ondragstart={(e) => drag(e, p.hash)}
					>
						<img
							src="/api/thumb/{p.hash}"
							alt={p.name}
							style:transform={turned(p)}
							loading="lazy"
							decoding="async"
							onload={(e) => (e.currentTarget as HTMLElement).classList.add('in')}
						/>
						{#if p.video}<span class="video"><Play size={14} fill="currentColor" /></span>{/if}
						{#if p.missing}<span class="gone" title={text.notOnDisk}><ImageOff size={14} /></span>{/if}
						{#if onselect}
							<span class="pick">{#if selected?.has(p.hash)}<CircleCheck size={20} />{:else}<Circle size={20} />{/if}</span>
						{/if}
					</button>
				{/each}
			{/if}
		{/each}
	</div>
</div>
{#if opener.menu}<ContextMenu items={folderItems(opener.menu.path, true)} anchor={opener.menu.anchor} onclose={opener.close} />{/if}

<style>
	.scroller {
		height: 100%;
		overflow-y: auto;
		padding: 0 var(--space-40) var(--space-40);
	}
	.grid {
		position: relative;
	}
	h3,
	.tile {
		position: absolute;
		top: 0;
		left: 0;
	}
	h3 {
		height: var(--space-40);
		padding-top: 16px;
		font-size: var(--text-body);
		font-weight: var(--weight-semibold);
		color: var(--fg);
		white-space: nowrap;
	}
	h3 span {
		margin-left: 8px;
		font-size: var(--text-callout);
		font-weight: var(--weight-regular);
		color: var(--faint);
	}
	.tile {
		border-radius: var(--radius-6);
		overflow: hidden;
		background: var(--line);
	}
	img {
		width: 100%;
		height: 100%;
		object-fit: cover;
		display: block;
		opacity: 0;
		transition:
			opacity 160ms ease,
			scale 120ms ease;
	}
	img:global(.in) {
		opacity: 1;
	}
	.video {
		position: absolute;
		right: var(--space-6);
		bottom: var(--space-6);
		color: var(--on-photo);
		filter: drop-shadow(0 1px 2px rgba(0, 0, 0, 0.5));
	}
	/* Picking (K1): the circle shows on a tile under the pointer, on every
	   tile once one is picked; a picked one shrinks in an accent frame. */
	.pick {
		position: absolute;
		top: var(--space-8);
		left: var(--space-8);
		display: grid;
		color: var(--on-photo);
		filter: drop-shadow(0 1px 2px rgba(0, 0, 0, 0.5));
		opacity: 0;
		transition: opacity 120ms ease;
	}
	.tile:hover .pick,
	.selecting .pick {
		opacity: 1;
	}
	.tile.on {
		background: var(--accent-soft);
	}
	.tile.on img {
		scale: 0.84;
		border-radius: var(--radius-6);
	}
	.tile.on .pick {
		color: var(--accent);
		background: var(--bg);
		border-radius: var(--radius-full);
		filter: none;
	}
	/* A photo whose file is gone: its thumbnail faded, and a badge. */
	.tile:has(.gone) img:global(.in) {
		opacity: 0.35;
		filter: grayscale(1);
	}
	.gone {
		position: absolute;
		left: var(--space-6);
		bottom: var(--space-6);
		width: var(--space-24);
		height: var(--space-24);
		display: grid;
		place-items: center;
		border-radius: var(--radius-full);
		background: var(--surface);
		border: 1px solid var(--border);
		color: var(--warn);
	}
	:global(.narrow) .scroller {
		padding: 0 var(--space-24) var(--space-24);
	}
</style>
