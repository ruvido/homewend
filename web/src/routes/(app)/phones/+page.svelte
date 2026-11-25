<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- P1–P3, P5: the phones paired with this computer, and those no longer; Add a phone shows the code. -->
<script lang="ts">
	import { onMount } from 'svelte';
	import { flip } from 'svelte/animate';
	import { moving, reveal, swap } from '#lib/motion.js';
	import ContextMenu, { type MenuItem } from '#lib/components/ContextMenu.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import PairCard from '#lib/components/PairCard.svelte';
	import PrimaryButton from '#lib/components/PrimaryButton.svelte';
	import Whose from '#lib/components/Whose.svelte';
	import Avatar from '#lib/components/Avatar.svelte';
	import Dialog from '#lib/components/Dialog.svelte';
	import SecondaryButton from '#lib/components/SecondaryButton.svelte';
	import { app, personOf } from '#lib/app.svelte.js';
	import { ApiError, getPhones, startPairing, stopPairing, unpair, type Phone, type Unpaired } from '#lib/api.js';
	import { clock, day, span } from '#lib/format.js';
	import { text } from '#lib/strings.js';

	// null until the first answer: "No phone yet" never flashes before the list.
	let phones = $state<Phone[] | null>(null);
	let unpaired = $state<Unpaired[]>([]);
	let failed = $state('');
	// The code shown, while a phone is awaited.
	let code = $state('');
	let adding = $state(false);
	let typed = $state('');
	// A name being typed for someone else: no code until it is done, or a
	// phone scanning meanwhile would file into half a name.
	let naming = $state(false);
	// Bumped to ask a new code for the same profile: the last one expired.
	let renew = $state(0);
	// P6: whose phone it is is asked every time, nobody chosen beforehand: a
	// default went unnoticed, and a phone backed up into someone else's.
	let asking = $state(false);
	const profile = $derived(typed.trim());
	function whose(p: string) {
		typed = p;
		naming = p === '';
		asking = false;
		adding = true;
	}
	let menu = $state<{ items: MenuItem[]; anchor: HTMLElement } | null>(null);

	async function look() {
		try {
			const p = await getPhones();
			if (code && !p.pairing) {
				// A phone used the code: the card gives way to its row. No new
				// phone: the code expired, and a new one takes its place.
				if (p.phones.length > (phones?.length ?? 0)) ((code = ''), (adding = false));
				else renew++;
			}
			phones = p.phones;
			unpaired = p.unpaired;
			failed = '';
		} catch (e) {
			failed = String(e);
		}
	}
	onMount(() => {
		look();
		// Faster while a phone is awaited, so its row comes as it pairs.
		let t: ReturnType<typeof setTimeout>;
		let gone = false;
		const tick = () => (t = setTimeout(async () => (await look(), gone || tick()), code ? 1000 : 10000));
		tick();
		return () => {
			gone = true;
			clearTimeout(t);
			if (adding) stopPairing();
		};
	});

	// The code is the profile's: another chosen, another code.
	$effect(() => {
		const p = profile;
		void renew;
		if (!adding || !p || naming) return;
		startPairing(p).then(
			(r) => adding && profile === p && (code = r.qr),
			(e) => (failed = e instanceof ApiError && e.status === 422 ? text.profileCase : String(e))
		);
	});
	function cancel() {
		stopPairing();
		((code = ''), (adding = false), (naming = false), (typed = ''));
	}

	// shortcut: the state is read from when the phone was last heard; "Backing
	// up" with its "818 of 1,032" needs the phone to say how many are left.
	const minute = 60_000;
	type State = 'up' | 'going' | 'away' | 'paired';
	const states: Record<State, { tone: string; status: string }> = {
		away: { tone: 'warn', status: text.phoneNotSeen },
		going: { tone: 'accent', status: text.phoneBackingUp },
		up: { tone: 'ok', status: text.phoneBackedUp },
		paired: { tone: 'faint', status: text.phonePaired }
	};
	function stateOf(p: Phone): State {
		const now = Date.now();
		if (p.seen && now - Date.parse(p.seen) > 7 * 24 * 60 * minute) return 'away';
		if (p.backup && now - Date.parse(p.backup) < 2 * minute) return 'going';
		return p.photos > 0 ? 'up' : 'paired';
	}
	const counts = $derived.by(() => {
		const n = { up: 0, going: 0, away: 0, paired: 0 };
		for (const p of phones ?? []) n[stateOf(p)]++;
		return n;
	});

	function more(e: MouseEvent, p: Phone) {
		menu = {
			anchor: e.currentTarget as HTMLElement,
			items: [{ icon: 'plug', label: text.unpair, run: async () => (await unpair(p.id), look()) }]
		};
	}
</script>

