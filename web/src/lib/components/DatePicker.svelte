<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- K2: a day on the month, Monday first, and the time in two fields. The
     value is a clock, "2026-11-07T15:27", as the photos' dates are kept. -->
<script lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import { text } from '#lib/strings.js';

	let { value = $bindable() }: { value: string } = $props();

	const parts = $derived({
		y: +value.slice(0, 4),
		m: +value.slice(5, 7),
		d: +value.slice(8, 10),
		h: value.slice(11, 13),
		min: value.slice(14, 16)
	});
	// The month shown, moved by ‹ ›, starts at the value's.
	let shown = $state({ y: 0, m: 0 });
	$effect.pre(() => {
		if (!shown.y) shown = { y: parts.y, m: parts.m };
	});
	const two = (n: number | string) => String(n).padStart(2, '0');
	const days = $derived(new Date(Date.UTC(shown.y, shown.m, 0)).getUTCDate());
	// Monday 0: the empty cells before the 1st.
	const start = $derived((new Date(Date.UTC(shown.y, shown.m - 1, 1)).getUTCDay() + 6) % 7);
	const today = new Date();
	const month = $derived(new Intl.DateTimeFormat('en', { month: 'long', year: 'numeric', timeZone: 'UTC' }).format(Date.UTC(shown.y, shown.m - 1, 1)));
	function move(by: number) {
		const m = shown.m + by;
		shown = { y: shown.y + Math.floor((m - 1) / 12), m: ((m - 1 + 12) % 12) + 1 };
	}
	const set = (y: number, m: number, d: number, h: string, min: string) => (value = `${y}-${two(m)}-${two(d)}T${h}:${min}`);
	// A field is a number in its range, two digits; anything else is the old one.
	function time(field: 'h' | 'min', raw: string) {
		const n = Number(raw);
		const ok = raw !== '' && Number.isInteger(n) && n >= 0 && n <= (field === 'h' ? 23 : 59);
		const v = ok ? two(n) : parts[field];
		set(parts.y, parts.m, parts.d, field === 'h' ? v : parts.h, field === 'min' ? v : parts.min);
	}
</script>

<div class="date">
	<div class="head">
		<button aria-label={text.previousMonth} onclick={() => move(-1)}><Icon name="chevron-left" size={16} /></button>
		<b>{month}</b>
		<button aria-label={text.nextMonth} onclick={() => move(1)}><Icon name="chevron-right" size={16} /></button>
	</div>
	<div class="days">
		{#each text.weekdays as w, i (i)}<span>{w}</span>{/each}
		{#each { length: start } as _, i (i)}<i></i>{/each}
		{#each { length: days } as _, i (i)}
			{@const d = i + 1}
			<button
				class:on={shown.y === parts.y && shown.m === parts.m && d === parts.d}
				class:today={shown.y === today.getFullYear() && shown.m === today.getMonth() + 1 && d === today.getDate()}
				onclick={() => set(shown.y, shown.m, d, parts.h, parts.min)}>{d}</button
			>
		{/each}
	</div>
	<div class="time">
		<span>{text.time}</span>
		<input value={parts.h} aria-label={text.hour} inputmode="numeric" maxlength="2" onchange={(e) => time('h', e.currentTarget.value)} />:
		<input value={parts.min} aria-label={text.minute} inputmode="numeric" maxlength="2" onchange={(e) => time('min', e.currentTarget.value)} />
	</div>
</div>

<style>
	.date {
		display: flex;
		flex-direction: column;
		gap: var(--space-12);
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}
	.head b {
		font-size: var(--text-title-3);
		font-weight: var(--weight-semibold);
		color: var(--fg);
	}
	.head button {
		width: var(--space-32);
		height: var(--space-32);
		display: grid;
		place-items: center;
		border-radius: var(--radius-8);
		color: var(--muted);
	}
	.head button:hover {
		background: var(--soft);
		color: var(--fg);
	}
	.days {
		display: grid;
		grid-template-columns: repeat(7, 1fr);
		gap: var(--space-4);
		text-align: center;
	}
	.days span {
		padding-bottom: var(--space-4);
		font-size: var(--text-callout);
		color: var(--faint);
	}
	.days button {
		aspect-ratio: 1;
		border-radius: var(--radius-full);
		font-size: var(--text-body);
		color: var(--text);
	}
	.days button:hover {
		background: var(--soft);
	}
	.days button.today {
		color: var(--accent);
		font-weight: var(--weight-semibold);
	}
	.days button.on {
		background: var(--accent);
		color: var(--on-accent);
		font-weight: var(--weight-semibold);
	}
	.time {
		display: flex;
		align-items: center;
		gap: var(--space-8);
		color: var(--muted);
	}
	.time span {
		flex: 1;
	}
	.time input {
		width: var(--space-40);
		height: var(--space-32);
		border: 1px solid var(--border);
		border-radius: var(--radius-8);
		background: var(--bg);
		color: var(--fg);
		font: inherit;
		text-align: center;
		font-variant-numeric: tabular-nums;
	}
</style>
