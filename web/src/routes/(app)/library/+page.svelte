<!-- homewend — Copyright (C) 2026 Ron Alder -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { app } from '#lib/app.svelte.js';
	import { getPhotos, turnLeft, type GridPhoto } from '#lib/api.js';
	import { placeOf, urlOf } from '#lib/places.js';
	import { text } from '#lib/strings.js';
	import Grid from '#lib/components/Grid.svelte';
	import EmptyLibrary from '#lib/screens/EmptyLibrary.svelte';
	import Viewer from '#lib/components/Viewer.svelte';
	import PrimaryButton from '#lib/components/PrimaryButton.svelte';
	import TakeOut from '#lib/components/TakeOut.svelte';
	import GiveDate from '#lib/components/GiveDate.svelte';
	import SecondaryButton from '#lib/components/SecondaryButton.svelte';
	import { Calendar, CircleCheck, CircleAlert, Loader, RotateCcw, X } from '@lucide/svelte';

	let open = $state<number | null>(null);

	const place = $derived(placeOf(page.url));
	let photos = $state<GridPhoto[]>([]);
	let loaded = $state(false);
	const title = $derived(
		place.kind === 'year'
			? place.label
			: place.kind === 'album'
				? place.name
				: place.kind === 'missing'
					? loaded ? text.missingTitle(photos.length) : text.notOnDisk
					: text.allPhotos
	);
	let takingOut = $state(false);
	let reload = $state(0);

	// The photos picked (K1), by hash; the last one picked, for Shift.
	let selected = $state(new Set<string>());
	let anchor = -1;
	let dating = $state(false);
	// A photo given a date in the viewer: it is opened again where the list,
	// loaded again, now has it.
	let reopen: string | null = null;
	// Another page, or the list loaded again: nothing picked.
	$effect(() => {
		void place;
		void reload;
		selected = new Set();
		anchor = -1;
	});
	function select(i: number, range: boolean) {
		const next = new Set(selected);
		if (range && anchor >= 0) {
			for (let k = Math.min(anchor, i); k <= Math.max(anchor, i); k++) next.add(photos[k].hash);
		} else if (!next.delete(photos[i].hash)) next.add(photos[i].hash);
		anchor = i;
		selected = next;
	}
	const picked = $derived(photos.filter((p) => selected.has(p.hash)));
	async function turnPicked() {
		for (const p of picked) p.turn = (await turnLeft(p.hash)).turn;
	}
	// Esc closes the date's dialog, else lets the photos go; the viewer has its own.
	function key(e: KeyboardEvent) {
		if (e.key !== 'Escape' || open !== null) return;
		if (dating) dating = false;
		else if (selected.size > 0) selected = new Set();
	}


	// What the download says of this page: all here, missing, or arriving.
	const where = $derived.by(() => {
		const years = Object.values(app.jobs).flatMap((j) => j.years.map((y) => ({ ...y, running: j.running, finished: j.finished })));
		const mine =
			place.kind === 'year'
				? years.filter((y) => y.year === (place.label === text.noDate ? '' : place.label))
				: place.kind === 'all'
					? years
					: [];
		if (mine.length === 0) return null;
		const arrived = mine.reduce((n, y) => n + y.arrived, 0);
		const of = mine.reduce((n, y) => n + y.of, 0);
		if (arrived >= of) return { kind: 'ok', label: text.allHere };
		if (mine.some((y) => y.running)) return { kind: 'arriving', label: text.arriving };
		if (mine.some((y) => y.finished)) return { kind: 'missing', label: text.missing((of - arrived).toLocaleString('en')) };
		return null;
	});
	// What the page holds: how many photos and how many videos (L1); whose
	// they are is the sidebar's. The photos with no date say why they have none.
	const subtitle = $derived.by(() => {
		const n = photos.length.toLocaleString('en');
		if (place.kind === 'missing') return text.missingDetail;
		if (place.kind === 'year' && place.label === text.noDate) return text.noDateSub(n);
		const videos = photos.filter((p) => p.video).length;
		return text.photosAndVideos(photos.length - videos, videos);
	});

	// The whole list, a chunk at a time: the grid needs every photo's month to
	// lay itself out, and the first chunk shows while the rest arrives.
	const CHUNK = 5000;
	$effect(() => {
		const p = place;
		void reload;
		let stopped = false;
		photos = [];
		loaded = false;
		(async () => {
			const q =
				p.kind === 'year'
					? p.label === text.noDate
						? { noDate: true }
						: { year: p.label }
					: p.kind === 'album'
						? { album: p.name }
						: p.kind === 'missing'
							? { missing: true }
							: {};
			for (let offset = 0; !stopped; offset += CHUNK) {
				const chunk = await getPhotos({ ...q, offset, limit: CHUNK });
				if (stopped) return;
				photos = offset === 0 ? chunk : photos.concat(chunk);
				if (chunk.length < CHUNK) break;
			}
			loaded = true;
			if (reopen) {
				const i = photos.findIndex((x) => x.hash === reopen);
				open = i >= 0 ? i : null;
				reopen = null;
			}
			// None left not on the disk, all taken out or back: the library.
			if (p.kind === 'missing' && photos.length === 0) goto(urlOf({ kind: 'all' }));
		})();
		return () => (stopped = true);
	});
