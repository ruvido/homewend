<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- "Library [ron ▾]": whose photos they are. The name opens the list of the
     profiles there are, and someone else's: a new name typed in the field
     that takes its place. The import's (J1) and a phone's pairing (P2). -->
<script lang="ts">
	import Avatar from '#lib/components/Avatar.svelte';
	import ContextMenu from '#lib/components/ContextMenu.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import { app } from '#lib/app.svelte.js';
	import { text } from '#lib/strings.js';

	// typed: a name chosen or typed; profile: the one in use, the parent's.
	// naming: a new name is being typed, for a parent that waits for it.
	let {
		typed = $bindable(''),
		naming = $bindable(false),
		profile
	}: { typed?: string; naming?: boolean; profile: string } = $props();
	const profiles = $derived(app.overview?.profiles ?? []);
	let menu = $state<HTMLElement | null>(null);
</script>

<div class="whose">
	<span>{text.libraryLabel}</span>
	{#if profile && !naming}
		<button class="account" aria-haspopup="menu" onclick={(e) => (menu = menu ? null : e.currentTarget)}
			><Avatar account={{ id: profile, name: profile, email: '', shown: true }} /><b>{profile}</b><Icon name="chevron-down" size={14} /></button
		>
	{:else}
		<!-- svelte-ignore a11y_autofocus -->
		<!-- Typing is naming: the pill comes back on Enter or leaving the field, not at the first letter. -->
		<input
			bind:value={typed}
			oninput={() => (naming = true)}
			placeholder={text.yourName}
			aria-label={text.yourName}
			spellcheck="false"
			autofocus={naming}
			onkeydown={(e) => {
				if (e.key === 'Enter' && typed.trim()) naming = false;
				if (e.key === 'Escape' && profile) ((typed = ''), (naming = false));
			}}
			onblur={() => typed.trim() && (naming = false)}
		/>
	{/if}
</div>
{#if menu}
	<ContextMenu
		anchor={menu}
		onclose={() => (menu = null)}
		items={[
			...profiles.map((p) => ({ icon: p === profile ? ('check-lg' as const) : ('person' as const), label: p, run: () => void (typed = p) })),
			{ icon: 'person-plus' as const, label: text.someoneElse, run: () => void ((typed = ''), (naming = true)) }
		]}
	/>
{/if}

<style>
	.whose {
		display: flex;
		align-items: center;
		gap: var(--space-12);
		color: var(--muted);
	}
	/* The account pill of the status bar, opening the list of libraries. */
	.account {
		height: var(--size-avatar);
		display: inline-flex;
		align-items: center;
		gap: var(--space-8);
		padding: 0 var(--space-12) 0 var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-full);
		background: var(--soft);
		color: var(--muted);
	}
	.account b {
		color: var(--fg);
		font-weight: 600;
	}
	.whose input {
		height: 36px;
		padding: 0 var(--space-12);
		border: 1px solid var(--border);
		border-radius: var(--radius-10);
		background: var(--bg);
		color: var(--fg);
		font: inherit;
		font-size: var(--text-body);
	}
	.whose input::placeholder {
		color: var(--faint);
	}
</style>
