<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- X2, X3: the search's dates and badges, in a row under the title bar; the
     grid moves down. The name is typed in the title bar. -->
<script lang="ts">
	import type { Badge, Filter } from '#lib/filter.js';
	import { day } from '#lib/format.js';
	import { reveal, swap } from '#lib/motion.js';
	import { text } from '#lib/strings.js';
	import AppBadge from './AppBadge.svelte';
	import DatePicker from './DatePicker.svelte';
	import Dialog from './Dialog.svelte';
	import Icon from './Icon.svelte';
	import PrimaryButton from './PrimaryButton.svelte';
	import SecondaryButton from './SecondaryButton.svelte';

	let {
		filter = $bindable(),
		badges,
		dated,
		bounds
	}: {
		filter: Filter;
		badges: Badge[];
		/** The page's photos have dates to search by. */
		dated: boolean;
		/** The page's first and last day: where the calendars start. */
		bounds: () => { start: string; end: string };
	} = $props();

	// The calendar shown, and the day on it.
	let asking = $state<'start' | 'end' | null>(null);
	let value = $state('');
	// 1 Jan 2025; the year once when it is both days'.
	const say = (which: 'start' | 'end') => {
		const on = filter[which];
		const once = which === 'start' && on.slice(0, 4) === filter.end.slice(0, 4);
		return day(new Date(on + 'T00:00')) + (once ? '' : ` ${on.slice(0, 4)}`);
	};
	const label = { start: text.searchFrom, end: text.searchTo };

	function flip(id: string) {
		const on = filter.badges.includes(id);
		filter = { ...filter, badges: on ? filter.badges.filter((b) => b !== id) : [...filter.badges, id] };
	}
	function ask(which: 'start' | 'end') {
		value = (filter[which] || bounds()[which]) + 'T00:00';
		asking = which;
	}
	function choose() {
		if (!asking) return;
		const next = { ...filter, [asking]: value.slice(0, 10) };
		// From after To: the two days are taken the other way round.
		if (next.start && next.end && next.start > next.end) [next.start, next.end] = [next.end, next.start];
		filter = next;
		asking = null;
	}
	// The calendar takes the page's keys; Esc closes it.
	function key(e: KeyboardEvent) {
		if (!asking) return;
		e.stopPropagation();
		if (e.key === 'Escape') asking = null;
	}
</script>

<svelte:window onkeydowncapture={key} />

{#snippet pick(which: 'start' | 'end')}
	<button aria-label={text.searchDay(label[which], say(which))} onclick={() => ask(which)}>{say(which)}</button>
{/snippet}
{#snippet chip(days: ('start' | 'end')[])}
	<span class="field on" in:swap>
		<Icon name="calendar3" size={14} />
		{@render pick(days[0])}
		{#if days[1]}–{@render pick(days[1])}{/if}
		<button class="drop" aria-label={text.dropDate(days.map(say).join(' – '))} onclick={() => (filter = { ...filter, ...Object.fromEntries(days.map((d) => [d, ''])) })}><Icon name="x-lg" size={12} /></button>
	</span>
{/snippet}
{#snippet side(which: 'start' | 'end')}
	{#if filter[which]}{@render chip([which])}{:else}<button class="field" in:swap onclick={() => ask(which)}><Icon name="calendar3" size={14} />{label[which]}</button>{/if}
{/snippet}

<div class="bar" in:reveal out:reveal={{ leaving: true }}>
	{#if dated}
		{#if filter.start && filter.end}{@render chip(['start', 'end'])}{:else}{@render side('start')}<em>–</em>{@render side('end')}{/if}
		{#if badges.length > 0}<i></i>{/if}
	{/if}
	{#each badges as b (b.id)}
		{@const on = filter.badges.includes(b.id)}
		<AppBadge id={b.id} label={b.label} icon={b.icon} count={on ? undefined : b.count} {on} onclick={() => flip(b.id)} />
	{/each}
</div>
{#if asking}
	<Dialog title={label[asking]}>
		<DatePicker bind:value time={false} />
		{#snippet actions()}
			<SecondaryButton onclick={() => (asking = null)}>{text.cancel}</SecondaryButton>
			<PrimaryButton onclick={choose}>{text.chooseDay}</PrimaryButton>
		{/snippet}
	</Dialog>
{/if}

<style>
	.bar {
		padding: var(--space-8) var(--space-40);
		display: flex;
		flex-wrap: wrap;
		flex-shrink: 0;
		align-items: center;
		gap: var(--space-8);
		border-bottom: 1px solid var(--line);
	}
	:global(.narrow) .bar {
		padding-inline: var(--space-24);
	}
	/* The dates, then the badges. */
	i {
		width: var(--space-16);
	}
	em {
		font-style: normal;
		color: var(--muted);
	}
	.field {
		height: var(--size-slot-h);
		padding: 0 var(--space-12);
		display: inline-flex;
		align-items: center;
		gap: var(--space-8);
		border: 1px solid var(--border);
		border-radius: var(--radius-full);
		color: var(--text);
		font-size: var(--text-callout);
		white-space: nowrap;
	}
	.field.on {
		border-color: transparent;
		background: var(--accent-soft);
		color: var(--accent);
		font-weight: var(--weight-semibold);
	}
	.drop {
		display: grid;
		color: var(--accent);
	}
</style>