</script>

{#if app.overview?.gone}
	<!-- The strip under the titlebar says it (G4); there is nothing to show. -->
{:else if app.overview && app.overview.total === 0}
	<EmptyLibrary />
{:else}
	<!-- The bar over the grid, the grid in what is left. -->
	<div class="lib">
	{#if selected.size > 0}
		<div class="selbar">
			<button class="close" aria-label={text.clearSelection} onclick={() => (selected = new Set())}><X size={20} /></button>
			<b>{text.selected(selected.size)}</b>
			<SecondaryButton small onclick={turnPicked}><RotateCcw size={14} />{text.rotateLeft}</SecondaryButton>
			<SecondaryButton small onclick={() => (dating = true)}><Calendar size={14} />{text.changeDateEllipsis}</SecondaryButton>
		</div>
	{/if}
	<div class="fill">
	<Grid {photos} withYear={place.kind !== 'year'} onopen={(i) => (open = i)} {selected} onselect={select}>
		{#snippet header()}
			<div class="head">
				<h1>{title}</h1>
				<p>
					{subtitle}{loaded ? '' : '…'}
					{#if where}
						<span class="state {where.kind}">
							·
							{#if where.kind === 'ok'}<CircleCheck size={14} />{:else if where.kind === 'missing'}<CircleAlert size={14} />{:else}<Loader size={14} />{/if}
							{where.label}
						</span>
					{/if}
				</p>
				{#if place.kind === 'missing' && photos.length > 0}
					<div class="out">
						<PrimaryButton onclick={() => (takingOut = true)}>{text.takeOutAll}</PrimaryButton>
						<span>{text.takeOutNote}</span>
					</div>
				{/if}
			</div>
		{/snippet}
	</Grid>
	</div>
	</div>
	<Viewer {photos} bind:index={open} ontakenout={() => reload++} ondated={(hash) => ((reopen = hash), reload++)} />
	{#if dating}
		<GiveDate
			photos={picked}
			onclose={(moved) => {
				dating = false;
				if (moved) reload++;
			}}
		/>
	{/if}
	{#if takingOut}
		<TakeOut
			count={photos.length}
			onclose={(taken) => {
				takingOut = false;
				if (taken) reload++;
			}}
		/>
	{/if}
{/if}

<svelte:window onkeydown={key} />

<style>
	.lib {
		height: 100%;
		display: flex;
		flex-direction: column;
	}
	.fill {
		flex: 1;
		min-height: 0;
	}
	/* K1: the library's bar while photos are picked, over the grid. */
	.selbar {
		display: flex;
		align-items: center;
		gap: var(--space-12);
		padding: var(--space-16) var(--space-40) var(--space-16) var(--space-24);
		border-bottom: 1px solid var(--line);
		background: var(--bg);
	}
	.selbar b {
		flex: 1;
		font-size: var(--text-title-2);
		font-weight: var(--weight-bold);
		color: var(--fg);
	}
	.close {
		width: var(--space-32);
		height: var(--space-32);
		display: grid;
		place-items: center;
		border-radius: var(--radius-8);
		color: var(--fg);
	}
	.close:hover {
		background: var(--soft);
	}
	.head {
		padding: var(--space-32) 0 var(--space-8);
	}
	h1 {
		font-size: var(--text-display);
		font-weight: var(--weight-bold);
		color: var(--fg);
	}
	p {
		margin-top: var(--space-8);
		font-size: var(--text-body);
		color: var(--muted);
	}
	.out {
		display: flex;
		gap: var(--space-16);
		align-items: center;
		margin-top: var(--space-16);
	}
	.out span {
		font-size: var(--text-callout);
		color: var(--muted);
	}
	.state {
		display: inline-flex;
		align-items: center;
		gap: var(--space-4);
		font-weight: var(--weight-medium);
	}
	.state.ok {
		color: var(--ok);
	}
	.state.missing {
		color: var(--warn);
	}
	.state.arriving {
		color: var(--accent);
	}
	:global(.narrow) h1 {
		font-size: var(--text-large-title);
	}
</style>
