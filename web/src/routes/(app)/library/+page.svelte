<!-- homewend — Copyright (C) 2026 Ron Alder -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { page } from '$app/state';
	import { app } from '#lib/app.svelte.js';
	import { getPhotos, type GridPhoto } from '#lib/api.js';
	import { placeOf } from '#lib/places.js';
	import { text } from '#lib/strings.js';
	import Grid from '#lib/components/Grid.svelte';
	import EmptyLibrary from '#lib/screens/EmptyLibrary.svelte';
	import Viewer from '#lib/components/Viewer.svelte';
	import PrimaryButton from '#lib/components/PrimaryButton.svelte';
	import TakeOut from '#lib/components/TakeOut.svelte';
	import { CircleCheck, CircleAlert, Loader } from '@lucide/svelte';

	let open = $state<number | null>(null);

	const place = $derived(placeOf(page.url));
	let photos = $state<GridPhoto[]>([]);
	const title = $derived(
		place.kind === 'year'
			? place.label
			: place.kind === 'album'
				? place.name
				: place.kind === 'missing'
					? text.missingTitle(photos.length)
					: text.allPhotos
	);
	let takingOut = $state(false);
	let reload = $state(0);


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
	// What the page holds: the count, and on All photos, how many each shown
	// account brought; the photos with no date say why they have none.
	const subtitle = $derived.by(() => {
		const n = photos.length.toLocaleString('en');
		if (place.kind === 'missing') return text.missingDetail;
		if (place.kind === 'year' && place.label === text.noDate) return text.noDateSub(n);
		const shown = (app.overview?.accounts ?? []).filter((a) => a.shown);
		const parts = [text.photosAndVideos(photos.length)];
		if (place.kind === 'all' && shown.length > 1)
			for (const a of shown) parts.push(`${a.email} ${a.photos.toLocaleString('en')}`);
		if (place.kind === 'album' && place.account) {
			const a = app.overview?.accounts.find((x) => x.id === place.account);
			if (a) parts.push(a.email);
		}
		return parts.join(' · ');
	});
	let loaded = $state(false);

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
		})();
		return () => (stopped = true);
	});
</script>

{#if app.overview?.gone}
	<!-- The strip under the titlebar says it (G4); there is nothing to show. -->
{:else if app.overview && app.overview.total === 0}
	<EmptyLibrary />
{:else}
	<Grid {photos} withYear={place.kind !== 'year'} onopen={(i) => (open = i)}>
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
	<Viewer {photos} bind:index={open} ontakenout={() => reload++} />
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

<style>
	.head {
		padding: 32px 0 8px;
	}
	h1 {
		font-size: var(--text-display);
		font-weight: 700;
		color: var(--fg);
	}
	p {
		margin-top: 8px;
		font-size: var(--text-body);
		color: var(--muted);
	}
	.out {
		display: flex;
		gap: 16px;
		align-items: center;
		margin-top: 16px;
	}
	.out span {
		font-size: var(--text-callout);
		color: var(--muted);
	}
	.state {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		font-weight: 500;
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
