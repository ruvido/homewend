<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- P6: whose they are, asked every time, nobody chosen beforehand: one row a
     person, a click goes on. Someone else is a name typed here, or the
     parent's own way when it gives onelse. -->
<script lang="ts">
	import Avatar from '#lib/components/Avatar.svelte';
	import Dialog from '#lib/components/Dialog.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import PrimaryButton from '#lib/components/PrimaryButton.svelte';
	import SecondaryButton from '#lib/components/SecondaryButton.svelte';
	import { app, personOf } from '#lib/app.svelte.js';
	import { text } from '#lib/strings.js';

	let {
		title,
		lead,
		onpick,
		onelse,
		oncancel
	}: { title: string; lead: string; onpick: (profile: string) => void; onelse?: () => void; oncancel: () => void } = $props();
	let naming = $state(false);
	let typed = $state('');
	const name = $derived(typed.trim());
</script>

<!-- Esc is Cancel: nothing is chosen. -->
<svelte:window
	onkeydowncapture={(e) => {
		if (e.key !== 'Escape') return;
		e.stopPropagation();
		e.preventDefault();
		if (naming) naming = false;
		else oncancel();
	}}
/>

<Dialog {title}>
	<p class="muted">{lead}</p>
	{#if naming}
		<!-- svelte-ignore a11y_autofocus -->
		<input bind:value={typed} placeholder={text.yourName} aria-label={text.yourName} spellcheck="false" autocomplete="off" autofocus onkeydown={(e) => e.key === 'Enter' && name && onpick(name)} />
	{:else}
		<div class="who">
			{#each app.overview?.profiles ?? [] as p (p)}
				<button onclick={() => onpick(p)}><Avatar account={{ id: p, name: p, email: '', shown: true }} index={personOf(p)} /><b>{p}</b></button>
			{/each}
			<button onclick={() => (onelse ? onelse() : (naming = true))}><span class="glyph"><Icon name="person-plus" size={16} /></span><b>{text.someoneElse}</b></button>
		</div>
	{/if}
	{#snippet actions()}
		<SecondaryButton onclick={oncancel}>{text.cancel}</SecondaryButton>
		{#if naming}<PrimaryButton disabled={!name} onclick={() => onpick(name)}>{text.continue}</PrimaryButton>{/if}
	{/snippet}
</Dialog>

<style>
	.muted {
		margin: 0;
		color: var(--muted);
	}
	.who {
		display: flex;
		flex-direction: column;
		gap: var(--space-8);
	}
	.who button {
		display: flex;
		align-items: center;
		gap: var(--space-12);
		padding: var(--space-8) var(--space-12);
		border: 1px solid var(--border);
		border-radius: var(--radius-10);
		background: var(--bg);
		color: var(--fg);
		font-size: var(--text-body);
		text-align: left;
	}
	.who button:hover {
		background: var(--soft);
	}
	.who b {
		font-weight: var(--weight-semibold);
	}
	.glyph {
		width: var(--size-avatar);
		height: var(--size-avatar);
		display: grid;
		place-items: center;
		border-radius: var(--radius-full);
		background: var(--soft);
		color: var(--muted);
	}
	input {
		height: var(--size-segmented);
		padding: 0 var(--space-12);
		border: 1px solid var(--accent);
		border-radius: var(--radius-8);
		background: var(--surface);
		color: var(--fg);
		font: inherit;
		outline: none;
	}
</style>
