<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { untrack } from 'svelte';
	import { app, refresh } from '#lib/app.svelte.js';
	import { copiesOf, getPhotos, holds, mark, patched, photosIn, turnLeft, type GridPhoto, type MarkAct, type PhotoKey, type Shown } from '#lib/api.js';
	import { isView } from '#lib/library.js';
	import Dialog from '#lib/components/Dialog.svelte';
	import { placeOf, urlOf } from '#lib/places.js';
	import { text } from '#lib/strings.js';
	import Grid from '#lib/components/Grid.svelte';
	import EmptyLibrary from '#lib/screens/EmptyLibrary.svelte';
	import Viewer from '#lib/components/Viewer.svelte';
	import PrimaryButton from '#lib/components/PrimaryButton.svelte';
	import TakeOut from '#lib/components/TakeOut.svelte';
	import EmptyTrash from '#lib/components/EmptyTrash.svelte';
	import GiveDate from '#lib/components/GiveDate.svelte';
	import SecondaryButton from '#lib/components/SecondaryButton.svelte';
	import LibraryFilter from '#lib/components/LibraryFilter.svelte';
	import { finder } from '#lib/finder.svelte.js';
	import { badgesOf, countBadges, isFiltering, noFilter, search } from '#lib/filter.js';

	let open = $state<number | null>(null);

	const place = $derived(placeOf(page.url));
	// The place's photos, and those the search keeps of them: the grid, the
	// viewer and the selection know only these.
	// Raw: a hundred thousand photos are replaced whole, never each watched.
	let list = $state.raw<GridPhoto[]>([]);
	let loaded = $state(false);
	const filter = $derived(finder.filter);
	// Counted once the list is whole.
	const counts = $derived(loaded ? countBadges(list) : {});
	const badges = $derived(badgesOf(counts, filter.badges));
	const photos = $derived(search(list, filter, badges));
	const dated = $derived(!(place.kind === 'year' && place.label === text.noDate));
	// Every person unchecked shows nothing, as the checkboxes say: not an empty library.
	const nobody = $derived(!!app.overview?.profiles.length && app.overview.profiles.every((p) => app.settings.hidden?.includes(p)));
	const empty = $derived(!app.overview || app.overview.gone || (!nobody && app.overview.total + app.overview.archived + app.overview.trashed === 0));
	// The photos not on the disk are all taken out, or none: no search there.
	$effect(() => {
		finder.offered = !empty && place.kind !== 'missing';
		return () => (finder.offered = false);
	});
	// The first and the last day; with none, today by this computer's clock.
	function bounds() {
		const now = new Date();
		const today = new Date(now.getTime() - now.getTimezoneOffset() * 60000).toISOString();
		const at = (p?: GridPhoto) => (p?.taken ?? today).slice(0, 10);
		return { start: at(list.findLast((p) => p.taken)), end: at(list.find((p) => p.taken)) };
	}
	const title = $derived(
		place.kind === 'year'
			? place.label
			: place.kind === 'album'
				? place.name
				: place.kind === 'missing'
					? loaded ? text.missingTitle(list.length) : text.notOnDisk
					: place.kind === 'archive'
						? text.archive
						: place.kind === 'trash'
							? text.trash
							: place.kind === 'favorites'
								? text.favorites
								: text.allPhotos
	);
	let takingOut = $state(false);
	// The photos the Trash showed as Empty was asked: one put in it meanwhile is not deleted.
	let emptying = $state.raw<PhotoKey[] | null>(null);
	let emptyBusy = $state(false);
	let reload = $state(0);
	// The people hidden: another of them is another list, as a reload is.
	const hidden = $derived((app.settings.hidden ?? []).join('/'));

	// The photos picked (K1), by id; the last one picked, for Shift.
	let selected = $state(new Set<string>());
	let anchor = -1;
	let dating = $state(false);
	// A photo given a date in the viewer: it is opened again where the list,
	// loaded again, now has it.
	let reopen: string | null = null;
	// Another page or search, or the list loaded again: nothing picked.
	$effect(() => {
		void place;
		void reload;
		void hidden;
		void filter;
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
			for (let k = Math.min(anchor, i); k <= Math.max(anchor, i); k++) next.add(photos[k].id);
		} else if (!next.delete(photos[i].id)) next.add(photos[i].id);
		anchor = i;
		selected = next;
	}
	const picked = $derived(photos.filter((p) => selected.has(p.id)));
	// What the bar acts on: a tile of two people is both their copies.
	const pickedCopies = $derived(copiesOf(picked));
	async function turnPicked() {
		// Shown a few times a second: each showing lays the whole grid out.
		const turns = new Map<string, Shown>();
		let shownAt = 0;
		try {
			for (const p of pickedCopies) {
				turns.set(p.id, await turnLeft(p));
				if (performance.now() - shownAt < 200) continue;
				turned(turns);
				shownAt = performance.now();
			}
		} finally {
			turned(turns);
		}
	}
	const turned = (turns: Map<string, Shown>) => (list = patched(list, (p) => (turns.has(p.id) ? { ...p, edit: undefined, ...turns.get(p.id) } : p)));
	// A heart shows at once, and is taken back if it could not be kept. One
	// after another: two quick clicks end as the last.
	let hearts: Promise<unknown> = Promise.resolve();
	function favorite(photo: GridPhoto, on: boolean) {
		const show = (favorite: boolean) => (list = patched(list, (p) => (p.id === photo.id ? { ...p, favorite } : p)));
		markFailed = '';
		show(on);
		hearts = hearts
			.then(() => mark([photo], 'favorite', on))
			.then(() => {
				// Its heart off, it leaves the Favorites, as one unarchived the Archive.
				if (on || place.kind !== 'favorites') return;
				if (open !== null && holds(photos[open], photo.id)) open = null;
				reload++;
			})
			.catch(() => {
				show(!on);
				if (open !== null && holds(photos[open], photo.id)) markFailed = text.markFailed;
			});
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
	// A photo archived, deleted or back leaves this list (an album and
	// the Favorites keep the archived): the list is loaded again, the viewer closed.
	let asking = $state.raw<{ act: MarkAct; photos: GridPhoto[] } | null>(null);
	let markFailed = $state('');
	async function act(a: MarkAct, on: GridPhoto[]) {
		if (a.ask && !asking) return void (asking = { act: a, photos: on });
		markFailed = '';
		try {
			await mark(on, a.state, a.on);
		} catch {
			markFailed = text.markFailed;
			return;
		}
		asking = null;
		open = null;
		reload++;
		await refresh();
	}
	// Esc closes the date's dialog, else lets the photos go, else closes the search; the viewer has its own.
	// Ctrl+A or ⌘A picks all the photos shown, Ctrl+F or ⌘F searches them.
	function key(e: KeyboardEvent) {
		const free = open === null && !dating && !asking && !takingOut && !emptying && place.kind !== 'missing';
		const ctrl = e.ctrlKey || e.metaKey;
		if (e.key.toLowerCase() === 'f' && ctrl && free && finder.offered) {
			e.preventDefault();
			return finder.show();
		}
		if (e.key.toLowerCase() === 'a' && ctrl && free && !(e.target instanceof HTMLInputElement)) {
			e.preventDefault();
			selected = new Set(photos.map((p) => p.id));
			return;
		}
		// A menu open takes its own Esc.
		if (e.key !== 'Escape' || e.defaultPrevented || (e.target as Element).closest?.('[role=menu]')) return;
		if (asking) {
			e.preventDefault();
			return void (asking = null);
		}
		if (emptying) return void (emptyBusy || (emptying = null));
		if (open !== null) return;
		if (dating) dating = false;
		else if (finder.open && (selected.size === 0 || e.target === finder.input)) finder.close();
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
		if (loaded && photos.length === 0 && (isFiltering(filter) || nobody)) return text.searchNone;
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
	// While it changes without pause (an import), reading rests REST times as
	// long as it took: a library takes no more than a fifth of the machine.
	const REST = 4;
	let restUntil = 0;
	let resting = false;
	let live = $state(0);
	let reading = false;
	let stale = false;
	let first = true;
	$effect(() => {
		void app.libraryMoved;
		if (first) return void (first = false);
		untrack(moved);
	});
	function moved() {
		if (reading) stale = true;
		else if (!resting) {
			resting = true;
			setTimeout(() => {
				resting = false;
				if (reading) stale = true;
				else live++;
			}, Math.max(0, restUntil - performance.now()));
		}
	}

	// The first photos, a screen and more, show while the whole list arrives:
	// the grid needs every photo's month to lay itself out.
	const FIRST = 500;
	// The place whose photos are shown, to tell a new place from a reload.
	let shown = '';
	$effect(() => {
		const p = place;
		void reload;
		void hidden;
		void live;
		let stopped = false;
		// Another place starts empty, at its top: its title over the last
		// place's photos would say a count, a take-out, a photo not its own.
		// The same place loaded again keeps its photos until all have come:
		// the first alone would shorten the grid under the finger.
		const here = urlOf(p);
		const again = here === shown;
		if (!again) ((list = []), (finder.open = false), (finder.filter = { ...noFilter }), (shown = here), (loaded = false));
		reading = true;
		(async () => {
			const q =
				p.kind === 'year'
					? p.label === text.noDate
						? { noDate: true }
						: { year: p.label }
					: p.kind === 'album'
						? { album: p.name, account: p.account }
						: isView(p.kind)
							? { view: p.kind }
							: {};
			try {
				const began = performance.now();
				let got: GridPhoto[] | undefined;
				// A view's photos are few, and read by no index: whole at once.
				if (!again && !isView(p.kind)) {
					const start = await getPhotos({ ...q, limit: FIRST });
					if (stopped) return;
					list = start;
					if (start.length < FIRST) got = start;
				}
				got ??= await getPhotos(q);
				if (stopped) return;
				const now = performance.now();
				restUntil = now + REST * (now - began);
				// The photo in the viewer, found again in the new list.
				const viewing = open !== null ? photos[open]?.id : undefined;
				list = got;
				if (viewing && !reopen) reopen = viewing;
			} catch {
				// No answer: nothing shown is better than another list.
				if (stopped) return;
				list = [];
				reading = false;
				if (stale) ((stale = false), moved());
				return;
			}
			loaded = true;
			reading = false;
			if (stale) ((stale = false), moved());
			if (reopen) {
				const i = photos.findIndex((x) => holds(x, reopen!));
				open = i >= 0 ? i : null;
				reopen = null;
			}
			// None left not on the disk, all taken out or back: the library.
			if (p.kind === 'missing' && list.length === 0) goto(urlOf({ kind: 'all' }));
		})();
		return () => (stopped = true);
	});
</script>

{#if app.overview?.gone}
	<!-- The strip under the titlebar says it (G4); there is nothing to show. -->
{:else if app.overview && empty}
	<EmptyLibrary />
{:else}
	<!-- The bar over the grid, the grid in what is left. -->
	<div class="lib">
	{#if finder.open && (dated || badges.length > 0)}
		<LibraryFilter bind:filter={finder.filter} {badges} {bounds} {dated} />
	{/if}
	{#if selected.size > 0}
		<div class="selbar">
			<button class="close" aria-label={text.clearSelection} onclick={() => (selected = new Set())}><Icon name="x-lg" size={20} /></button>
			<b>{text.selected(selected.size)}</b>
			{#if markFailed && !asking}<span class="failed">{markFailed}</span>{/if}
			{#if editable}
				<SecondaryButton small onclick={turnPicked}><Icon name="arrow-counterclockwise" size={14} />{text.rotateLeft}</SecondaryButton>
				<SecondaryButton small onclick={() => (dating = true)}><Icon name="calendar" size={14} />{text.changeDateEllipsis}</SecondaryButton>
			{/if}
			{#each actsOf(pickedCopies) as a (a.label)}
				<SecondaryButton small onclick={() => act(a, pickedCopies)}><Icon name={a.icon} size={14} />{a.label}</SecondaryButton>
			{/each}
		</div>
	{/if}
	<div class="fill">
	<Grid {photos} withYear={place.kind !== 'year'} onopen={(i) => (open = i)} {selected} onselect={select} view={filter} settled={loaded}>
		{#snippet header()}
			<div class="head">
				<div class="top">
					<h1>{title}</h1>
					{#if place.kind === 'trash' && loaded && photos.length > 0}
						<PrimaryButton danger onclick={() => (emptying = copiesOf(photos))}><Icon name="trash" size={16} />{text.emptyTrash}</PrimaryButton>
					{/if}
				</div>
				<p aria-live={loaded ? 'polite' : 'off'}>
					{subtitle}{loaded ? '' : '…'}
					{#if where}
						<span class="state {where.kind}">
							·
							{#if where.kind === 'ok'}<Icon name="check-circle" size={14} />{:else if where.kind === 'missing'}<Icon name="exclamation-circle" size={14} />{:else}<Icon name="hourglass-split" size={14} />{/if}
							{where.label}
						</span>
					{/if}
				</p>
				{#if place.kind === 'missing' && list.length > 0}
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
	<Viewer {photos} bind:index={open} {actsOf} {editable} held={asking !== null} failed={markFailed} onact={(a, p) => act(a, [p])} onturned={(id, now) => turned(new Map([[id, now]]))} onfavorite={favorite} ontakenout={() => reload++} ondated={(id) => ((reopen = id), (open = null), reload++)} />
	{#if asking}
		<!-- Q2: Delete asks first; the photos go to the Trash, never off the disk. -->
		<Dialog title={text.trashAsk(asking.photos.length)}>
			{#if asking.photos.length > photosIn(asking.photos)}<p>{text.trashAskCopies}</p>{/if}
			<p>{text.trashAskLead}</p>
			{#if markFailed}<p class="failed">{markFailed}</p>{/if}
			{#snippet actions()}
				<SecondaryButton onclick={() => (asking = null)}>{text.cancel}</SecondaryButton>
				<PrimaryButton danger onclick={() => asking && act(asking.act, asking.photos)}>{text.moveToTrash}</PrimaryButton>
			{/snippet}
		</Dialog>
	{/if}
	{#if dating}
		<GiveDate
			photos={pickedCopies}
			onclose={(moved) => {
				dating = false;
				if (moved) reload++;
			}}
		/>
	{/if}
	{#if emptying}
		<EmptyTrash
			photos={emptying}
			bind:busy={emptyBusy}
			onclose={() => (emptying = null)}
			onemptied={(left) => {
				emptying = left.length > 0 ? left : null;
				reload++;
			}}
		/>
	{/if}
	{#if takingOut}
		<TakeOut
			count={list.length}
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
	.top {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: var(--space-16);
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
