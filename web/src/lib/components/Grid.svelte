<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- The photos, by month, newest first. Only the rows on screen exist: the
     grid has to scroll through hundreds of thousands of tiles. -->
<script module lang="ts">
	// The pictures drawn lately, kept as the browser decoded them: a tile that
	// comes back to the screen has its own at once, not empty and faded in.
	// shortcut: the last 1,000, ten screens; some 10 MB of files, and what of
	// them the browser keeps decoded. Raised if going back further ever shows.
	const KEPT = 1000;
	const kept = new Map<string, HTMLImageElement>();
	function keep(src: string) {
		// One of its own: the tile's would hold the whole tile with it.
		const img = kept.get(src) ?? Object.assign(new Image(), { src });
		kept.delete(src);
		kept.set(src, img);
		if (kept.size > KEPT) kept.delete(kept.keys().next().value!);
	}
	// Said once, as the tile comes: one loading now fades in to the end.
	function shown(img: HTMLImageElement) {
		if (kept.has(img.src)) img.classList.add('in', 'known');
		else img.decoding = 'async';
	}
</script>

<script lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import { tick, untrack, type Snippet } from 'svelte';
	import { pathOf, pictureOf, turned, type GridPhoto } from '#lib/api.js';
	import ContextMenu, { folderItems } from './ContextMenu.svelte';
	import { Opener } from '#lib/menu.svelte.js';
	import { colourOf, initials } from '#lib/library.js';
	import { swap } from '#lib/motion.js';
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
	// Pixels drawn beyond the screen: a flick covers more than a screen in a
	// frame, and where it is going no tile may be missing.
	const AHEAD = 2400;
	const BEHIND = 600;

	let width = $state(0);
	let height = $state(0);
	let scrollTop = $state(0);
	let down = $state(true);
	let headerHeight = $state(0);

	// A photo's menu, at the end of its tile: its folder, its path.
	const opener = new Opener<{ path: string }>();
	function context(e: MouseEvent) {
		const on = (e.target as Element).closest<HTMLElement>('.tile');
		const p = photoAt(e);
		if (on && p) opener.open(e, () => pathOf(p), on);
	}
	// Dragged out of the window, a photo is its file, handed to the system's
	// drag as the file manager hands one: the original goes where it is dropped.
	function drag(e: DragEvent, p: GridPhoto) {
		e.preventDefault();
		pathOf(p).then(({ path }) => window.shell?.dragFile(path), () => {});
	}

	// The tile under the pointer.
	let over = $state('');
	const selecting = $derived(!!selected && selected.size > 0);
	// A click opens the photo; on its circle, or while picking, or with
	// Shift, it picks it instead.
	const idAt = (e: Event) => (e.target as Element).closest<HTMLElement>('.tile')?.dataset.id ?? '';
	const photoAt = (e: Event) => {
		const id = idAt(e);
		return id ? photos.find((p) => p.id === id) : undefined;
	};
	function click(e: MouseEvent) {
		const id = idAt(e);
		if (!id) return;
		const i = photos.findIndex((p) => p.id === id);
		if (onselect && (selecting || e.shiftKey || (e.target as Element).closest('.pick'))) onselect(i, e.shiftKey);
		else onopen?.(i);
	}

	// Whose a photo of two libraries is (W1): the letter, once a person.
	const letters = new Map<string, string>();
	function letterOf(profile: string) {
		let letter = letters.get(profile);
		if (letter === undefined) letters.set(profile, (letter = initials(profile)));
		return letter;
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

	// The rows on screen: found by halving, not by walking the list. What is
	// drawn follows the first and the last of them, so a scroll within a row
	// draws nothing, and one past it only the rows that come and go.
	function rowAt(y: number): number {
		let lo = 0;
		let hi = layout.rows.length;
		while (lo < hi) {
			const mid = (lo + hi) >> 1;
			if (layout.rows[mid].top + MONTH + tile < y) lo = mid + 1;
			else hi = mid;
		}
		return lo;
	}
	const at = $derived(scrollTop - headerHeight);
	const first = $derived(rowAt(at - (down ? BEHIND : AHEAD)));
	const last = $derived(rowAt(at + height + (down ? AHEAD : BEHIND)));
	const visible = $derived(layout.rows.slice(first, last + 1));
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
			if (reading || was.length === 0 || now === was || was.every((p, i) => now[i]?.id === p.id)) return;
			const known = new Set(was.map((p) => p.id));
			arrived = new Set(now.filter((p) => !known.has(p.id)).map((p) => p.id));
			if (!scroller || scroller.scrollTop === 0) return;
			const top = scroller.scrollTop - headerHeight;
			const row = drawn.rows.find((r) => r.kind === 'photos' && r.top + tile > top);
			if (row?.kind !== 'photos') return;
			const id = row.items[0].id;
			const offset = row.top - top;
			tick().then(() => {
				const moved = layout.rows.find((r) => r.kind === 'photos' && r.items.some((p) => p.id === id));
				if (moved) scroller.scrollTop = moved.top - offset + headerHeight;
			});
		});
	});
