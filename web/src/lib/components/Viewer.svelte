<!-- homewend — Copyright (C) 2026 Ron Alder -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- B7: one photo, over the grid. ← and → move, Esc or Space closes. -->
<script lang="ts">
	import { ChevronLeft, ChevronRight, FolderOpen, ImageOff } from '@lucide/svelte';
	import TakeOut from './TakeOut.svelte';
	import { fade } from 'svelte/transition';
	import { getAbout, pathOf, type GridPhoto } from '#lib/api.js';
	import { text } from '#lib/strings.js';
	import { takenAt } from '#lib/format.js';
	import { app } from '#lib/app.svelte.js';

	let {
		photos,
		index = $bindable(),
		ontakenout
	}: { photos: GridPhoto[]; index: number | null; ontakenout?: () => void } = $props();
	let takingOut = $state(false);

	const p = $derived(index === null ? null : photos[index]);
	// What Chromium shows by itself; anything else shows its thumbnail.
	const ext = $derived(p ? p.name.toLowerCase().split('.').pop() ?? '' : '');
	const image = $derived(['jpg', 'jpeg', 'png', 'gif', 'webp'].includes(ext));
	const video = $derived(['mp4', 'webm', 'm4v'].includes(ext));
	// "Taken 14 July 2019, 18:32", as the design writes it.
	const taken = (iso: string) => text.taken(takenAt(new Date(iso)));

	// "IMG_4471.HEIC · Greece 2019 · James Dean": the file, its albums, whose.
	let about = $state('');
	// Where its file was, whole, when it is gone: the person looks there.
	let wasAt = $state('');
	$effect(() => {
		const hash = p?.hash;
		about = '';
		wasAt = '';
		if (hash && p?.missing)
			pathOf(hash).then((r) => {
				if (p?.hash === hash) wasAt = r.path;
			});
		if (hash)
			getAbout(hash).then((a) => {
				if (p?.hash !== hash) return;
				// Accounts by id, named by their address; cards and folders by name.
				const email = (id: string) => app.overview?.accounts.find((x) => x.id === id)?.email ?? id;
				about = [...a.albums, ...a.accounts.map(email), ...a.local].map((x) => ` · ${x}`).join('');
			});
	});

	const move = (d: number) => {
		if (index !== null) index = Math.max(0, Math.min(photos.length - 1, index + d));
	};
	async function reveal() {
		if (p) window.shell?.showInFolder((await pathOf(p.hash)).path);
	}
	function key(e: KeyboardEvent) {
		if (index === null) return;
		// Space closes it, as it closes Quick Look.
		if (e.key === 'Escape' || e.key === ' ') {
			e.preventDefault();
			index = null;
		}
		if (e.key === 'ArrowLeft') move(-1);
		if (e.key === 'ArrowRight') move(1);
	}
</script>

<svelte:window onkeydown={key} />

{#if p}
	<div class="viewer" transition:fade={{ duration: 150 }}>
		<header>
			<div class="meta">
				<b>{p.taken ? taken(p.taken) : text.noDate}</b>
				<span>{p.name}{about}</span>
			</div>
			{#if p.missing}
				<button class="pill" onclick={() => (takingOut = true)}>{text.takeOutOne}</button>
			{:else if window.shell}
				<button class="pill" onclick={reveal}><FolderOpen size={14} />{text.openInFolder}</button>
			{/if}
		</header>
		<div class="stage">
			{#key p.hash}
				{#if p.missing}
					<!-- Its file is gone: what is left is its thumbnail, faded. -->
					<img class="gone" src="/api/thumb/{p.hash}" alt={p.name} />
					<div class="said">
						<span class="badge"><ImageOff size={16} />{text.notOnDisk}</span>
						<span>{text.missingInViewer(wasAt)}</span>
					</div>
				{:else if video}
					<!-- svelte-ignore a11y_media_has_caption -->
					<video src="/api/original/{p.hash}" controls autoplay></video>
				{:else}
					<img src="/api/{image ? 'original' : 'thumb'}/{p.hash}" alt={p.name} />
				{/if}
			{/key}
		</div>
		<button class="round nav left" aria-label={text.previous} onclick={() => move(-1)}><ChevronLeft size={20} /></button>
		<button class="round nav right" aria-label={text.next} onclick={() => move(1)}><ChevronRight size={20} /></button>
	</div>
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
</style>
