<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- P1–P3: the phones paired with this computer; Add a phone shows the code. -->
<script lang="ts">
	import { onMount } from 'svelte';
	import ContextMenu, { type MenuItem } from '#lib/components/ContextMenu.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import PairCard from '#lib/components/PairCard.svelte';
	import PrimaryButton from '#lib/components/PrimaryButton.svelte';
	import Whose from '#lib/components/Whose.svelte';
	import { app } from '#lib/app.svelte.js';
	import { getPhones, startPairing, stopPairing, unpair, type Phone } from '#lib/api.js';
	import { clock, day } from '#lib/format.js';
	import { text } from '#lib/strings.js';

	// null until the first answer: "No phone yet" never flashes before the list.
	let phones = $state<Phone[] | null>(null);
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
	const profile = $derived(typed.trim() || app.overview?.profile || app.overview?.profiles?.[0] || '');
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
			(e) => (failed = String(e))
		);
	});
	function cancel() {
		stopPairing();
		((code = ''), (adding = false));
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
			{#if phones?.length}<p>{text.phonesLine(counts)}</p>{/if}
		</div>
		{#if !adding}<PrimaryButton onclick={() => (adding = true)}><Icon name="plus-lg" size={16} />{text.addPhone}</PrimaryButton>{/if}
	</header>
	{#if failed}<p class="err">{failed}</p>{/if}
	{#if adding}
		<!-- P2: whose phone first, then its code. -->
		<Whose bind:typed bind:naming {profile} />
		<!-- At once, its code's place kept until the code comes. -->
		<PairCard code={naming ? '' : code} oncancel={cancel} />
	{/if}
	{#if phones?.length === 0 && !adding}
		<p class="muted">{text.noPhones}</p>
	{:else if phones?.length}
		<table>
			<thead>
				<tr><th>{text.phoneCol}</th><th>{text.accountCol}</th><th>{text.lastBackupCol}</th><th>{text.backedUpCol}</th><th>{text.statusCol}</th><th></th></tr>
			</thead>
			<tbody>
				{#each phones as p (p.id)}
					{@const s = states[stateOf(p)]}
					<tr>
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
	{/if}
</div>

{#if menu}<ContextMenu items={menu.items} anchor={menu.anchor} onclose={() => (menu = null)} />{/if}

<style>
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
	th:nth-child(1) {
		width: 26%;
	}
	th:nth-child(6) {
		width: 96px;
	}
</style>
