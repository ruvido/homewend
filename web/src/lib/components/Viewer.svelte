<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- B7: one photo, over the grid. ← and → move, Esc or Space closes; a click zooms in. -->
<script lang="ts">
	import { untrack } from 'svelte';
	import Icon from '#lib/components/Icon.svelte';
	import AppBadge from './AppBadge.svelte';
	import TakeOut from './TakeOut.svelte';
	import GiveDate from './GiveDate.svelte';
	import { pane, swap } from '#lib/motion.js';
	import { convertNegatives, getAbout, getFile, isNegativeRaw, originalOf, pathOf, pictureOf, turn as turnPhoto, type GridPhoto, type MarkAct, type PhotoAbout, type PhotoFile, type Shown } from '#lib/api.js';
	import { text } from '#lib/strings.js';
	import { badgeOf } from '#lib/filter.js';
	import { bytes, takenAt, takenDay } from '#lib/format.js';
	import { app } from '#lib/app.svelte.js';
	import Avatar from './Avatar.svelte';

	let {
		photos,
		index = $bindable(),
		actsOf = () => [],
		editable = true,
		held = false,
		failed = '',
		onact,
		onturned,
		onfavorite,
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
		onact?: (a: MarkAct, p: GridPhoto) => void;
		/** Turned: how it is shown now. */
		onturned?: (id: string, now: Shown) => void;
		/** Given a heart, or not any more. */
		onfavorite?: (p: GridPhoto, on: boolean) => void;
		ontakenout?: () => void;
		ondated?: (id: string) => void;
	} = $props();
	let takingOut = $state(false);
	// The photo just given a heart: its bounce, not at every favorite passed.
	let beat = $state('');
	// K3: this one photo given a date, as the library's selection is (K2).
	let dating = $state(false);

	const tile = $derived(index === null ? null : photos[index]);
	// W3: a photo two people have opens on the first one's copy; the other's
	// is a press away. What is done to it is done to the copy shown.
	let chosen = $state({ tile: '', profile: '' });
	const p = $derived(tile?.copies?.find((c) => chosen.tile === tile.id && c.profile === chosen.profile) ?? tile);
	// What is asked about it is asked once: a heart or a turn makes another p.
	const id = $derived(p?.id);
	const missing = $derived(p?.missing);
	// What Chromium shows by itself: zoomed in, the original; anything else
	// (HEIC, a raw file, a photo with an edit) is zoomed in on its preview.
	const ext = $derived(p ? p.name.toLowerCase().split('.').pop() ?? '' : '');
	const image = $derived(['jpg', 'jpeg', 'png', 'gif', 'webp'].includes(ext));
	// The photo whose preview has loaded whole, as it was then: shown over its
	// thumbnail. Its edit another since, that stays until the new one's has.
	let ready = $state.raw<GridPhoto>();
	const seen = $derived(p && ready?.id === p.id && ready.edit !== p.edit ? ready : p);
	// A RAF is shown from its camera's JPEG while it is developed: the header
	// says so, and once it is, its preview is asked again and shown over the
	// one before, once whole.
	const developing = $derived(!!p && app.developing.now.includes(p.hash));
	let waited = false;
	let again = $state(0);
	let under = $state('');
	const previewSrc = $derived(p ? pictureOf(p, 'preview') + (again ? `&developed=${again}` : '') : '');
	// Another photo begins again; before the one below, which reads it.
	$effect(() => {
		void id;
		untrack(() => {
			waited = false;
			under = '';
			again = 0;
		});
	});
	$effect(() => {
		void id;
		const done = !!p && app.developing.recent.includes(p.hash);
		const now = developing;
		untrack(() => {
			if (now) waited = true;
			else if (waited && done) {
				waited = false;
				under = previewSrc;
				again++;
				ready = undefined;
			}
		});
	});
	// Printed from its negative, or shown as it came again: the picture
	// before stays under, the other fades in over it once whole.
	let shownSrc = '';
	let printedAs = { id: '', positive: '' };
	$effect(() => {
		const now = { id: id ?? '', positive: p?.positive ?? '' };
		untrack(() => {
			if (now.id === printedAs.id && now.positive !== printedAs.positive && shownSrc) {
				under = shownSrc;
				ready = undefined;
			}
			printedAs = now;
		});
	});
	// The previews of the photos either side, asked before they are wanted:
	// the browser keeps them, and moving on is at once.
	$effect(() => {
		if (index === null) return;
		for (const d of [-1, 1]) {
			const q = photos[index + d];
			// Said ahead: a raw file's is not developed sooner for it.
			if (q && !q.missing && !q.video) fetch(pictureOf(q, 'preview'), { headers: { 'X-Ahead': '1' } }).catch(() => {});
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
	const turn = $derived(seen?.turn ?? 0);
	const side = $derived(turn % 2 === 1);
	const fit = $derived(
		!turn
			? ''
			: side
				? `width:${stageH}px;height:${stageW}px;left:${(stageW - stageH) / 2}px;top:${(stageH - stageW) / 2}px;transform:rotate(${-90 * turn}deg)`
				: `transform:rotate(${-90 * turn}deg)`
	);
	// A quarter turn more, anticlockwise or clockwise, kept in the catalog: the grid shows it too.
	async function turnIt(right: boolean) {
		if (!p) return;
		const photo = p;
		zoomed = asked = null;
		onturned?.(photo.id, await turnPhoto(photo, right));
	}
	let pan = $state<HTMLDivElement>();
	let asked = $state<{ src: string; fx: number; fy: number } | null>(null);
	// w × h the picture as shown, turned; iw × ih the picture itself.
	let zoomed = $state<{ src: string; fx: number; fy: number; w: number; h: number; iw: number; ih: number } | null>(null);
	const zoomSrc = $derived(p ? image && !p.edit ? originalOf(p) : pictureOf(p, 'preview') : '');
	$effect(() => {
		// Another photo, or edit of it: fitted again.
		if (zoomSrc !== zoomed?.src) zoomed = null;
		if (zoomSrc !== asked?.src) asked = null;
		if (id !== beat) beat = '';
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
		asked = { src: zoomSrc, fx, fy };
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
	// "26 August 2026, 15:21 · estimated"; then whether it is edited.
	const dated = $derived(!p?.taken ? date : p.guessed ? text.estimated(date) : text.taken(date));
	const edited = $derived(p?.newer ? text.editedNewer(dated) : p?.edit ? text.edited(dated) : dated);
	const taken = $derived(developing ? text.inDevelopment(edited) : edited);

	// "IMG_4471.HEIC · Greece 2019 · James Dean": the file, its albums, whose.
	let about = $state<PhotoAbout | null>(null);
	// Accounts by id, named by their address; cards and folders by name.
	const email = (id: string) => app.overview?.accounts.find((x) => x.id === id)?.email ?? id;
	const from = $derived(about ? [...about.accounts.map(email), ...about.local] : []);
	const beside = $derived(about ? [...about.albums, ...from].map((x) => ` · ${x}`).join('') : '');
	// Info, open on the right (V2): it stays open from photo to photo.
	let info = $state(false);
	// What its file says: read only while Info shows it, of this photo alone.
	let file = $state<PhotoFile | null>(null);
	$effect(() => {
		file = null;
		const of = untrack(() => p);
		if (!id || !of || !info || missing) return;
		let left = false;
		getFile(of).then((f) => left || (file = f)).catch(() => {});
		return () => (left = true);
	});
	const pixels = $derived(file?.width ? (side ? text.infoPixels(file.height, file.width) : text.infoPixels(file.width, file.height)) + ' · ' : '');
	// Where its file was, whole, when it is gone: the person looks there.
	let wasAt = $state('');
	$effect(() => {
		const at = id;
		const of = untrack(() => p);
		wasAt = '';
		if (!of) return;
		if (missing)
			pathOf(of).then((r) => {
				if (id === at) wasAt = r.path;
			});
		getAbout(of).then((a) => {
			if (id !== at) return;
			about = a;
		}).catch(() => {
			if (id === at) about = null;
		});
	});

	const move = (d: number) => {
		if (index !== null) index = Math.max(0, Math.min(photos.length - 1, index + d));
	};
	function key(e: KeyboardEvent) {
		// A key the page's dialog took is not the viewer's, its dialog closed by it.
		if (held || e.defaultPrevented) return;
		if (index === null) return;
		// A dialog over it takes the keys; Esc closes it.
		if (dating || takingOut) {
			if (e.key === 'Escape') dating = takingOut = false;
			return;
		}
		// On a button of its own, Space presses it.
		if (e.key === ' ' && (e.target as Element).closest?.('.viewer button')) return;
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
			<button class="pill icon" aria-label={text.backToLibrary} title={text.backToLibrary} onclick={() => (index = null)}><Icon name="arrow-left" size={16} /></button>
			<div class="meta">
				<b>{taken}{#if developing}<span class="spin"><Icon name="arrow-repeat" size={16} /></span>{/if}</b>
				<span>{p.name}{beside}</span>
			</div>
			{#if tile?.copies}
				<div class="whose">
					{#each tile.copies as c (c.id)}
						<button class="pill" aria-pressed={c.id === p.id} onclick={(e) => (e.currentTarget.blur(), (chosen = { tile: tile.id, profile: c.profile }))}>
							<Avatar account={{ id: c.profile, name: c.profile, email: c.profile }} index={c.of} size={20} />{c.profile}
						</button>
					{/each}
				</div>
			{/if}
			{#if p.missing}
				<button class="pill" onclick={() => (takingOut = true)}>{text.takeOutOne}</button>
			{:else}
				<!-- Clicked, it gives the focus back: ←, → and Space are the viewer's keys. -->
				{#if failed}<span class="failed">{failed}</span>{/if}
				{#if editable}
					<!-- An edit not known here cannot be turned: the photo stays as it came. -->
					{#if !video && !p.newer}
						<button class="pill" onclick={(e) => (e.currentTarget.blur(), turnIt(false))}><Icon name="arrow-counterclockwise" size={14} />{text.rotateLeft}</button>
						<button class="pill" onclick={(e) => (e.currentTarget.blur(), turnIt(true))}><Icon name="arrow-clockwise" size={14} />{text.rotateRight}</button>
					{/if}
					{#if isNegativeRaw(p) && !p.newer}
						<button class="pill" disabled={developing} onclick={(e) => (e.currentTarget.blur(), convertNegatives([p], !p.positive).catch(() => {}))}><Icon name="circle-half" size={14} />{p.positive ? text.showAsNegative : text.convertNegative}</button>
					{/if}
					<button class="pill" onclick={(e) => (e.currentTarget.blur(), (dating = true))}><Icon name="calendar" size={14} />{text.changeDateEllipsis}</button>
				{/if}
				{#each actsOf([p]) as a (a.label)}
					<button class="pill" onclick={(e) => (e.currentTarget.blur(), onact?.(a, p))}><Icon name={a.icon} size={14} />{a.label}</button>
				{/each}
			{/if}
			{#if editable && !p.missing}
				<button class="pill icon heart" class:beat={beat === p.id && p.favorite} aria-label={text.favorite} title={text.favorite} aria-pressed={!!p.favorite} onclick={(e) => (e.currentTarget.blur(), (beat = p.id), onfavorite?.(p, !p.favorite))}>
					<Icon name={p.favorite ? 'heart-fill' : 'heart'} size={16} />
				</button>
			{/if}
			<button class="pill icon" aria-label={text.info} title={text.info} aria-pressed={info} onclick={(e) => (e.currentTarget.blur(), (info = !info))}><Icon name="info-circle" size={16} /></button>
		</header>
		<div class="body">
			<div class="main">
				<div class="stage" bind:clientWidth={stageW} bind:clientHeight={stageH}>
					{#key p.id}
						{#if p.missing}
							<!-- Its file is gone: what is left is its thumbnail, faded. -->
							<img class="gone" src={pictureOf(p)} alt={p.name} />
							<div class="said">
								<span class="badge"><Icon name="file-earmark-x" size={16} />{text.notOnDisk}</span>
								<span>{text.missingInViewer(wasAt)}</span>
							</div>
						{:else if video}
							<!-- svelte-ignore a11y_media_has_caption -->
							<video src={originalOf(p)} controls autoplay></video>
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
							<img src={under || pictureOf(seen ?? p)} alt={p.name} style={fit} />
							<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
							<img
								style={fit}
								class="full"
								class:ready={ready?.id === p.id}
								src={previewSrc}
								alt=""
								bind:this={fitted}
								onload={() => p && ((ready = p), (shownSrc = previewSrc))}
								onclick={zoomIn}
							/>
							{#if asked}
								<img class="probe" src={zoomSrc} alt="" onload={loaded} />
								<span class="waiting spin"><Icon name="arrow-repeat" size={20} /></span>
							{/if}
						{/if}
					{/key}
				</div>
				<button class="round nav left" aria-label={text.previous} onclick={(e) => (e.currentTarget.blur(), move(-1))}><Icon name="chevron-left" size={20} /></button>
				<button class="round nav right" aria-label={text.next} onclick={(e) => (e.currentTarget.blur(), move(1))}><Icon name="chevron-right" size={20} /></button>
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
								<div><dt>{text.infoSize}</dt><dd>{pixels}{bytes(about.bytes)}</dd></div>
								{#if !p.video && !p.missing}<div><dt>{text.infoCamera}</dt><dd>{file ? file.camera || '—' : '\u00a0'}{#if file && !file.camera}<small in:swap>{text.infoNoCamera}</small>{/if}</dd></div>{/if}
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
				if (moved) ondated?.(p.id);
			}}
		/>
	{/if}
	{#if takingOut}
		<TakeOut
			count={1}
			photo={p}
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
		/* Over the titlebar, whose drag would take its buttons' clicks. */
		-webkit-app-region: no-drag;
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
		   Mac, at the top right elsewhere: the arrow comes after them. */
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
		transition: opacity var(--motion-base) var(--ease-out);
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
	}
	.spin {
		animation: spin var(--motion-spin) linear infinite;
	}
	.meta .spin {
		display: inline-grid;
		vertical-align: middle;
		margin-left: var(--space-6);
		font: inherit;
		color: inherit;
	}
	@keyframes spin {
		to {
			rotate: 360deg;
		}
	}
	/* W3: whose copy is shown; the others' a press away. */
	.whose {
		display: flex;
		gap: var(--space-4);
	}
	.whose .pill {
		padding-left: var(--space-6);
		background: transparent;
		color: var(--on-photo-muted);
		transition: background var(--motion-fast) var(--ease-out), color var(--motion-fast) var(--ease-out);
	}
	.whose .pill[aria-pressed='true'] {
		background: var(--on-photo-soft);
		color: var(--on-photo);
	}
	.pill.icon {
		width: 32px;
		padding: 0;
		justify-content: center;
	}
	.pill[aria-pressed='true'] {
		background: #ffffff29;
	}
	/* A heart given bounces. */
	.heart.beat :global(svg) {
		animation: beat var(--motion-base) var(--ease-out);
	}
	@keyframes beat {
		50% {
			scale: 1.3;
		}
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
