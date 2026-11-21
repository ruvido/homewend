<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- C1: the exports on Google Takeout, each account's. -->
<script module lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import type { Takeout as Seen } from '#lib/api.js';
	// Each account's list as last read, kept while the window is open.
	const seen = new Map<string, Seen[]>();
</script>

<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount, untrack } from 'svelte';
	import ContextMenu, { folderItems, type MenuItem } from '#lib/components/ContextMenu.svelte';
	import { app, signIn, sidebarOf, signInAgain, signingInAgain, startFailure, startJob } from '#lib/app.svelte.js';
	import { ApiError, folderOf, getTakeouts, readContents, type Takeout } from '#lib/api.js';
	import { bytes, clock, day } from '#lib/format.js';
	import { text } from '#lib/strings.js';
	import Avatar from '#lib/components/Avatar.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { part } from '#lib/job.js';

	const accounts = $derived(app.overview ? sidebarOf(app.overview).accounts : []);
	let account = $state('');
	// The download this account runs, to say which takeout it is.
	const job = $derived(app.jobs[account]);
	const email = $derived(accounts.find((a) => a.id === account)?.email ?? '');
	$effect(() => {
		if (!account && accounts.length > 0) account = accounts[0].id;
	});

	let list = $state<Takeout[] | null>(null);
	let failed = $state('');
	let signedOut = $state(false);
	let tries = $state(0);
	// One sign-in at a time, shared with the download's banner: none can start
	// while one is open, and the page reads the list again once this
	// account's is done, wherever it was started.
	const again = signingInAgain(() => account);
	let was = '';
	$effect(() => {
		const now = signIn.account;
		if (was === untrack(() => account) && now === '') untrack(() => tries++);
		was = now;
	});
	let reading = $state('');
	$effect(() => {
		const a = account;
		void tries;
		if (!a) return;
		// The list seen last shows at once; Google's answer replaces it.
		list = seen.get(a) ?? null;
		failed = '';
		signedOut = false;
		refused = '';
		getTakeouts(a).then(
			async (l) => {
				seen.set(a, l);
				if (account !== a) return;
				// Google's answer replaces the rows a menu was opened on.
				menu = null;
				list = l;
				// A ready takeout made on Google: read its list of files, one at a
				// time, to say what it holds.
				for (const t of l) {
					if (t.known || t.years?.length || t.status !== 'ready' || account !== a) continue;
					reading = t.id;
					try {
						t.years = (await readContents(a, t.id)).years;
						list = [...l];
					} catch {
						break; // no download from this account yet: nothing to read with
					} finally {
						reading = '';
					}
				}
			},
			(e) => {
				if (account !== a) return;
				failed = e instanceof ApiError && e.status === 424 ? text.noBrowser : text.takeoutsFailed;
				signedOut = e instanceof ApiError && e.status === 401;
			}
		);
	});

	function holds(t: Takeout) {
		if (t.known) return t.year ? text.holdsYear(t.year) : text.holdsAll;
		const y = t.years ?? [];
		if (y.length === 1) return y[0];
		if (y.length > 1) return `${y[0]}–${y[y.length - 1]}`;
		return reading === t.id ? text.reading : text.holdsUnknown;
	}

	// One row's state: its status, the line under it, its colour, and what can
	// be done. The colour is the status's, on the dot and on the button.
	type Act = 'download' | 'resume' | 'retry' | 'timer' | 'more' | '';
	type Row = { status: string; detail: string; tone: string; act: Act };
	function rowOf(t: Takeout): Row {
		const until = t.Expires ? (t.status === 'expired' ? text.since : text.until)(day(new Date(t.Expires))) : '';
		const l = t.local;
		const all = l && l.of > 0 && l.parts === l.of;
		// While Google still prepares it, it is the preparing row; its parts
		// are counted once they are known.
		if (job?.running && job.stage !== 'waiting' && job.export?.startsWith(t.id))
			return { status: text.statusDownloading, detail: job.of > 0 ? text.partsOf(Math.max(job.parts - 1, 0), part(job).of) : '', tone: 'accent', act: '' };
		if (t.status === 'preparing') return { status: text.statusPreparing, detail: '', tone: 'warn', act: 'timer' };
		if (all && (l.present ?? 0) < (l.declared ?? 0)) {
			const missing = ((l.declared ?? 0) - (l.present ?? 0)).toLocaleString('en');
			return { status: text.statusMissing(missing), detail: until, tone: 'err', act: t.status === 'expired' ? 'more' : 'retry' };
		}
		if (all) return { status: text.statusAllHere, detail: until, tone: 'ok', act: 'more' };
		if (t.status === 'expired') return { status: text.statusExpired, detail: until, tone: 'faint', act: 'more' };
		if (l) return { status: text.statusPaused, detail: text.partsOf(l.parts, l.of), tone: 'accent', act: 'resume' };
		return { status: text.statusReady, detail: until, tone: 'ok', act: 'download' };
	}

	// Why the last download asked for did not start, said above the list.
	let refused = $state('');
	async function bring(o: Parameters<typeof startJob>[0]) {
		refused = '';
		try {
			await startJob(o);
			goto('/bringing');
		} catch (e) {
			refused = startFailure(e);
		}
	}
	const download = (t: Takeout, again = false) => {
		app.picked[account] = t.id;
		return bring({ account, export: t.id, again });
	};
	// An expired takeout is asked of Google once more, as it was asked: one
	// made on Google that held a single year, as that year.
	const yearOf = (t: Takeout) => (t.known ? t.year : t.years?.length === 1 ? Number(t.years[0]) : 0);
	const askAgain = (t: Takeout) => bring({ account, year: yearOf(t) || undefined, fresh: true });
	// The ⋯ of a row: the same menu as a folder's, at the end of the button.
	// Pressed again, the ⋯ closes it.
	let menu = $state<{ id: string; anchor: Element; items: MenuItem[] } | null>(null);
	function more(e: MouseEvent, t: Takeout) {
		if (menu?.id === t.id) {
			menu = null;
			return;
		}
		const anchor = e.currentTarget as HTMLElement;
		const items: MenuItem[] =
			t.status === 'expired'
				? [{ icon: 'arrow-clockwise', label: text.askAgain, run: () => askAgain(t) }]
				: [{ icon: 'arrow-clockwise', label: text.downloadAgain, run: () => download(t, true) }];
		// Where its photos are: the profile it went in, which a rename of the
		// account's does not move.
		const local = t.local;
		if (local) items.push(...folderItems(async () => (await folderOf({ profile: local.profile || undefined, account })).path));
		menu = { id: t.id, anchor, items };
	}

	// How long Google has been preparing, counted while the page is open.
	let now = $state(Date.now());
	onMount(() => {
		const t = setInterval(() => (now = Date.now()), 1000);
		return () => clearInterval(t);
	});
	function since(iso: string) {
		const s = Math.max(0, Math.floor((now - new Date(iso).getTime()) / 1000));
		const p = (n: number) => String(n).padStart(2, '0');
		return `${Math.floor(s / 3600)}:${p(Math.floor(s / 60) % 60)}:${p(s % 60)}`;
	}
