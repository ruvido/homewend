<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- C1, C5: the exports on Google Takeout, each account's, and those no longer there. -->
<script module lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import type { Takeout as Seen } from '#lib/api.js';
	// Each account's list as last read, kept while the window is open.
	const seen = new Map<string, Seen[]>();
</script>

<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount, untrack } from 'svelte';
	import { flip } from 'svelte/animate';
	import { moving, reveal, swap } from '#lib/motion.js';
	import ContextMenu, { folderItems, type MenuItem } from '#lib/components/ContextMenu.svelte';
	import { app, signIn, sidebarOf, signInAgain, signingInAgain, startFailure, startJob, personOfAccount } from '#lib/app.svelte.js';
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
	// Google not answering: the takeouts asked for, as this computer knows them.
	let local = $state<Takeout[]>([]);
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
		local = [];
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
				getTakeouts(a, true).then((l) => account === a && (local = l), () => {});
			}
		);
	});

	const here = $derived(list?.filter((t) => t.status !== 'expired') ?? []);
	const gone = $derived(list?.filter((t) => t.status === 'expired') ?? []);
	const line = $derived(
		[accounts.length === 1 && accounts[0].email, list?.length && text.takeoutsLine(here.length, gone.length)].filter(Boolean).join(' · ')
	);

	// Its bytes, and its parts under them where the window is narrow.
	function size(t: Takeout): [string, string] {
		const b = t.Bytes || t.local?.bytes;
		const n = t.Parts?.length || t.local?.of;
		return [b ? bytes(b) : n ? '' : '—', n ? text.parts(n) : ''];
	}
	// As its last count said, when its download ended.
	const inLibrary = (t: Takeout) => (t.local?.declared ? text.nPhotos(t.local.present ?? 0) : '—');
	const missing = (t: Takeout) => (t.local?.declared ?? 0) - (t.local?.present ?? 0);

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
		const until = t.Expires ? text.until(day(new Date(t.Expires))) : '';
		const l = t.local;
		const all = l && l.of > 0 && l.parts === l.of;
		// While Google still prepares it, it is the preparing row; its parts
		// are counted once they are known.
		if (job?.running && job.stage !== 'waiting' && job.export?.startsWith(t.id))
			return { status: text.statusDownloading, detail: job.of > 0 ? text.partsOf(Math.max(job.parts - 1, 0), part(job).of) : '', tone: 'accent', act: '' };
		if (t.status === 'preparing') return { status: text.statusPreparing, detail: '', tone: 'warn', act: 'timer' };
		if (all && missing(t) > 0) return { status: text.statusMissing(missing(t).toLocaleString('en')), detail: until, tone: 'err', act: 'retry' };
		if (all) return { status: text.statusAllHere, detail: until, tone: 'ok', act: 'more' };
		if (l) return { status: text.statusPaused, detail: text.partsOf(l.parts, l.of), tone: 'accent', act: 'resume' };
		return { status: text.statusReady, detail: until, tone: 'ok', act: 'download' };
	}

	// One no longer on Google, or Google not answering: how it ended here.
	function pastOf(t: Takeout): { status: string; tone: string } {
		const l = t.local;
		// Its library is away: what came is not known.
		if (!l) return { status: app.overview?.gone ? '—' : text.statusNotDownloaded, tone: 'faint' };
		if (!l.declared) return { status: l.of ? text.partsOf(l.parts, l.of) : text.parts(l.parts), tone: 'warn' };
		return missing(t) > 0 ? { status: text.statusMissing(missing(t).toLocaleString('en')), tone: 'warn' } : { status: text.statusAllHere, tone: 'ok' };
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
	// The ⋯ of a row: the same menu as a folder's, at the end of the button.
	// Pressed again, the ⋯ closes it.
	let menu = $state<{ id: string; anchor: Element; items: MenuItem[] } | null>(null);
	function more(e: MouseEvent, t: Takeout) {
		if (menu?.id === t.id) {
			menu = null;
			return;
		}
		const anchor = e.currentTarget as HTMLElement;
		const items: MenuItem[] = [{ icon: 'arrow-clockwise', label: text.downloadAgain, run: () => download(t, true) }];
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
			{#if line}<p class="line" in:reveal out:reveal={{ leaving: true }}>{line}</p>{/if}
		</div>
	</header>

	{#if accounts.length > 1}
		<div class="tabs">
			{#each accounts as a (a.id)}
				<button class:on={account === a.id} onclick={() => (account = a.id)}>
					<Avatar account={a} index={personOfAccount(a.id)} />{a.name}
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
		{#if local.length}{@render past(text.takeoutCol, local)}{/if}
	{:else if list === null}
		<Skeleton label={text.readingTakeouts} />
	{:else if list.length === 0}
		<p class="muted">{text.noTakeouts}</p>
	{:else}
		{#if refused}<p class="refused">{refused}</p>{/if}
		{#if here.length}
			<div class="table" in:reveal out:reveal={{ leaving: true }}>
				<table>
					{@render head(text.takeoutCol)}
					<tbody>
						{#each here as t (t.id)}
							{@const r = rowOf(t)}
							<tr in:swap animate:flip={moving()}>
								{@render facts(t)}
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
			</div>
		{/if}
		{#if gone.length}{@render past(text.goneCol, gone)}{/if}
	{/if}
</div>

{#snippet head(name: string)}
	<thead>
		<tr><th>{name}</th><th>{text.askedCol}</th><th>{text.sizeCol}</th><th>{text.inLibraryCol}</th><th>{text.statusCol}</th><th></th></tr>
	</thead>
{/snippet}

{#snippet two(first: string, sep: string, then: string)}
	{first}{#if first && then}<span class="sep">{sep}</span>{/if}{#if then}<span class="then">{then}</span>{/if}
{/snippet}

<!-- Rows told from this computer: how each ended here, nothing to do. -->
{#snippet past(name: string, rows: Takeout[])}
	<div class="table" in:reveal out:reveal={{ leaving: true }}>
		<table>
			{@render head(name)}
			<tbody>
				{#each rows as t (t.id)}
					{@const r = pastOf(t)}
					<tr in:swap animate:flip={moving()}>
						{@render facts(t)}
						<td><span class="status {r.tone}"><i></i><b>{r.status}</b></span></td>
						<td class="act"></td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/snippet}

{#snippet facts(t: Takeout)}
	<td>
		<span class="name"><Icon name="archive" size={16} /><span><b>{holds(t)}</b><small>{t.id}</small></span></span>
	</td>
	<td>{@render two(day(new Date(t.Created)), ', ', clock(new Date(t.Created)))}</td>
	<td>{@render two(size(t)[0], ' · ', size(t)[1])}</td>
	<td>{inLibrary(t)}</td>
{/snippet}

{#if menu}<ContextMenu items={menu.items} anchor={menu.anchor} onclose={() => (menu = null)} />{/if}

<style>
	.line {
		margin-top: var(--space-8);
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
	th:nth-child(6) {
		width: 96px;
	}
	/* Too narrow for six columns on one line: a time and a count of parts go
	   under what they belong to, as the id is under the name. */
	.table {
		container-type: inline-size;
	}
	.sep {
		white-space: pre;
	}
	@container (max-width: 860px) {
		.sep {
			display: none;
		}
		.then {
			display: block;
			font-size: var(--text-subheadline);
			color: var(--faint);
		}
		th:nth-child(2) {
			width: 10%;
		}
		th:nth-child(3) {
			width: 11%;
		}
		th:nth-child(4) {
			width: 17%;
		}
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