</script>

<div
	class="scroller"
	bind:this={scroller}
	bind:clientHeight={height}
	onscroll={(e) => {
		const to = e.currentTarget.scrollTop;
		if (to !== scrollTop) down = to > scrollTop || to === 0;
		scrollTop = to;
	}}
>
	<div class="head" bind:clientHeight={headerHeight}>{@render header?.()}</div>
	<!-- One listener for them all: a tile is as little as it can be. -->
	<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
	<div
		class="grid"
		bind:clientWidth={width}
		style:height="{layout.total}px"
		style:--cols={cols}
		style:--tile="{tile}px"
		onpointerover={(e) => (over = idAt(e))}
		onpointerleave={(e) => {
			// A finger leaves before its click: the circle stays to be hit.
			if (e.pointerType !== 'touch') over = '';
		}}
		onclick={click}
		oncontextmenu={context}
		ondragstart={(e) => {
			const p = photoAt(e);
			if (p) drag(e, p);
		}}
		onloadcapture={(e) => {
			const img = e.target as HTMLImageElement;
			img.classList.add('in');
			keep(img.src);
		}}
	>
		{#each visible as row (row.top)}
			{#if row.kind === 'month'}
				<h3 style:transform="translateY({row.top}px)">
					{row.label} <span>{row.count.toLocaleString('en')}</span>
				</h3>
			{/if}
		{/each}
		<!-- A row comes and goes whole: its tiles need no place of their own. -->
		{#each visible as row (row.top)}
			{#if row.kind === 'photos'}
				<div class="row" style:transform="translateY({row.top}px)">
					{#each row.items as p (p.id)}
						<button
							class="tile"
							class:on={selected?.has(p.id)}
							class:arrived={arrived.has(p.id)}
							data-id={p.id}
							draggable={!!window.shell}
						>
							<img
								src={pictureOf(p)}
								alt={p.name}
								style:transform={turned(p)}
								use:shown
							/>
							{#if p.video}<span class="video"><Icon name="play-fill" size={14} /></span>{/if}
							{#if p.shared}<span class="avatar whose" style:background={colourOf(p.of ?? 0)} title={p.profile}>{letterOf(p.profile)}</span>{/if}
							{#if p.missing}<span class="gone" title={text.notOnDisk}><Icon name="file-earmark-x" size={14} /></span>{/if}
							<!-- Only where it can be seen: a tile is as little as it can be. -->
							{#if onselect && (selecting || over === p.id)}
								<span class="pick" in:swap>{#if selected?.has(p.id)}<Icon name="check-circle" size={20} />{:else}<Icon name="circle" size={20} />{/if}</span>
							{/if}
						</button>
					{/each}
				</div>
			{/if}
		{/each}
	</div>
</div>
{#if opener.menu}<ContextMenu items={folderItems(opener.menu.path, true)} anchor={opener.menu.anchor} onclose={opener.close} />{/if}

<style>
	/* Its own ground: one the compositor scrolls by itself. */
	.scroller {
		height: 100%;
		background: var(--bg);
		will-change: scroll-position;
		overflow-y: auto;
		padding: 0 var(--space-40) var(--space-40);
	}
	.grid {
		position: relative;
	}
	h3,
	.row {
		position: absolute;
		top: 0;
		left: 0;
	}
	.row {
		display: grid;
		grid-template-columns: repeat(var(--cols), var(--tile));
		grid-auto-rows: var(--tile);
		gap: var(--space-8);
		contain: layout style;
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
		position: relative;
		contain: strict;
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
	/* Seen before: no fading in again. */
	img:global(.known) {
		transition: scale var(--motion-fast) var(--ease-out);
	}
	/* A photo that has just arrived in the library fades in slowly. */
	.arrived img:not(:global(.known)) {
		transition-duration: var(--motion-ambient), var(--motion-fast);
	}
	.video {
		position: absolute;
		right: var(--space-6);
		bottom: var(--space-6);
		color: var(--on-photo);
		filter: drop-shadow(0 1px 2px rgba(0, 0, 0, 0.5));
	}
	.whose {
		width: var(--space-20);
		height: var(--space-20);
		position: absolute;
		right: var(--space-6);
		bottom: var(--space-6);
	}
	.video:has(+ .whose) {
		right: calc(2 * var(--space-6) + var(--space-20));
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
