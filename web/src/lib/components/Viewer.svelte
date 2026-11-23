<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- B7: one photo, over the grid. ← and → move, Esc or Space closes; a click zooms in. -->
<script lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import AppBadge from './AppBadge.svelte';
	import TakeOut from './TakeOut.svelte';
	import GiveDate from './GiveDate.svelte';
	import { pane, swap } from '#lib/motion.js';
	import { getAbout, pathOf, turnLeft, type GridPhoto, type MarkAct, type PhotoAbout } from '#lib/api.js';
	import { text } from '#lib/strings.js';
	import { badgeOf } from '#lib/filter.js';
	import { bytes, takenAt, takenDay } from '#lib/format.js';
	import { app } from '#lib/app.svelte.js';

	let {
		photos,
		index = $bindable(),
		actsOf = () => [],
		editable = true,
		held = false,
		failed = '',
		onact,
		onturned,
		ontakenout,
		ondated
	}: {
		photos: GridPhoto[];
		index: number | null;
		/** Archive, Delete, Restore…: the page's for a photo, beside Rotate and Change date… (Q6). */
		actsOf?: (ps: GridPhoto[]) => MarkAct[];
		/** Rotate and Change date… offered. */
		editable?: boolean;
		/** A dialog of the page is over the viewer: the keys are its. */
		held?: boolean;
		/** What went wrong with the last act. */
		failed?: string;
		onact?: (a: MarkAct, hash: string) => void;
		/** Turned: how many quarter turns it has now. */
		onturned?: (hash: string, turn: number) => void;
		ontakenout?: () => void;
		ondated?: (hash: string) => void;
	} = $props();
	let takingOut = $state(false);
	// K3: this one photo given a date, as the library's selection is (K2).
	let dating = $state(false);

	const p = $derived(index === null ? null : photos[index]);
	// What Chromium shows by itself: zoomed in, the original; anything else
	// (HEIC, a raw file) is zoomed in on its preview.
	const ext = $derived(p ? p.name.toLowerCase().split('.').pop() ?? '' : '');
	const image = $derived(['jpg', 'jpeg', 'png', 'gif', 'webp'].includes(ext));
	// The photo whose preview has loaded whole: shown over its thumbnail.
	let ready = $state('');
	// The previews of the photos either side, asked before they are wanted:
	// the browser keeps them, and moving on is at once.
	$effect(() => {
		if (index === null) return;
		for (const d of [-1, 1]) {
			const q = photos[index + d];
			if (q && !q.missing && !q.video) new Image().src = `/api/preview/${q.hash}`;
		}
	});

	// Zoom: a click on the photo shows it at its own size, the point clicked in
	// the middle; dragged, or scrolled with two fingers, it moves; a click
	// again, or Esc, fits it back. The
	// bigger picture is waited for whole before the switch.
	let fitted = $state<HTMLImageElement>();
	// Turned a quarter or three, the picture's box is the stage's on its side.
	let stageW = $state(0);
	let stageH = $state(0);
	const turn = $derived(p?.turn ?? 0);
	const side = $derived(turn % 2 === 1);
	const fit = $derived(
		!turn
			? ''
			: side
				? `width:${stageH}px;height:${stageW}px;left:${(stageW - stageH) / 2}px;top:${(stageH - stageW) / 2}px;transform:rotate(${-90 * turn}deg)`
				: `transform:rotate(${-90 * turn}deg)`
	);
	// A quarter turn anticlockwise more, kept in the catalog: the grid shows it too.
	async function turnIt() {
		if (!p) return;
		const photo = p;
		zoomed = asked = null;
		onturned?.(photo.hash, (await turnLeft(photo.hash)).turn);
	}
	let pan = $state<HTMLDivElement>();
	let asked = $state<{ hash: string; fx: number; fy: number } | null>(null);
	// w × h the picture as shown, turned; iw × ih the picture itself.
	let zoomed = $state<{ hash: string; fx: number; fy: number; w: number; h: number; iw: number; ih: number } | null>(null);
	const zoomSrc = $derived(p ? `/api/${image ? 'original' : 'preview'}/${p.hash}` : '');
	$effect(() => {
		// Another photo: fitted again.
		if (p?.hash !== zoomed?.hash) zoomed = null;
		if (p?.hash !== asked?.hash) asked = null;
	});
	function zoomIn(e: MouseEvent) {
		if (!fitted || !p) return;
		// Where the click fell on the picture, as object-fit: contain draws it.
		const r = fitted.getBoundingClientRect();
		const [nw, nh] = side ? [fitted.naturalHeight, fitted.naturalWidth] : [fitted.naturalWidth, fitted.naturalHeight];
		const scale = Math.min(r.width / nw, r.height / nh);
		const w = nw * scale, h = nh * scale;
		const fx = (e.clientX - r.left - (r.width - w) / 2) / w;
		const fy = (e.clientY - r.top - (r.height - h) / 2) / h;
		if (fx < 0 || fx > 1 || fy < 0 || fy > 1) return;
		asked = { hash: p.hash, fx, fy };
	}
	function loaded(e: Event) {
		if (!asked) return;
		const img = e.currentTarget as HTMLImageElement;
		// One pixel of the picture to one of the screen.
		const iw = img.naturalWidth / devicePixelRatio, ih = img.naturalHeight / devicePixelRatio;
		zoomed = { ...asked, iw, ih, w: side ? ih : iw, h: side ? iw : ih };
		asked = null;
	}
	$effect(() => {
		if (!zoomed || !pan) return;
		pan.scrollLeft = zoomed.fx * zoomed.w - pan.clientWidth / 2;
		pan.scrollTop = zoomed.fy * zoomed.h - pan.clientHeight / 2;
	});
	// A drag moves the picture; a click without one fits it back.
	let drag: { x: number; y: number; moved: boolean } | null = null;
	function down(e: PointerEvent) {
		drag = { x: e.clientX, y: e.clientY, moved: false };
		(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
	}
	function moveDrag(e: PointerEvent) {
		if (!drag || !pan) return;
		const dx = e.clientX - drag.x, dy = e.clientY - drag.y;
		if (Math.abs(dx) + Math.abs(dy) > 3) drag.moved = true;
		pan.scrollLeft -= dx;
		pan.scrollTop -= dy;
		drag.x = e.clientX;
		drag.y = e.clientY;
	}
	function up() {
		if (drag && !drag.moved) zoomed = null;
		drag = null;
	}
	const video = $derived(['mp4', 'webm', 'm4v'].includes(ext));
	// What is known of the date: an estimated one says only what was read.
	function when(iso: string, guessed?: GridPhoto['guessed']) {
		const d = new Date(iso);
		if (guessed === 'year') return String(d.getUTCFullYear());
		return guessed === 'day' ? takenDay(d) : takenAt(d);
	}
	const date = $derived(p?.taken ? when(p.taken, p.guessed) : text.noDate);
	const badge = $derived(badgeOf(p?.origin));
	// "Taken 14 July 2019, 18:32", as the design writes it, or
	// "26 August 2026, 15:21 · estimated".
	const taken = $derived(!p?.taken ? date : p.guessed ? text.estimated(date) : text.taken(date));

	// "IMG_4471.HEIC · Greece 2019 · James Dean": the file, its albums, whose.
	let about = $state<PhotoAbout | null>(null);
	// Accounts by id, named by their address; cards and folders by name.
	const email = (id: string) => app.overview?.accounts.find((x) => x.id === id)?.email ?? id;
	const from = $derived(about ? [...about.accounts.map(email), ...about.local] : []);
	const beside = $derived(about ? [...about.albums, ...from].map((x) => ` · ${x}`).join('') : '');
	// Info, open on the right (V2): it stays open from photo to photo.
	let info = $state(false);
	// Where its file was, whole, when it is gone: the person looks there.
	let wasAt = $state('');
	$effect(() => {
		const hash = p?.hash;
		wasAt = '';
		if (hash && p?.missing)
			pathOf(hash).then((r) => {
				if (p?.hash === hash) wasAt = r.path;
			});
		if (hash)
			getAbout(hash).then((a) => {
				if (p?.hash !== hash) return;
				about = a;
			}).catch(() => {
				if (p?.hash === hash) about = null;
			});
	});

	const move = (d: number) => {
		if (index !== null) index = Math.max(0, Math.min(photos.length - 1, index + d));
	};
	function key(e: KeyboardEvent) {
		if (held) return;
		if (index === null) return;
		// A dialog over it takes the keys; Esc closes it.
		if (dating || takingOut) {
			if (e.key === 'Escape') dating = takingOut = false;
			return;
		}
		// Space closes it, as it closes Quick Look.
		if (e.key === 'Escape' || e.key === ' ') {
			e.preventDefault();
			// Zoomed in, Esc fits the photo back first.
			if (zoomed || asked) zoomed = asked = null;
			else index = null;
		}
		if (e.key === 'ArrowLeft') move(-1);
		if (e.key === 'ArrowRight') move(1);
	}
</script>

<svelte:window onkeydown={key} />

{#if p}
	<div class="viewer" data-theme="dark" in:swap out:swap={{ leaving: true }}>
		<header>
			<div class="meta">
				<b>{taken}</b>
				<span>{p.name}{beside}</span>
			</div>
			{#if p.missing}
				<button class="pill" onclick={() => (takingOut = true)}>{text.takeOutOne}</button>
			{:else}
				<!-- Clicked, it gives the focus back: ←, → and Space are the viewer's keys. -->
				{#if failed}<span class="failed">{failed}</span>{/if}
				{#if editable}
					{#if !video}<button class="pill" onclick={(e) => (e.currentTarget.blur(), turnIt())}><Icon name="arrow-counterclockwise" size={14} />{text.rotateLeft}</button>{/if}
					<button class="pill" onclick={(e) => (e.currentTarget.blur(), (dating = true))}><Icon name="calendar" size={14} />{text.changeDateEllipsis}</button>
				{/if}
				{#each actsOf([p]) as a (a.label)}
					<button class="pill" onclick={(e) => (e.currentTarget.blur(), onact?.(a, p.hash))}><Icon name={a.icon} size={14} />{a.label}</button>
				{/each}
			{/if}
			<button class="pill icon" aria-label={text.info} title={text.info} aria-pressed={info} onclick={(e) => (e.currentTarget.blur(), (info = !info))}><Icon name="info-circle" size={16} /></button>
		</header>
		<div class="body">
			<div class="main">
				<div class="stage" bind:clientWidth={stageW} bind:clientHeight={stageH}>
					{#key p.hash}
						{#if p.missing}
							<!-- Its file is gone: what is left is its thumbnail, faded. -->
							<img class="gone" src="/api/thumb/{p.hash}" alt={p.name} />
							<div class="said">
								<span class="badge"><Icon name="file-earmark-x" size={16} />{text.notOnDisk}</span>
								<span>{text.missingInViewer(wasAt)}</span>
							</div>
						{:else if video}
							<!-- svelte-ignore a11y_media_has_caption -->
							<video src="/api/original/{p.hash}" controls autoplay></video>
						{:else if zoomed}
							<div class="pan" bind:this={pan} onpointerdown={down} onpointermove={moveDrag} onpointerup={up} role="presentation">
								<div class="turned" style:width="{zoomed.w}px" style:height="{zoomed.h}px">
									<img
										src={zoomSrc}
										alt={p.name}
										style:width="{zoomed.iw}px"
										style:height="{zoomed.ih}px"
										style:transform="translate(-50%, -50%) rotate({-90 * turn}deg)"
										draggable="false"
									/>
								</div>
							</div>
						{:else}
							<!-- The thumbnail at once; the preview, from the cache, over it only
							     once whole: a big file is never seen drawing itself. -->
							<img src="/api/thumb/{p.hash}" alt={p.name} style={fit} />
							<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
							<img
								style={fit}
								class="full"
								class:ready={ready === p.hash}
								src="/api/preview/{p.hash}"
								alt=""
								bind:this={fitted}
								onload={() => (ready = p!.hash)}
								onclick={zoomIn}
							/>
							{#if asked}
								<img class="probe" src={zoomSrc} alt="" onload={loaded} />
								<span class="waiting"><Icon name="arrow-repeat" size={20} /></span>
							{/if}
						{/if}
					{/key}
				</div>
				<button class="round nav left" aria-label={text.previous} onclick={() => move(-1)}><Icon name="chevron-left" size={20} /></button>
				<button class="round nav right" aria-label={text.next} onclick={() => move(1)}><Icon name="chevron-right" size={20} /></button>
			</div>
			{#if info}
				<aside in:pane out:pane={{ leaving: true }}>
					<div class="rows">
						<h2>{text.info}</h2>
						<dl>
							<div>
								<dt>{text.infoTaken}</dt>
								<dd>
									{date}
									{#if about && text.dated[about.dated]}<small>{text.dated[about.dated]}</small>{/if}
									{#if p.guessed || badge}
										<span class="tags">
											{#if p.guessed}<AppBadge id="estimated" label={text.estimatedTag} on small />{/if}
											{#if badge}<AppBadge id={badge.id} label={badge.label} icon={badge.icon} on small />{/if}
										</span>
									{/if}
								</dd>
							</div>
							{#if about}
								<div><dt>{text.infoSize}</dt><dd>{bytes(about.bytes)}</dd></div>
								{#if from.length}<div><dt>{text.infoFrom}</dt><dd>{from.join(', ')}</dd></div>{/if}
								<div><dt>{text.infoFile}</dt><dd>{p.name}<small>{about.path.slice(0, about.path.lastIndexOf('/') + 1)}</small></dd></div>
							{/if}
						</dl>
					</div>
				</aside>
			{/if}
		</div>
	</div>
	{#if dating}
		<GiveDate
			photos={[p]}
			onclose={(moved) => {
				dating = false;
				if (moved) ondated?.(p.hash);
			}}
		/>
	{/if}
	{#if takingOut}
		<TakeOut
			count={1}
			hash={p.hash}
			onclose={(taken) => {
				takingOut = false;
				if (taken) {
					index = null;
					ontakenout?.();
				}
			}}
		/>
	{/if}
{/if}

<style>
	.gone {
		opacity: 0.35;
		filter: grayscale(1);
	}
	.said {
		position: absolute;
		inset: 0;
		display: flex;
		flex-direction: column;
		gap: 8px;
		align-items: center;
		justify-content: center;
		font-family: var(--font-mono);
		font-size: var(--text-subheadline);
		color: #ffffff99;
	}
	.badge {
		display: inline-flex;
		gap: 8px;
		align-items: center;
		height: 32px;
		padding: 0 14px;
		border-radius: var(--radius-full);
		background: #ffffff14;
		color: #fff;
		font-family: var(--font-ui);
		font-size: var(--text-body);
		font-weight: 600;
	}
	.viewer {
		position: fixed;
		/* The photo takes the whole window, titlebar included. */
		inset: 0;
		z-index: 30;
		display: flex;
		flex-direction: column;
		background: #0b0c10;
		color: #fff;
	}
	header {
		display: flex;
		align-items: center;
		gap: 16px;
		padding: 12px 16px;
		/* Clear of the window buttons the system draws, at the top left on a
		   Mac, at the top right elsewhere. Esc closes it: a button of ours
		   there sat under the Mac's own. */
		padding-left: calc(env(titlebar-area-x, 0px) + 16px);
		padding-right: calc(100vw - env(titlebar-area-x, 0px) - env(titlebar-area-width, 100vw) + 16px);
	}
	.meta {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
	}
	.meta b {
		font-size: var(--text-body);
	}
	.meta span {
		font-family: var(--font-mono);
		font-size: var(--text-subheadline);
		color: #ffffff99;
	}
	.round {
		width: 36px;
		height: 36px;
		border-radius: 50%;
		display: grid;
		place-items: center;
		background: #ffffff14;
		color: #fff;
	}
	.pill {
		height: 32px;
		padding: 0 12px;
		display: flex;
		align-items: center;
		gap: 6px;
		border-radius: 999px;
		background: #ffffff14;
		color: #fff;
		font-size: var(--text-callout);
		font-weight: 600;
	}
	/* The photo fits the space left, whole: never taller than the window. */
	.stage {
		flex: 1;
		min-height: 0;
		position: relative;
		margin: 0 72px 24px;
	}
	img,
	video {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		object-fit: contain;
	}
	.full {
		opacity: 0;
		cursor: zoom-in;
	}
	.full.ready {
		opacity: 1;
	}
	/* Zoomed in: the photo at its own size, moved by dragging it. */
	/* It scrolls: a trackpad's two fingers or a mouse wheel move it as a drag
	   does; no scrollbars over the photo. */
	.pan {
		position: absolute;
		inset: 0;
		display: flex;
		overflow: auto;
		scrollbar-width: none;
		overscroll-behavior: contain;
		cursor: grab;
		touch-action: none;
	}
	.pan:active {
		cursor: grabbing;
	}
	/* Centred when smaller than the window, never cut when bigger; the
	   picture turned inside its box. */
	.turned {
		margin: auto;
		flex-shrink: 0;
		position: relative;
	}
	.pan img {
		position: absolute;
		inset: auto;
		left: 50%;
		top: 50%;
		max-width: none;
		object-fit: fill;
	}
	/* The bigger picture, loaded out of sight until whole. */
	.probe {
		visibility: hidden;
	}
	.waiting {
		position: absolute;
		top: 50%;
		left: 50%;
		translate: -50% -50%;
		display: grid;
		color: #ffffffcc;
		animation: spin var(--motion-spin) linear infinite;
	}
	@keyframes spin {
		to {
			rotate: 360deg;
		}
	}
	.pill.icon {
		width: 32px;
		padding: 0;
		justify-content: center;
	}
	.pill[aria-pressed='true'] {
		background: #ffffff29;
	}
	.body {
		flex: 1;
		min-height: 0;
		display: flex;
	}
	.main {
		flex: 1;
		min-width: 0;
		position: relative;
		display: flex;
		flex-direction: column;
	}
	/* As wide as the sidebar; its text does not reflow while it opens. */
	aside {
		width: 248px;
		flex-shrink: 0;
		overflow: hidden;
	}
	.rows {
		box-sizing: border-box;
		width: 248px;
		height: 100%;
		padding: 16px 24px;
		border-left: 1px solid #ffffff29;
		display: flex;
		flex-direction: column;
		gap: 16px;
		overflow-y: auto;
	}
	.rows h2 {
		margin: 0;
		font-size: var(--text-title-3);
		font-weight: 600;
	}
	dl {
		margin: 0;
		display: flex;
		flex-direction: column;
		gap: 16px;
	}
	dt {
		margin-bottom: 4px;
		font-size: var(--text-subheadline);
		color: #ffffff99;
	}
	dd {
		margin: 0;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 4px;
		font-size: var(--text-body);
		overflow-wrap: anywhere;
	}
	dd small {
		font-size: var(--text-callout);
		color: #ffffff99;
	}
	.tags {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
	}
	.nav {
		position: absolute;
		top: 50%;
	}
	.left {
		left: 16px;
	}
	.right {
		right: 16px;
	}
	.failed {
		color: var(--err);
		font-size: var(--text-body);
	}
</style>
