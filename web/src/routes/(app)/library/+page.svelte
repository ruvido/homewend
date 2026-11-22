<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { untrack } from 'svelte';
	import { app, refresh } from '#lib/app.svelte.js';
	import { getPhotos, mark, turnLeft, type GridPhoto, type MarkAct } from '#lib/api.js';
	import { isView } from '#lib/library.js';
	import Dialog from '#lib/components/Dialog.svelte';
	import { placeOf, urlOf } from '#lib/places.js';
	import { text } from '#lib/strings.js';
	import Grid from '#lib/components/Grid.svelte';
	import EmptyLibrary from '#lib/screens/EmptyLibrary.svelte';
	import Viewer from '#lib/components/Viewer.svelte';
	import PrimaryButton from '#lib/components/PrimaryButton.svelte';
	import TakeOut from '#lib/components/TakeOut.svelte';
	import GiveDate from '#lib/components/GiveDate.svelte';
	import SecondaryButton from '#lib/components/SecondaryButton.svelte';

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
					: place.kind === 'archive'
						? text.archive
						: place.kind === 'trash'
							? text.trash
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
		markFailed = '';
	});
	// Another photo in the viewer: what failed was the last one's.
	$effect(() => {
		void open;
		markFailed = '';
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
	// Archive and Delete (Q1, Q6), Unarchive for photos all archived (Q4,
	// and in an album); in the Trash, Restore (Q5). The photos not on the
	// disk have none. Rotate and Change date… are not offered in the
	// Archive and the Trash, in the bar and the viewer alike.
	const archive: MarkAct = { icon: 'archive', label: text.archive, state: 'archived', on: true };
	const unarchive: MarkAct = { icon: 'archive', label: text.unarchive, state: 'archived', on: false };
	const del: MarkAct = { icon: 'trash', label: text.deleteIt, state: 'trashed', on: true, ask: true };
	const restore: MarkAct = { icon: 'arrow-counterclockwise', label: text.restore, state: 'trashed', on: false };
	const actsOf = (ps: GridPhoto[]) =>
		place.kind === 'trash' ? [restore] : place.kind === 'missing' ? [] : [ps.every((p) => p.archived) ? unarchive : archive, del];
	const editable = $derived(place.kind !== 'archive' && place.kind !== 'trash');
	// A photo archived, deleted or back leaves this list (an album keeps
	// the archived): the list is loaded again, the viewer closed.
	let asking = $state<{ act: MarkAct; hashes: string[] } | null>(null);
	let markFailed = $state('');
	async function act(a: MarkAct, hashes: string[]) {
		if (a.ask && !asking) return void (asking = { act: a, hashes });
		markFailed = '';
		try {
			await mark(hashes, a.state, a.on);
		} catch {
			markFailed = text.markFailed;
			return;
		}
		asking = null;
		open = null;
		reload++;
		await refresh();
	}
	// Esc closes the date's dialog, else lets the photos go; the viewer has its own.
	function key(e: KeyboardEvent) {
		if (e.key !== 'Escape') return;
		if (asking) return void (asking = null);
		if (open !== null) return;
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
		if (place.kind === 'archive') return text.archiveSub(photos.length);
		if (place.kind === 'trash') return text.trashSub(photos.length);
		if (place.kind === 'year' && place.label === text.noDate) return text.noDateSub(n);
		const videos = photos.filter((p) => p.video).length;
		return text.photosAndVideos(photos.length - videos, videos);
	});

	// The library changed under the page (a phone's backup, a download): the
	// list is read again where it is, nothing picked let go, the viewer kept
	// on its photo. One change during a reading is read after it.
	let live = $state(0);
	let reading = false;
	let stale = false;
	let first = true;
	$effect(() => {
		void app.libraryMoved;
		if (first) return void (first = false);
		untrack(() => (reading ? (stale = true) : live++));
	});

	// The whole list, a chunk at a time: the grid needs every photo's month to
	// lay itself out, and the first chunk shows while the rest arrives.
	const CHUNK = 5000;
	// The place whose photos are shown, to tell a new place from a reload.
	let shown = '';
	$effect(() => {
		const p = place;
		void reload;
		void live;
		let stopped = false;
		// Another place starts empty, at its top: its title over the last
		// place's photos would say a count, a take-out, a photo not its own.
		// The same place loaded again keeps its photos until all have come:
		// a first chunk alone would shorten the grid under the finger.
		const here = urlOf(p);
		const again = here === shown;
		if (!again) ((photos = []), (shown = here), (loaded = false));
		reading = true;
		(async () => {
			const q =
				p.kind === 'year'
					? p.label === text.noDate
						? { noDate: true }
						: { year: p.label }
					: p.kind === 'album'
						? { album: p.name }
						: isView(p.kind)
							? { view: p.kind }
							: {};
			try {
				let got: GridPhoto[] = [];
				for (let offset = 0; !stopped; offset += CHUNK) {
					const chunk = await getPhotos({ ...q, offset, limit: CHUNK });
					if (stopped) return;
					got = offset === 0 ? chunk : got.concat(chunk);
					if (!again) photos = got;
					if (chunk.length < CHUNK) break;
				}
				// The photo in the viewer, found again in the new list.
				const viewing = open !== null ? photos[open]?.hash : undefined;
				photos = got;
				if (viewing && !reopen) reopen = viewing;
			} catch {
				// No answer: nothing shown is better than another list.
				if (!stopped) ((photos = []), (reading = false));
				return;
			}
			loaded = true;
			reading = false;
			if (stale) ((stale = false), live++);
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
{:else if app.overview && app.overview.total + app.overview.archived + app.overview.trashed === 0}
	<EmptyLibrary />
{:else}
	<!-- The bar over the grid, the grid in what is left. -->
	<div class="lib">
	{#if selected.size > 0}
		<div class="selbar">
			<button class="close" aria-label={text.clearSelection} onclick={() => (selected = new Set())}><Icon name="x-lg" size={20} /></button>
			<b>{text.selected(selected.size)}</b>
			{#if markFailed && !asking}<span class="failed">{markFailed}</span>{/if}
			{#if editable}
				<SecondaryButton small onclick={turnPicked}><Icon name="arrow-counterclockwise" size={14} />{text.rotateLeft}</SecondaryButton>
				<SecondaryButton small onclick={() => (dating = true)}><Icon name="calendar" size={14} />{text.changeDateEllipsis}</SecondaryButton>
			{/if}
			{#each actsOf(picked) as a (a.label)}
				<SecondaryButton small onclick={() => act(a, picked.map((p) => p.hash))}><Icon name={a.icon} size={14} />{a.label}</SecondaryButton>
			{/each}
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
							{#if where.kind === 'ok'}<Icon name="check-circle" size={14} />{:else if where.kind === 'missing'}<Icon name="exclamation-circle" size={14} />{:else}<Icon name="hourglass-split" size={14} />{/if}
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
	<Viewer {photos} bind:index={open} {actsOf} {editable} held={asking !== null} failed={markFailed} onact={(a, hash) => act(a, [hash])} ontakenout={() => reload++} ondated={(hash) => ((reopen = hash), (open = null), reload++)} />
	{#if asking}
		<!-- Q2: Delete asks first; the photos go to the Trash, never off the disk. -->
		<Dialog title={text.trashAsk(asking.hashes.length)}>
			<p>{text.trashAskLead}</p>
			{#if markFailed}<p class="failed">{markFailed}</p>{/if}
			{#snippet actions()}
				<SecondaryButton onclick={() => (asking = null)}>{text.cancel}</SecondaryButton>
				<PrimaryButton danger onclick={() => asking && act(asking.act, asking.hashes)}>{text.moveToTrash}</PrimaryButton>
			{/snippet}
		</Dialog>
	{/if}
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
	.failed {
		color: var(--err);
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
