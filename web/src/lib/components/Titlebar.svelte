<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import { goto } from '$app/navigation';
	import Brand from './Brand.svelte';
	import { finder } from '#lib/finder.svelte.js';
	import { pane, swap } from '#lib/motion.js';
	import { text } from '#lib/strings.js';

	let {
		menu = true,
		open = false,
		onmenu
	}: { menu?: boolean; /** the sidebar is open */ open?: boolean; onmenu?: () => void } = $props();

	// On the Mac the corner belongs to the traffic lights: the sidebar opens
	// from the View menu and ⌃⌘S there, as in Apple's own apps.
	const mac = typeof navigator !== 'undefined' && /Mac/.test(navigator.platform);
</script>

<header>
	{#if menu && !mac}
		<button class="menu" aria-label={text.menu} class:open onclick={onmenu}
			><i></i><i></i><i></i></button
		>
	{/if}
	<Brand />
	<span class="spacer"></span>
	{#if finder.offered}
		<!-- X1, X2: Search, and in its place the field, its right edge where Search's is. -->
		<div class="finder">
			{#if finder.open}
				<label in:pane out:pane={{ leaving: true }}>
					<Icon name="search" size={14} />
					<input
						bind:this={finder.input}
						value={finder.filter.name}
						oninput={(e) => (finder.filter = { ...finder.filter, name: e.currentTarget.value })}
						placeholder={text.searchName}
						aria-label={text.searchName}
						spellcheck="false"
						autocomplete="off"
					/>
					<button class="x" aria-label={text.closeSearch} title={text.closeSearch} onclick={() => finder.close()}><Icon name="x-lg" size={12} /></button>
				</label>
			{:else}
				<button class="import" bind:this={finder.button} aria-label={text.search} aria-keyshortcuts="Control+F Meta+F" in:swap out:swap={{ leaving: true }} onclick={() => finder.show()}><Icon name="search" size={14} /><span>{text.search}</span></button>
			{/if}
		</div>
	{/if}
	{#if menu}
		<!-- The app's two ways in, always in the same place: from Google, and
		     from a folder. -->
		<button class="import" aria-label={text.newTakeout} onclick={() => goto('/choose')}><Icon name="plus-lg" size={14} /><span>{text.newTakeout}</span></button>
	{/if}
	<button class="import" aria-label={text.importFolder} onclick={() => goto('/import')}><Icon name="box-arrow-in-down" size={14} /><span>{text.importFolder}</span></button>
</header>

<style>
	header {
		height: 44px;
		flex-shrink: 0;
		display: flex;
		align-items: center;
		gap: 8px;
		/* In the desktop app the window's own controls sit in this bar: the
		   traffic lights on the left on a Mac, the buttons on the right
		   elsewhere. The titlebar-area variables say where they are; in a
		   browser they are unset and the plain padding applies. */
		padding-left: max(16px, calc(env(titlebar-area-x, 0px) + 8px));
		padding-right: max(
			16px,
			calc(100vw - env(titlebar-area-x, 0px) - env(titlebar-area-width, 100vw) + 8px)
		);
		background: var(--bg);
		border-bottom: 1px solid var(--line);
		-webkit-app-region: drag;
	}
	header :global(button),
	label {
		-webkit-app-region: no-drag;
	}
	/* ☰ turns into ✕ while the sidebar is open: three bars, the middle one
	   fades and the outer two cross. */
	.menu {
		width: 24px;
		height: 24px;
		position: relative;
	}
	.menu i {
		position: absolute;
		left: 4px;
		width: 16px;
		height: 2px;
		border-radius: 1px;
		background: var(--fg);
		transition:
			transform var(--motion-base) var(--ease-out),
			opacity var(--motion-base) var(--ease-out);
	}
	.menu i:nth-child(1) {
		top: 6px;
	}
	.menu i:nth-child(2) {
		top: 11px;
	}
	.menu i:nth-child(3) {
		top: 16px;
	}
	.menu.open i:nth-child(1) {
		transform: translateY(5px) rotate(45deg);
	}
	.menu.open i:nth-child(2) {
		opacity: 0;
	}
	.menu.open i:nth-child(3) {
		transform: translateY(-5px) rotate(-45deg);
	}
	@media (prefers-reduced-motion: reduce) {
		.menu i {
			transition: none;
		}
	}
	.spacer {
		flex: 1;
	}
	.finder {
		min-width: 0;
		display: grid;
		grid-template-columns: minmax(0, auto);
		justify-items: end;
		align-items: center;
	}
	.finder > * {
		grid-area: 1 / 1;
	}
	label {
		width: 30vw;
		max-width: 100%;
		height: calc(var(--size-slot-h) + var(--space-4));
		padding: 0 var(--space-12);
		display: flex;
		align-items: center;
		gap: var(--space-8);
		border: 1px solid var(--border);
		border-radius: var(--radius-8);
		color: var(--muted);
	}
	input {
		flex: 1;
		min-width: 0;
		padding: 0;
		border: 0;
		outline: none;
		background: none;
		color: var(--fg);
		font: inherit;
		font-size: var(--text-body);
	}
	input::placeholder {
		color: var(--muted);
	}
	/* The field's border is the place of the keyboard in it. */
	label:has(input:focus) {
		border-color: var(--accent);
	}
	.x {
		display: grid;
	}
	/* A text button: the bordered buttons are the page's, not the bar's. */
	.import {
		height: 32px;
		padding: 0 10px;
		display: inline-flex;
		align-items: center;
		gap: 8px;
		border-radius: 8px;
		color: var(--accent);
		font-size: var(--text-callout);
		font-weight: 600;
		transition: background-color var(--motion-fast) var(--ease-out);
	}
	/* Half a screen has no room for their words; the field takes what is left. */
	:global(.tiny) .import span {
		display: none;
	}
	:global(.tiny) .finder {
		flex: 1;
	}
	:global(.tiny) .spacer:has(+ .finder) {
		display: none;
	}
	:global(.tiny) label {
		width: 100%;
	}
	.import:hover {
		background: var(--accent-soft);
	}
</style>