<div class="listing">
	<header>
		<div>
			<h1>{text.phones}</h1>
			{#if phones}{#if phones.length}<p in:reveal out:reveal={{ leaving: true }}>{text.phonesLine(counts)}</p>{/if}{/if}
		</div>
		{#if !adding}<PrimaryButton onclick={() => (asking = true)}><Icon name="plus-lg" size={16} />{text.addPhone}</PrimaryButton>{/if}
	</header>
	{#if failed}<p class="err">{failed}</p>{/if}
	{#if adding}
		<!-- P2: whose phone, then its code, coming as one block; the code's
		     place kept until the code comes. -->
		<div class="adding" in:reveal out:reveal={{ leaving: true }}>
			<Whose bind:typed bind:naming {profile} />
			<PairCard code={naming ? '' : code} oncancel={cancel} />
		</div>
	{/if}
	{#if asking}
		<Dialog title={text.whosePhone}>
			<p class="muted">{text.whosePhoneLead}</p>
			<div class="who">
				{#each app.overview?.profiles ?? [] as p (p)}
					<button onclick={() => whose(p)}><Avatar account={{ id: p, name: p, email: '', shown: true }} index={personOf(p)} /><b>{p}</b></button>
				{/each}
				<button onclick={() => whose('')}><span class="glyph"><Icon name="person-plus" size={16} /></span><b>{text.someoneElse}</b></button>
			</div>
			{#snippet actions()}
				<SecondaryButton onclick={() => (asking = false)}>{text.cancel}</SecondaryButton>
			{/snippet}
		</Dialog>
	{/if}
	{#if phones?.length === 0 && !unpaired.length && !adding}
		<p class="muted">{text.noPhones}</p>
	{/if}
	<!-- Inside the first answer's block: what it brings does not move in. -->
	{#if phones}
		{#if phones.length}
			<div in:reveal out:reveal={{ leaving: true }}>
				<table>
					<thead>
						<tr><th>{text.phoneCol}</th><th>{text.accountCol}</th><th>{text.lastBackupCol}</th><th>{text.backedUpCol}</th><th>{text.statusCol}</th><th></th></tr>
					</thead>
					<tbody>
						{#each phones as p (p.id)}
							{@const s = states[stateOf(p)]}
							<tr out:swap={{ leaving: true }} animate:flip={moving()}>
								<td><span class="name"><Icon name="phone" size={16} /><b>{p.name}</b></span></td>
								<td>{p.profile}</td>
								<td>{p.backup ? `${day(new Date(p.backup))}, ${clock(new Date(p.backup))}` : '—'}</td>
								<td>{p.photos.toLocaleString('en')}</td>
								<td><span class="status {s.tone}"><i></i><b>{s.status}</b></span></td>
								<td class="act">
									<span class="slot"
										><button class="more" aria-haspopup="menu" aria-label={text.moreActions} onclick={(e) => more(e, p)}
											><Icon name="three-dots" size={16} /></button
										></span
									>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
		{#if unpaired.length}
			<div class="unpaired" in:reveal>
				<table>
					<thead>
						<tr><th>{text.unpairedCol}</th><th>{text.accountCol}</th><th>{text.pairedCol}</th><th>{text.broughtCol}</th><th>{text.byCol}</th><th></th></tr>
					</thead>
					<tbody>
						{#each unpaired as p (p.unpaired)}
							<tr in:swap animate:flip={moving()}>
								<td><span class="name"><Icon name="phone" size={16} /><b>{p.name}</b></span></td>
								<td>{p.profile}</td>
								<td>{span(new Date(p.paired), new Date(p.unpaired))}</td>
								<td>{p.photos.toLocaleString('en')}</td>
								<td><span class="status faint"><i></i><b>{text.byWhom[p.by]}</b></span></td>
								<td class="act"><span class="slot"></span></td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	{/if}
</div>

{#if menu}<ContextMenu items={menu.items} anchor={menu.anchor} onclose={() => (menu = null)} />{/if}

<style>
	/* One box for the block to open in: the listing's column, again inside it. */
	.adding {
		display: flex;
		flex-direction: column;
		gap: var(--space-24);
	}
	header {
		display: flex;
		align-items: center;
		gap: var(--space-24);
	}
	header p {
		color: var(--muted);
	}
	header > div {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: var(--space-8);
	}
	.err {
		color: var(--err);
	}
	/* Leaving, a row is taken out of the table to fade where it was (svelte's
	   flip): it keeps its columns as a table of its own. */
	tr:global([inert]) {
		display: table;
		table-layout: fixed;
	}
	:is(th, td):nth-child(1) {
		width: 26%;
	}
	:is(th, td):nth-child(6) {
		width: 96px;
	}
	/* Too narrow for equal columns to hold the two dates (135px with their
	   gap): each takes its content, the empty one nothing. */
	.unpaired {
		container-type: inline-size;
	}
	/* Empty, it has no baseline: a row is as tall as the paired ones. */
	.unpaired .slot {
		vertical-align: top;
	}
	@container (max-width: 860px) {
		.unpaired table {
			table-layout: auto;
		}
		.unpaired :is(th, td):nth-child(6) {
			width: 0;
			padding-right: 0;
		}
		.unpaired .slot {
			width: 0;
		}
	}
	/* P6: one row a person, a click goes on to the code. */
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
</style>
