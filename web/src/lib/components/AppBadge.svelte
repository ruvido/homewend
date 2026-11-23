<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- The app a photo came through. At rest only its logo is coloured; on, the
     badge fills with that colour: picked in a search, or a photo's own in Info. -->
<script lang="ts">
	import Icon from './Icon.svelte';
	import type { IconName } from '#lib/icons.js';

	let {
		id,
		label,
		icon,
		count,
		on = false,
		small = false,
		onclick
	}: {
		id: string;
		label: string;
		icon?: IconName;
		count?: number;
		on?: boolean;
		/** In Info, beside text. */
		small?: boolean;
		/** Pressable: in the search. */
		onclick?: () => void;
	} = $props();
</script>

{#snippet inside()}
	{#if icon}<Icon name={icon} size={12} />{/if}{label}
	{#if count !== undefined}<i>{count.toLocaleString('en')}</i>{/if}
{/snippet}
{#if onclick}
	<button class="badge {id}" class:on aria-pressed={on} {onclick}>{@render inside()}</button>
{:else}
	<span class="badge {id}" class:on class:small>{@render inside()}</span>
{/if}

<style>
	.badge {
		--app: var(--muted);
		height: var(--size-slot-h);
		padding: 0 var(--space-12);
		display: inline-flex;
		align-items: center;
		gap: var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-full);
		color: var(--text);
		font-size: var(--text-callout);
		font-weight: var(--weight-semibold);
		white-space: nowrap;
	}
	.small {
		height: var(--space-20);
		padding: 0 var(--space-8);
		font-size: var(--text-subheadline);
	}
	.badge :global(svg) {
		color: var(--app);
	}
	i {
		font-style: normal;
		font-weight: var(--weight-regular);
	}
	.whatsapp {
		--app: var(--app-whatsapp);
	}
	.telegram {
		--app: var(--app-telegram);
	}
	.signal {
		--app: var(--app-signal);
	}
	.instagram {
		--app: var(--app-instagram);
	}
	.estimated {
		--app: var(--warn);
	}
	.on {
		border-color: transparent;
		background: var(--app);
		color: var(--on-accent);
	}
	.on :global(svg) {
		color: inherit;
	}
</style>