</script>

<div class="listing">
	<header>
		<div>
			<h1>{text.takeoutsTitle}</h1>
			{#if accounts.length === 1}<p class="who">{accounts[0].email}</p>{/if}
		</div>
	</header>

	{#if accounts.length > 1}
		<div class="tabs">
			{#each accounts as a, i (a.id)}
				<button class:on={account === a.id} onclick={() => (account = a.id)}>
					<Avatar account={a} index={i} />{a.name}
				</button>
			{/each}
		</div>
	{/if}

	{#if failed && list === null}
		<!-- What went wrong is the engine's to log; the person is told what
		     they can do. -->
		<div class="failed {signedOut ? 'warn' : 'accent'}">
			{#if signedOut}
				<p>{again.here ? text.signingIn : text.bannerSignedOut(email)}</p>
				<button class="pill" disabled={again.busy} onclick={() => signInAgain(account)}>{text.signInAgain}</button>
			{:else}
				<p>{failed}</p>
				<button class="pill" onclick={() => tries++}>{text.retry}</button>
			{/if}
		</div>
	{:else if list === null}
		<Skeleton label={text.readingTakeouts} />
	{:else if list.length === 0}
		<p class="muted">{text.noTakeouts}</p>
	{:else}
		{#if refused}<p class="refused">{refused}</p>{/if}
		<table>
			<thead>
				<tr><th>{text.takeoutCol}</th><th>{text.askedCol}</th><th>{text.sizeCol}</th><th>{text.statusCol}</th><th></th></tr>
			</thead>
			<tbody>
				{#each list as t (t.id)}
					{@const r = rowOf(t)}
					<tr>
						<td>
							<span class="name"><Icon name="archive" size={16} /><span><b>{holds(t)}</b><small>{t.id}</small></span></span>
						</td>
						<td>{day(new Date(t.Created))}, {clock(new Date(t.Created))}</td>
						<td>{t.Bytes ? bytes(t.Bytes) : '—'}{t.Parts?.length ? ` · ${text.parts(t.Parts.length)}` : ''}</td>
						<td>
							<span class="status {r.tone}"><i></i><span><b>{r.status}</b>{#if r.detail}<small>{r.detail}</small>{/if}</span></span>
						</td>
						<td class="act {r.tone}">
							{#if r.act === 'download'}<button class="pill" onclick={() => download(t)}>{text.download}</button>
							{:else if r.act === 'resume'}<button class="pill" onclick={() => download(t)}>{text.resume}</button>
							{:else if r.act === 'retry'}<button class="pill" onclick={() => download(t, true)}>{text.retry}</button>
							{:else if r.act === 'timer'}<span class="slot timer"><Icon name="stopwatch" size={14} />{since(t.Created)}</span>
							{:else if r.act === 'more'}
								<span class="slot"
									><button
										class="more"
										aria-haspopup="menu"
										aria-label={text.moreActions}
										onclick={(e) => more(e, t)}><Icon name="three-dots" size={16} /></button></span
								>
							{/if}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
</div>

{#if menu}<ContextMenu items={menu.items} anchor={menu.anchor} onclose={() => (menu = null)} />{/if}

<style>
	.who {
		margin-top: 8px;
		color: var(--muted);
	}
	.tabs {
		display: flex;
		gap: 8px;
	}
	.tabs button {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 12px 4px 4px;
		border: 1px solid var(--border);
		border-radius: 999px;
	}
	.tabs button.on {
		background: var(--line);
		border-color: transparent;
		font-weight: 600;
		color: var(--fg);
	}
	th:nth-child(1) {
		width: 26%;
	}
	th:nth-child(2),
	th:nth-child(3) {
		width: 17%;
	}
	th:nth-child(5) {
		width: 96px;
	}
	.refused {
		color: var(--err);
	}
	.failed .pill {
		width: auto;
		padding: 0 14px;
	}
	.failed {
		display: flex;
		align-items: center;
		gap: 16px;
		color: var(--muted);
	}
</style>
