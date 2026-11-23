<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- S1–S8: the library's search. The field grows to show the dates and the
     badges, and the grid moves down; closed, it holds what is picked. -->
<script lang="ts">
	import { noFilter, isFiltering, type Badge, type Filter } from '#lib/filter.js';
	import { takenDay } from '#lib/format.js';
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

	let open = $state(false);
	// The calendar shown, and the day on it.
	let asking = $state<'start' | 'end' | null>(null);
	let value = $state('');
	const picked = $derived(badges.filter((b) => filter.badges.includes(b.id)));
	const say = (day: string) => (day ? takenDay(new Date(day)) : '…');
	const range = $derived(filter.start || filter.end ? `${say(filter.start)} – ${say(filter.end)}` : '');

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
	// The calendar takes the page's keys; Esc closes it, or the field when
	// the focus is in it.
	let field = $state<HTMLElement>();
	function key(e: KeyboardEvent) {
		if (asking) {
			e.stopPropagation();
			if (e.key === 'Escape') asking = null;
		} else if (e.key === 'Escape' && open && field?.contains(document.activeElement)) {
			e.stopPropagation();
			open = false;
			field.querySelector<HTMLElement>('.toggle')?.focus();
		}
	}
	const said = $derived([range, ...picked.map((b) => b.label)].filter(Boolean).join(', '));
</script>

<svelte:window onkeydowncapture={key} />

{#snippet day(which: 'start' | 'end', label: string)}
	<button class="field" class:on={!!filter[which]} aria-label={filter[which] ? text.searchDay(label, say(filter[which])) : label} onclick={() => ask(which)}><Icon name="calendar3" size={14} />{filter[which] ? say(filter[which]) : label}</button>
	{#if filter[which]}
		<button class="drop" in:swap aria-label={text.dropDate(label)} onclick={() => (filter = { ...filter, [which]: '' })}><Icon name="x-lg" size={12} /></button>
	{/if}
{/snippet}

<div class="search" bind:this={field}>
	<div class="input">
		<button class="toggle" aria-label={said ? text.searchPicked(said) : text.search} aria-expanded={open} onclick={() => (open = !open)}></button>
		<Icon name="search" size={16} />
		{#if !open}
			{#if range}
				<span class="field on" in:swap>
					<Icon name="calendar3" size={14} />{range}
					<button class="drop" aria-label={text.dropDate(range)} onclick={() => (filter = { ...filter, start: '', end: '' })}><Icon name="x-lg" size={12} /></button>
				</span>
			{/if}
			{#each picked as b (b.id)}<span in:swap><AppBadge id={b.id} label={b.label} icon={b.icon} on /></span>{/each}
		{/if}
		{#if open || !isFiltering(filter)}<span in:swap>{text.search}</span>{/if}
		{#if isFiltering(filter)}<button class="clear" in:swap onclick={() => (filter = noFilter)}>{text.clearSearch}</button>{/if}
	</div>
	{#if open}
		<div in:reveal out:reveal={{ leaving: true }}>
			<dl>
				{#if dated}
					<dt>{text.searchDate}</dt>
					<dd>{@render day('start', text.searchFrom)}<em>–</em>{@render day('end', text.searchTo)}</dd>
				{/if}
				{#if badges.length > 0}
					<dt>{text.searchBadge}</dt>
					<dd>
						{#each badges as b (b.id)}
							<AppBadge id={b.id} label={b.label} icon={b.icon} count={b.count} on={filter.badges.includes(b.id)} onclick={() => flip(b.id)} />
						{/each}
					</dd>
				{/if}
			</dl>
		</div>
	{/if}
</div>
{#if asking}
	<Dialog title={asking === 'start' ? text.searchFrom : text.searchTo}>
		<DatePicker bind:value time={false} />
		{#snippet actions()}
			<SecondaryButton onclick={() => (asking = null)}>{text.cancel}</SecondaryButton>
			<PrimaryButton onclick={choose}>{text.chooseDay}</PrimaryButton>
		{/snippet}
	</Dialog>
{/if}

<style>
	.search {
		margin-top: var(--space-16);
		border: 1px solid var(--border);
		border-radius: var(--radius-12);
	}
	.input {
		position: relative;
		min-height: var(--size-segmented);
		padding: var(--space-4) var(--space-12);
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-8);
		color: var(--muted);
		font-size: var(--text-body);
	}
	/* The whole field is pressed; what is in it lies over, and lets it through. */
	.toggle {
		position: absolute;
		inset: 0;
		border-radius: var(--radius-12);
	}
	.input > :global(*:not(.toggle)) {
		position: relative;
		pointer-events: none;
	}
	.input .drop,
	.input > .clear {
		pointer-events: auto;
	}
	.clear {
		margin-left: auto;
		color: var(--accent);
		font-size: var(--text-callout);
		font-weight: var(--weight-semibold);
	}
	dl {
		margin: 0;
		padding: var(--space-12);
		display: grid;
		grid-template-columns: auto 1fr;
		gap: var(--space-12) var(--space-24);
		align-items: center;
		border-top: 1px solid var(--line);
	}
	dt {
		color: var(--muted);
		font-size: var(--text-callout);
	}
	dd {
		margin: 0;
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-8);
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
