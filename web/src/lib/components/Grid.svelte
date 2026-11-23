<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- The photos, by month, newest first. Only the rows on screen exist: the
     grid has to scroll through hundreds of thousands of tiles. -->
<script lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import { tick, untrack, type Snippet } from 'svelte';
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
		withYear = true,
		view,
		settled = true
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
		/** What chose these photos: another, and the grid starts again at its top. */
		view?: unknown;
		/** The list is whole: what comes after has arrived, what comes before is still being read. */
		settled?: boolean;
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

	// Its month, "2026-11", as its date begins: named once per heading.
	const monthOf = (p: GridPhoto) => p.taken?.slice(0, 7) ?? '';

	const layout = $derived.by(() => {
		const rows: Row[] = [];
		let top = 0;
		let i = 0;
		while (i < photos.length) {
			const month = monthOf(photos[i]);
			const label = month ? monthName.format(new Date(photos[i].taken!)) : text.noDate;
			let j = i;
			while (j < photos.length && monthOf(photos[j]) === month) j++;
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
	// The tiles on screen, one list keyed by photo: a photo that moves to
	// another row keeps its tile and its picture, it is not drawn again.
	const tiles = $derived(
		visible.flatMap((r) => (r.kind === 'photos' ? r.items.map((p, c) => ({ p, x: c * (tile + GAP), y: r.top })) : []))
	);

	// The photos come again with some new (a phone's backup): the new ones
	// fade in slowly, and the photo at the top of the screen stays where it
	// is, nothing moving under the finger. At the very top the new ones show.
	let scroller: HTMLElement;
	let drawn: { rows: Row[]; total: number } = { rows: [], total: 0 };
	let before: GridPhoto[] = [];
	let arrived = $state(new Set<string>());
	$effect(() => {
		drawn = layout;
	});
	let viewed: unknown = undefined;
	let wasSettled = false;
	$effect.pre(() => {
		const now = photos;
		const by = view;
		const whole = settled;
		untrack(() => {
			const was = before;
			before = now;
			const reading = !wasSettled;
			wasSettled = whole;
			if (by !== viewed) {
				// Other photos of the same library: none of them arrived.
				viewed = by;
				arrived = new Set();
				if (scroller) scroller.scrollTop = 0;
				return;
			}
			if (reading || was.length === 0 || now === was || was.every((p, i) => now[i]?.hash === p.hash)) return;
			const known = new Set(was.map((p) => p.hash));
			arrived = new Set(now.filter((p) => !known.has(p.hash)).map((p) => p.hash));
			if (!scroller || scroller.scrollTop === 0) return;
			const top = scroller.scrollTop - headerHeight;
			const row = drawn.rows.find((r) => r.kind === 'photos' && r.top + tile > top);
			if (row?.kind !== 'photos') return;
			const hash = row.items[0].hash;
			const offset = row.top - top;
			tick().then(() => {
				const moved = layout.rows.find((r) => r.kind === 'photos' && r.items.some((p) => p.hash === hash));
				if (moved) scroller.scrollTop = moved.top - offset + headerHeight;
			});
		});
	});
</script>

<div
	class="scroller"
	bind:this={scroller}
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
			{/if}
		{/each}
		{#each tiles as { p, x, y } (p.hash)}
			<button
				class="tile"
				class:on={selected?.has(p.hash)}
				class:arrived={arrived.has(p.hash)}
				style:width="{tile}px"
				style:height="{tile}px"
				style:transform="translate({x}px, {y}px)"
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
				{#if p.video}<span class="video"><Icon name="play-fill" size={14} /></span>{/if}
				{#if p.missing}<span class="gone" title={text.notOnDisk}><Icon name="file-earmark-x" size={14} /></span>{/if}
				{#if onselect}
					<span class="pick">{#if selected?.has(p.hash)}<Icon name="check-circle" size={20} />{:else}<Icon name="circle" size={20} />{/if}</span>
				{/if}
			</button>
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
			opacity var(--motion-base) var(--ease-out),
			scale var(--motion-fast) var(--ease-out);
	}
	img:global(.in) {
		opacity: 1;
	}
	/* A photo that has just arrived in the library fades in slowly. */
	.arrived img {
		transition-duration: var(--motion-ambient), var(--motion-fast);
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
		transition: opacity var(--motion-fast) var(--ease-out);
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
