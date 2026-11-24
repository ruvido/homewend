<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- I1–I8: a card or a folder, what comes in, the copy, a card taken out, the end.
     Each title answers what a person wants to know there; the numbers that
     matter are the biggest thing on the screen. -->
<script lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import Whose from '#lib/components/Whose.svelte';
	import { swap } from '#lib/motion.js';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { app, cards as cardList, lookCards, refresh, watchCards } from '#lib/app.svelte.js';
	import {
		ApiError,
		BRING_ALL_BACK,
		getLatest,
		getImport,
		getLibrary,
		planImport,
		startImport,
		stopImport,
		type GridPhoto,
		againImport,
		type Choice,
		type Filed,
		type ImportAsk,
		type ImportGroup,
		type ImportPlan,
		type Redate,
		type ImportState
	} from '#lib/api.js';
	import { bytes, roughly, takenDay } from '#lib/format.js';
	import { forget, pace } from '#lib/pace.js';
	import { text } from '#lib/strings.js';
	import PrimaryButton from '#lib/components/PrimaryButton.svelte';
	import SecondaryButton from '#lib/components/SecondaryButton.svelte';
	import ImportDates from './ImportDates.svelte';
	import ImportPick from './ImportPick.svelte';
	import Arrived from '#lib/components/Arrived.svelte';
	import YearsPanel from '#lib/components/YearsPanel.svelte';

	let im = $state<ImportState | null>(null);
	// A look again of the plan on screen keeps it there, untouchable, until
	// the new one comes: a tick does not flash the reading screen.
	let kept = $state<ImportPlan | undefined>();
	const plan = $derived(im?.plan ?? kept);
	const cards = $derived(cardList.list);
	let free = $state(0);
	// An import that ended before this window opened is not shown again: the
	// screen starts at the choice. One under way, or a card out, is.
	let choosing = $state(true);

	// Whose photos they are: a name chosen or typed, else the one the engine
	// goes to, else the first there is. "Into ron's photos. Change" (option A).
	const profiles = $derived(app.overview?.profiles ?? []);
	let typed = $state('');
	const profile = $derived(typed.trim() || app.overview?.profile || profiles[0] || '');

	// How the folder is named on every screen, as the engine names it.
	const name = $derived(im?.top ?? '');
	// Said by the engine when the run began: a card taken out is no longer
	// in the list, and is still a card.
	const isCard = $derived(!!im?.card);

	type View = 'choose' | 'reading' | 'plan' | 'copying' | 'taken-out' | 'done' | 'stopped' | 'failed';
	const view = $derived<View>(
		!im || (choosing && !im.running && im.problem !== 'taken-out')
			? 'choose'
			: im.running
				? im.kind === 'plan'
					? kept
						? 'plan'
						: 'reading'
					: 'copying'
				: im.problem === 'taken-out'
					? 'taken-out'
					: im.kind === 'import' && im.imported && !im.error
						? im.stopped
							? 'stopped'
							: 'done'
						: im.kind === 'plan' && im.plan && !im.error
							? 'plan'
							: im.stopped
								? 'choose'
								: 'failed'
	);

	async function load() {
		try {
			const s = await getImport();
			// The cards are looked at again before a card taken out is shown:
			// a list from before it came out would carry the copy on at once.
			if (s.problem === 'taken-out') await lookCards();
			im = s;
		} catch {
			// The engine answers within a second; the next ask will do.
		}
	}
	// The engine has answered once: the step shown is the right one.
	let ready = $state(false);
	onMount(() => {
		load().then(() => {
			ready = true;
			// A copy under way, or waiting for its card, is where the window opens.
			if (im?.running || im?.problem === 'taken-out') {
				choosing = false;
				if (im.profile) typed = im.profile;
				// What was given before the window closed goes on with it.
				redates = im.redates ?? [];
				choice = im.choice ?? null;
				bringBack = im.bring_back ?? [];
			}
		});
		// The cards come and go: one put in while the window is open, the
		// one taken out put back.
		const unwatch = watchCards();
		getLibrary().then((l) => (free = l.free), () => {});
		const t = setInterval(() => {
			if (im?.running) load();
		}, 1000);
		return () => (clearInterval(t), unwatch());
	});

	// What came in is in the sidebar's counts and years at once, and its
	// last photos at the end.
	let latest = $state<GridPhoto[]>([]);
	$effect(() => {
		if (view === 'done' || view === 'stopped') {
			refresh();
			getLatest().then((l) => (latest = l), () => {});
		}
	});
	// During the copy, the last photos in, as a download shows them.
	$effect(() => {
		if (view !== 'copying') return;
		const load = () => getLatest().then((l) => (latest = l), () => {});
		load();
		const t = setInterval(load, 5000);
		return () => clearInterval(t);
	});

	// The card put back in: the look or the copy carries on by itself. What
	// is in already is found by content and not copied again.
	let restarting = false;
	$effect(() => {
		if (!restarting && view === 'taken-out' && im?.dir && im.profile && cards.some((c) => c.path === im?.dir)) {
			restarting = true;
			carryOn().finally(() => (restarting = false));
		}
	});
	// The engine keeps what was asked, and runs it again.
	const carryOn = () => start(againImport);

	async function start(call: () => Promise<ImportState>, keep?: ImportPlan) {
		kept = keep;
		choosing = false;
		// A run measures its own pace, not the last one's.
		forget('import');
		try {
			im = await call();
		} catch (e) {
			// A second click while the first one runs: that run is the one shown.
			if (e instanceof ApiError && e.status === 409) return load();
			const said = e instanceof ApiError && e.status === 400 ? text.badProfile : (e as Error).message;
			im = { running: false, n: 0, of: 0, done: 0, total: 0, moved: 0, bytes: 0, error: said };
		}
	}
	// A card or a folder comes in at once, each group as its kind says, like
	// the command line; what stayed out is shown at the end (J1–J3).
	function bringIn(dir: string) {
		if (!profile) return;
		redates = [];
		link = false;
		choice = null;
		bringBack = [];
		start(() => startImport({ dir, profile }));
	}
	// After it: the look at the folder, to pick from a group left out, bring
	// back what was deleted, or give dates; the copy that follows adds them.
	function lookAt(group: string | null, dates = false) {
		// The room left is read again: the import took some.
		getLibrary().then((l) => (free = l.free), () => {});
		picking = group;
		dating = dates;
		start(() => planImport(asking()));
	}

	// Dates given to photos a reset clock dated wrong (I3b): kept here
	// until the copy, and looked at again with them, which reads nothing
	// twice.
	let redates = $state<Redate[]>([]);
	let dating = $state(false);
	// Photos deleted from the library come in again only when ticked, by
	// name, or all.
	let bringBack = $state<string[]>([]);
	function back(name: string) {
		bringBack = bringBack.includes(name) ? bringBack.filter((n) => n !== name) : [...bringBack, name];
		lookAgain();
	}
	function backAll(on: boolean) {
		bringBack = on ? [BRING_ALL_BACK] : [];
		lookAgain();
	}

	// What the person has said so far, for the next look or the copy.
	const asking = (): ImportAsk => ({ dir: im!.dir!, profile: im!.profile || profile, redates, choice, bring_back: bringBack });
	const lookAgain = () => start(() => planImport(asking()), plan);

	// Which of the folder's groups come in (I4), and the photos picked from
	// one that stays out (I5): null, each as its kind says. A change looks
	// again, which reads nothing twice.
	let choice = $state<Choice | null>(null);
	let picking = $state<string | null>(null);
	function rechoose(next: Choice) {
		choice = next;
		lookAgain();
	}
	const without = (list: string[], s: string) => list.filter((x) => x !== s);
	function tick(g: ImportGroup) {
		const c = choice ?? { out: [], in: [], pick: [] };
		rechoose(
			g.in
				? { ...c, in: without(c.in, g.folder), out: [...without(c.out, g.folder), g.folder] }
				: { ...c, out: without(c.out, g.folder), in: [...without(c.in, g.folder), g.folder] }
		);
	}
	// Each file's group, by its path: a folder may hold a hundred thousand.
	const groupOf = $derived(new Map((plan?.files ?? []).map((f) => [f.path, f.group])));
	const pickedIn = (g: string) => (choice?.pick ?? []).filter((p) => groupOf.get(p) === g);
	// What would come in: the files of the groups in, and those picked.
	const coming = $derived.by(() => {
		const whole = new Set((plan?.groups ?? []).filter((g) => g.in).map((g) => g.folder));
		const picked = new Set(choice?.pick ?? []);
		return (plan?.files ?? []).filter((f) => whole.has(f.group) || picked.has(f.path));
	});
	function pick(g: string, paths: string[]) {
		const c = choice ?? { out: [], in: [], pick: [] };
		const others = c.pick.filter((p) => groupOf.get(p) !== g);
		picking = null;
		rechoose({ ...c, pick: [...others, ...paths] });
	}
	const range = (g: ImportGroup) =>
		g.first && g.last ? text.takenRange(takenDay(new Date(g.first)), takenDay(new Date(g.last))) : '';
	function give(names: string[], when: string) {
		// A photo given a date again keeps only the last.
		const others = redates
			.map((r) => ({ ...r, names: r.names.filter((n) => !names.includes(n)) }))
			.filter((r) => r.names.length > 0);
		redates = [...others, { names, when }];
		dating = false;
		lookAgain();
	}
	async function chooseFolder() {
		const picked = await window.shell?.chooseFolder();
		if (picked) bringIn(picked);
	}

	// Hard links rather than copies: asked each time, copy unless said.
	let link = $state(false);
	// The years opened to show their months.
	let opened = $state(new Set<string>());
	function toggle(year: string) {
		const next = new Set(opened);
		if (!next.delete(year)) next.add(year);
		opened = next;
	}
	// What is in of a year, from its months filed.
	const filedIn = (filed: Filed, year: string) => Object.values(filed[year] ?? {}).reduce((n, k) => n + k, 0);
	const counts = (c: { photos: number; videos: number }) =>
		`${c.photos ? c.photos.toLocaleString('en') : '—'}${c.videos ? ` · ${c.videos.toLocaleString('en')}` : ''}`;
	const fits = $derived(!plan || !free || plan.bytes <= free);
	// The copy's speed and time left, as a download's are measured.
	const run = $derived(im?.running && im.kind === 'import' ? pace('import', im.moved, im.bytes) : null);
	const fileShare = $derived(im && im.total > 0 ? im.done / im.total : 0);
	const share = $derived(im && im.of > 0 ? (im.n - 1 + fileShare) / im.of : 0);
</script>

<!-- J2, J3: the years filed so far, newest first, photos and videos apart
     (raw files among the photos, as the numbers count them); done, each
     with its check. -->
{#snippet filedYears(done: boolean)}
	{@const filed = im?.filed ?? {}}
	{@const years = Object.keys(filed).sort((x, y) => (y || '0').localeCompare(x || '0'))}
	{#if years.length > 0}
		<YearsPanel
			title={text.whosePhotos(im?.profile || profile)}
			legend={[text.kindPhotos, text.kindVideos]}
			spacer={done}
		>
			{#each years as y (y)}
				{@const videos = im?.filedVideos?.[y] ?? 0}
				<div class="year">
					<span class="folder"><Icon name="folder" size={18} /></span>
					<span class="name">{y || text.noDate}</span>
					<span class="count n" class:done>{(filedIn(filed, y) - videos).toLocaleString('en')}</span>
					<span class="count n" class:done>{videos.toLocaleString('en')}</span>
					{#if done}<span class="state"><Icon name="check-circle" size={18} /></span>{/if}
				</div>
			{/each}
		</YearsPanel>
	{/if}
{/snippet}

<!-- The years, newest first; a year opened shows its months below it. -->
{#snippet byYear(years: ImportPlan['years'])}
	{#if years.length > 0}
		<h2>{text.byYearImport}</h2>
		<div class="table">
			<div class="th"><span class="grow">{text.colYear}</span><span>{text.byYearImportLegend}</span></div>
			{#each years as y (y.year)}
				{@const open = opened.has(y.year)}
				<button class="row year" class:open disabled={!y.months?.length} onclick={() => toggle(y.year)}>
					<span class="chev">{#if y.months?.length}<Icon name="chevron-right" size={16} />{/if}</span>
					<span class="grow name">{y.year || text.noDate}</span>
					<span class="count">{counts(y)}</span>
				</button>
				{#if open}
					{#each y.months ?? [] as m (m.month)}
						<div class="row month">
							<span class="grow">{text.monthNames[m.month - 1]}</span>
							<span class="count">{counts(m)}</span>
						</div>
					{/each}
				{/if}
			{/each}
		</div>
	{/if}
{/snippet}

<!-- A plan looked at again takes no click until the new one comes: it would be lost. -->
<div class="page" inert={!!im?.running && !!kept}>
	<!-- Nothing until the engine has said where the import is: never the
	     choice shown a moment before the copy under way. Then each step
	     gives way to the next in place. -->
	{#if !ready}<Skeleton label={text.readingImport} />
	{:else}{#key `${view}/${picking ?? ''}/${dating}`}<div class="step" in:swap out:swap={{ leaving: true }}>
	{#if view === 'choose'}
		<!-- J1: the library the photos go to, then a card or a folder, brought in at once. -->
		<h1>{cards.length > 0 ? text.importChoose : text.importNoCard}</h1>
		{#if cards.length === 0}<p class="lead">{text.importNoCardLead}</p>{/if}
		<Whose bind:typed {profile} />
		<!-- One grid: the buttons share one width and one edge. -->
		<div class="sources">
			{#each cards as c (c.path)}
				<span class="tile"><Icon name="hdd" size={22} /></span>
				<span class="what"><b>{c.name}</b><small class="mono">{c.path}</small></span>
				<span class="act"><PrimaryButton disabled={!profile} onclick={() => bringIn(c.path)}>{text.bringIn}</PrimaryButton></span>
			{/each}
			<span class="tile"><Icon name="folder" size={22} /></span>
			<span class="what"><b>{text.anotherFolder}</b><small>{text.anotherFolderDesc}</small></span>
			<!-- The system's own window picks the folder: the desktop app has it, a browser does not. -->
			<span class="act"
				><SecondaryButton disabled={!profile || !window.shell} onclick={chooseFolder}>{text.chooseFolderEllipsis}</SecondaryButton></span
			>
		</div>
	{:else if view === 'reading' && im}
		<!-- I2: every file read once, to know it; nothing copied yet. -->
		<span class="pill live"><span class="spin"><Icon name="arrow-repeat" size={14} /></span>{text.importReading(name)}</span>
		<h1>{text.importReadingTitle}</h1>
		<p class="lead">{text.importReadingLead}</p>
		<div class="track"><b style:width="{share * 100}%"></b></div>
		{#if im.of > 0}<div class="numbers"><div><b>{text.nOf(im.n, im.of)}</b><span>{text.filesRead}</span></div></div>{/if}
		<div class="actions"><SecondaryButton onclick={() => stopImport().then((s) => ((im = s), (choosing = true)))}>{text.importStop}</SecondaryButton></div>
	{:else if view === 'plan' && plan && im && picking !== null && plan.groups.some((g) => g.folder === picking)}
		<ImportPick
			group={plan.groups.find((g) => g.folder === picking)!}
			files={plan.files}
			picked={pickedIn(picking)}
			onpick={(paths) => pick(picking!, paths)}
			onback={() => (picking = null)}
		/>
	{:else if view === 'plan' && plan && im && dating}
		<ImportDates files={coming} {redates} onuse={give} onback={() => (dating = false)} />
	{:else if view === 'plan' && plan && im}
		<!-- I3: what comes in, folder by folder and by year, beside what happens. -->
		<div class="plan">
			<section class="main">
				<span class="pill">{name}</span>
				<h1>{text.toCopy(plan.photos, plan.videos)}</h1>
				<!-- How it ends, as the engine says it, for this screen and the command line alike. -->
				{#if plan.outcome !== 'new'}<p class="lead">{text.planLead[plan.outcome](plan)}</p>{:else if plan.in_library > 0}<p class="lead">{text.toCopyLead}</p>{/if}
				{#if plan.deleted + plan.brought_back > 0}
					{@const deletedAll = plan.deleted + plan.brought_back}
					<!-- The card still holds what was deleted from the library: it stays out
					     unless ticked back, one by its path or all. A ticked one keeps its
					     place among those left out. -->
					{@const all = bringBack.includes(BRING_ALL_BACK)}
					{@const named = [...new Set([...bringBack.filter((n) => n !== BRING_ALL_BACK), ...(plan.deleted_names ?? [])])].sort()}
					<h2>{text.deletedHead(deletedAll)}</h2>
					<div class="table">
						<label class="th"><input type="checkbox" checked={all} onchange={() => backAll(!all)} /><span class="grow">{text.bringAllBack}</span></label>
						{#each named as n (n)}
							<label class="row"><input type="checkbox" checked={bringBack.includes(n)} onchange={() => back(n)} /><span class="grow mono">{n}</span></label>
						{/each}
						{#if plan.deleted_more}<div class="row"><span class="grow muted">{text.othersMore(plan.deleted_more)}</span></div>{/if}
					</div>
				{/if}
				{#if plan.groups.length > 0}
					<!-- I4: one row per group, with why; a project is one row, not its folders. -->
					<div class="table">
						<div class="th"><span class="box"></span><span class="grow">{text.colFolder}</span><span class="kind">{text.colWhat}</span><span class="n">{text.colFiles}</span></div>
						{#each plan.groups as g (g.folder)}
							{@const picks = pickedIn(g.folder).length}
							<div class="row group" class:out={!g.in}>
								<input type="checkbox" checked={g.in} onchange={() => tick(g)} aria-label={g.folder} />
								<span class="grow what">
									<span class="mono">{g.folder === '.' ? name : g.folder}</span>
									<small>
										{g.kind === 'photos'
											? [range(g) || text.whyPhotos, g.in_library ? text.nInLibrary(g.in_library) : ''].filter(Boolean).join(' · ')
											: g.kind === 'project'
												? text.whyProject(g.mark ?? '')
												: g.kind === 'previews'
													? text.whyPreviews(g.mark ?? '')
													: text.whyUnsure}
									</small>
									{#if !g.in}<button class="link" onclick={() => (picking = g.folder)}>{text.choosePhotosInside(picks)}</button>{/if}
								</span>
								<span class="kind"><span class="tag {g.kind}">{text.groupKind[g.kind]}</span></span>
								<span class="n count">{!g.in && picks ? `${picks} / ` : ''}{(g.photos + g.videos).toLocaleString('en')}</span>
							</div>
						{/each}
					</div>
				{/if}
				{@render byYear(plan.years)}
				{#if plan.others.length > 0}
					<h2>{text.leftOut}</h2>
					<div class="table">
						<div class="th"><span class="grow">{text.leftOutLead}</span><span>{text.colFiles}</span></div>
						{#each plan.others as o (o.name)}
							<div class="row"><span class="grow mono">{o.name}</span><span class="count">{o.files.toLocaleString('en')} · {bytes(o.bytes)}</span></div>
						{/each}
						{#if plan.others_more}<div class="row"><span class="grow muted">{text.othersMore(plan.others_more)}</span></div>{/if}
					</div>
				{/if}
			</section>
			<aside class="panel">
				<h2>{text.whatHappens}</h2>
				<dl>
					<div><dt>{text.importToCopy}</dt><dd class="strong">{(plan.photos + plan.videos).toLocaleString('en')} · {bytes(plan.bytes)}</dd></div>
					<div><dt>{text.importInLibrary}</dt><dd class:ok={plan.in_library > 0}>{plan.in_library.toLocaleString('en')}</dd></div>
					{#if plan.other_files > 0}<div><dt>{text.importLeftOut}</dt><dd>{plan.other_files.toLocaleString('en')}</dd></div>{/if}
					{#if plan.unreadable > 0}<div class="warn"><dt>{text.importUnreadable}</dt><dd>{plan.unreadable.toLocaleString('en')}</dd></div>{/if}
					{#if free}<div class:warn={!fits}><dt>{text.importFree}</dt><dd>{bytes(free)}</dd></div>{/if}
				</dl>
				{#if im.canLink && plan.photos + plan.videos > 0}
					<!-- On the library's disk: a second name for each file instead of a copy. -->
					<fieldset class="how">
						<legend>{text.importHow}</legend>
						<label class:on={!link}
							><input type="radio" bind:group={link} value={false} /><b>{text.importHowCopy}</b><span>{text.importHowCopyDesc(bytes(plan.bytes))}</span></label
						>
						<label class:on={link}
							><input type="radio" bind:group={link} value={true} /><b>{text.importHowLink}</b><span>{text.importHowLinkDesc}</span></label
						>
					</fieldset>
				{/if}
				{#if !fits && !link}<p class="problem"><Icon name="exclamation-triangle" size={16} />{text.importNoRoom}</p>{/if}
				<div class="actions">
					<PrimaryButton
						disabled={(!fits && !link) || plan.outcome !== 'new' && plan.outcome !== 'redated'}
						onclick={() => start(() => startImport({ ...asking(), link: link && !!im!.canLink }))}>{link ? text.linkIn : text.copyAndVerify}</PrimaryButton
					>
					<SecondaryButton onclick={() => (choosing = true)}>{text.back}</SecondaryButton>
				</div>
				<p class="foot">
					{text.into} <b>{im.profile || profile}</b>{text.byTheirDate}
					<!-- A camera whose clock was reset: its photos given their day before the copy. -->
					{#if redates.length > 0}{text.datesGiven(redates.reduce((n, r) => n + r.names.length, 0))}
					{/if}
					<button class="link" onclick={() => (dating = true)}>{text.anyDatesWrong}</button>
				</p>
			</aside>
		</div>
	{:else if view === 'copying' && im}
		<!-- J2: each file copied and read back, one at a time; the years fill beside, as a download's. -->
		<div class="end">
			<div class="main">
				<span class="pill live"><span class="spin"><Icon name="arrow-repeat" size={14} /></span>{text.importBringing(name)}</span>
				<h1 class="hero">{isCard ? text.importCopying : text.importCopyingFolder}</h1>
				<div class="track"><b style:width="{share * 100}%"></b></div>
				<div class="numbers">
					<div><b>{im.n.toLocaleString('en')}</b><span>{text.ofN(im.of.toLocaleString('en'))}</span></div>
					{#if im.bytes > 0}<div><b>{bytes(im.moved)}</b><span>{text.ofN(bytes(im.bytes))}</span></div>{/if}
					{#if run}<div><b>{text.aboutTime(roughly(run.left))}</b><span>{text.leftAtSpeed}</span></div>{/if}
				</div>
				<div class="actions"><SecondaryButton onclick={() => stopImport().then((s) => (im = s))}>{text.importStop}</SecondaryButton></div>
				{#if latest.length > 0 && Object.keys(im.filed ?? {}).length > 0}<Arrived label={text.importArrived} photos={latest} />{/if}
			</div>
			{@render filedYears(false)}
		</div>
	{:else if view === 'taken-out' && im}
		<!-- I7: the card came out; put back, the copy carries on by itself. -->
		<span class="pill warn">{text.importWaitingFor(name)}</span>
		<h1>{text.importTakenOut(name)}</h1>
		<p class="lead">{im.kind === 'plan' ? text.importTakenOutReadingLead : text.importTakenOutLead}</p>
		{#if im.imported}
			<div class="numbers">
				<div><b>{im.imported.placed.toLocaleString('en')}</b><span>{text.importPlaced}</span></div>
			</div>
		{/if}
		<div class="actions">
			<!-- A card is found again by itself; a disk or a share, once back, is carried on from here. -->
			{#if !isCard}<PrimaryButton onclick={carryOn}>{text.carryOn}</PrimaryButton>{/if}
			<SecondaryButton onclick={() => stopImport().then((s) => (im = s))}
				>{im.kind === 'plan' ? text.importStop : text.importEndWithout}</SecondaryButton
			>
		</div>
		{#if im.kind !== 'plan'}<p class="note">{text.importEndWithoutDesc}</p>{/if}
	{:else if (view === 'done' || view === 'stopped') && im}
		<!-- J3: what came in, by kind and by year; what did not, each shown on asking. -->
		{@const done = im.imported}
		<div class="end">
			<div class="main">
				<span class="pill" class:ok={view === 'done'}>{#if view === 'done'}<Icon name="check-circle" size={14} />{/if}{name}</span>
				<!-- How it ended, as the engine says it. -->
				{#if view === 'stopped'}
					<h1 class="hero">{text.importStopped}</h1>
					<p class="lead">{text.importStoppedLead}</p>
				{:else if done && done.outcome !== 'new'}
					<h1 class="hero">{text.importNothingNew}</h1>
					<p class="lead">{(text.doneLead[done.outcome as keyof typeof text.doneLead] ?? text.planLead[done.outcome])(done)}</p>
				{:else}
					<h1 class="hero">{text.imported}</h1>
				{/if}
				{#if done && done.placed > 0}
					{@const photos = done.placed - done.videos}
					<div class="numbers">
						{#if photos > 0}<div><b>{photos.toLocaleString('en')}</b><span>{text.kindPhotos}</span></div>{/if}
						{#if done.raw > 0}<div><b>{done.raw.toLocaleString('en')}</b><span>{text.kindRawOf}</span></div>{/if}
						{#if done.videos > 0}<div><b>{done.videos.toLocaleString('en')}</b><span>{text.kindVideos}</span></div>{/if}
						<div><b>{bytes(done.bytes)}</b><span>{text.onTheDisk}</span></div>
						{#if done.unreadable > 0}<div class="warn"><b>{done.unreadable.toLocaleString('en')}</b><span>{text.importUnreadable}</span></div>{/if}
					</div>
				{/if}
				<div class="actions">
					<PrimaryButton onclick={() => goto('/library')}>{text.seeLibraryNow}</PrimaryButton>
					<SecondaryButton onclick={() => lookAt(null, true)}>{text.anyDatesWrong}</SecondaryButton>
				</div>
				{#if done && done.placed > 0 && latest.length > 0}<Arrived label={text.importArrived} photos={latest} />{/if}
				{#if done && ((done.left_out?.length ?? 0) > 0 || done.deleted > 0 || done.in_library > 0)}
					<div class="out">
						<span class="outhead">{text.notCopied}</span>
						<div>
							{#each done.left_out ?? [] as g (g.folder)}
								<div class="outrow">
									<b>{text.nPhotos(g.files)}</b><span>{text.outIn(g.folder === '.' ? name : g.folder, g.kind, g.mark)}</span>
									<button class="link" onclick={() => lookAt(g.folder)}>{text.show}</button>
								</div>
							{/each}
							{#if done.deleted > 0}
								<div class="outrow">
									<b>{text.nPhotos(done.deleted)}</b><span>{text.youDeleted}</span>
									<button class="link" onclick={() => lookAt(null)}>{text.show}</button>
								</div>
							{/if}
							{#if done.in_library > 0}
								<div class="outrow">
									<b>{text.nPhotos(done.in_library)}</b><span>{text.importInLibrary}</span>
									<button class="link" onclick={() => lookAt(null)}>{text.show}</button>
								</div>
							{/if}
						</div>
					</div>
				{/if}
			</div>
			{@render filedYears(true)}
		</div>
	{:else if im}
		<span class="pill err">{name}</span>
		<h1>{text.importFailed}</h1>
		<p class="lead">
			{im.problem === 'no-space'
				? text.importNoRoom
				: im.problem === 'no-link'
					? text.importNoLink
				: im.problem === 'busy'
					? text.importBusy
					: im.problem === 'library-gone'
						? text.libraryGone
						: im.error}
		</p>
		<div class="actions"><PrimaryButton onclick={() => (choosing = true)}>{text.lookAgain}</PrimaryButton></div>
	{/if}
	</div>{/key}{/if}
</div>

<style>
	/* The steps overlap in one cell while one gives way to the next. */
	.page {
		height: 100%;
		overflow-y: auto;
		display: grid;
		grid-template: minmax(100%, max-content) / 100%;
		padding: 64px 40px 40px;
	}
	.step {
		grid-area: 1 / 1;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 16px;
	}
	/* The page scrolls whole, its scrollbar at the window's edge; what is
	   read stays a column a line can be followed across. Picking dates
	   takes more: the photos and the panel beside them. */
	.step > :global(*) {
		max-width: 760px;
	}
	/* The end: the column beside the years, as at the end of a download (J3, A4). */
	.step > .end {
		max-width: none;
		width: 100%;
		flex: 1;
		min-height: 0;
		display: flex;
		align-items: flex-start;
		gap: 40px;
	}
	.main {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 16px;
	}
	.hero {
		font-size: var(--text-hero);
		letter-spacing: -2px;
		line-height: 1.05;
	}
	.out {
		width: 100%;
		display: flex;
		flex-direction: column;
		gap: 12px;
		margin-top: 8px;
	}
	.outhead {
		font-size: var(--text-callout);
		color: var(--muted);
	}
	.outrow {
		height: 44px;
		display: flex;
		align-items: center;
		gap: 12px;
		border-bottom: 1px solid var(--line);
	}
	.outrow b {
		font-weight: 500;
		color: var(--fg);
	}
	.outrow span {
		flex: 1;
		color: var(--muted);
	}
	.outrow .link {
		font-size: var(--text-callout);
	}
	.step > :global(.dates) {
		max-width: 1200px;
	}
	h1 {
		font-size: var(--text-display);
		font-weight: 700;
		color: var(--fg);
	}
	.lead {
		max-width: 560px;
		color: var(--muted);
	}
	.pill {
		height: 28px;
		padding: 0 12px;
		display: inline-flex;
		align-items: center;
		gap: 8px;
		border-radius: 999px;
		background: var(--accent-soft);
		color: var(--accent);
		font-size: var(--text-callout);
		font-weight: 600;
	}
	.pill.ok {
		background: var(--ok-soft);
		color: var(--ok);
	}
	.pill.warn {
		background: var(--warn-soft);
		color: var(--warn);
	}
	.pill.err {
		background: var(--err-soft);
		color: var(--err);
	}
	.spin {
		display: grid;
		animation: spin var(--motion-spin) linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
	.sources {
		width: 100%;
		margin-top: 8px;
		display: grid;
		grid-template-columns: auto 1fr auto;
		border-top: 1px solid var(--line);
	}
	/* The space between the columns inside the cells: a gap would break each row's line. */
	.sources .what,
	.sources .act {
		padding-left: 16px;
	}
	.sources > * {
		padding: 16px 0;
		display: flex;
		align-items: center;
		border-bottom: 1px solid var(--line);
	}
	.sources .tile :global(svg) {
		width: 44px;
		height: 44px;
		padding: 11px;
		box-sizing: border-box;
		border-radius: 10px;
		background: var(--accent-soft);
		color: var(--accent);
	}
	.sources .what {
		min-width: 0;
		flex-direction: column;
		align-items: stretch;
		justify-content: center;
		gap: 2px;
	}
	.sources .what b {
		font-size: var(--text-title-3);
		font-weight: 600;
		color: var(--fg);
	}
	.sources .what small {
		font-size: var(--text-callout);
		color: var(--muted);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.act :global(button) {
		width: 100%;
	}
	.numbers {
		display: flex;
		flex-wrap: wrap;
		gap: 16px 40px;
	}
	.numbers b {
		display: block;
		font-size: var(--text-title-1);
		font-weight: 700;
		color: var(--fg);
	}
	.numbers span {
		font-size: var(--text-callout);
		color: var(--muted);
	}
	.numbers .warn b {
		color: var(--warn);
	}
	/* A download's bar (A4). */
	.track {
		width: 100%;
		height: 8px;
		border-radius: 999px;
		background: var(--line);
		overflow: hidden;
	}
	.track b {
		display: block;
		height: 100%;
		border-radius: 999px;
		background: var(--accent);
		transition: width var(--motion-ambient) var(--ease-out);
	}
	.how {
		width: 100%;
		display: flex;
		flex-direction: column;
		gap: 8px;
		border: 0;
		padding: 0;
	}
	.how legend {
		margin-bottom: 8px;
		font-size: var(--text-callout);
		font-weight: 600;
		color: var(--muted);
	}
	.how label {
		display: grid;
		grid-template-columns: auto 1fr;
		column-gap: 12px;
		padding: 12px 16px;
		border: 1px solid var(--border);
		border-radius: 12px;
		cursor: pointer;
	}
	.how label.on {
		border-color: var(--accent);
	}
	.how input {
		grid-row: span 2;
		accent-color: var(--accent);
	}
	.how b {
		color: var(--fg);
	}
	.how span {
		color: var(--muted);
		font-size: var(--text-callout);
	}
	.actions {
		display: flex;
		gap: 12px;
	}
	.problem {
		display: flex;
		align-items: center;
		gap: 8px;
		color: var(--warn);
		font-weight: 600;
	}
	.note {
		color: var(--muted);
	}
	.link {
		color: var(--accent);
		font-weight: 600;
	}
	.tag {
		padding: 2px 8px;
		border-radius: 999px;
		font-size: var(--text-footnote);
		font-weight: 600;
		background: var(--ok-soft);
		color: var(--ok);
		white-space: nowrap;
	}
	.tag.unsure {
		background: var(--warn-soft);
		color: var(--warn);
	}
	.tag.project,
	.tag.previews {
		background: var(--soft);
		color: var(--muted);
	}
	/* I3: the folders, the years and what stays out beside what happens. */
	.step > .plan {
		max-width: 1200px;
		width: 100%;
	}
	.plan {
		display: flex;
		gap: 48px;
		align-items: flex-start;
	}
	.main {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 20px;
	}
	.main h2,
	.panel h2 {
		font-size: var(--text-title-3);
		font-weight: 600;
		color: var(--fg);
	}
	.panel h2 {
		font-size: var(--text-title-2);
		font-weight: 700;
	}
	.table {
		width: 100%;
		display: flex;
		flex-direction: column;
		border: 1px solid var(--line);
		border-radius: var(--radius-16);
		overflow: hidden;
	}
	.th,
	.row {
		display: flex;
		align-items: center;
		gap: 16px;
		padding: 0 20px;
		border-bottom: 1px solid var(--line);
	}
	.th {
		min-height: 40px;
		background: var(--soft);
		font-size: var(--text-subheadline);
		font-weight: 600;
		letter-spacing: 0.8px;
		text-transform: uppercase;
		color: var(--faint);
	}
	.row {
		min-height: 44px;
		color: var(--text);
	}
	.table > :last-child {
		border-bottom: 0;
	}
	.grow {
		flex: 1;
		min-width: 0;
	}
	.box {
		width: 16px;
	}
	.kind {
		width: 120px;
	}
	.n {
		width: 110px;
		text-align: right;
	}
	.count {
		color: var(--muted);
		font-variant-numeric: tabular-nums;
	}
	.muted {
		color: var(--muted);
	}
	.mono {
		font-family: var(--font-mono);
		font-size: var(--text-callout);
		overflow-wrap: anywhere;
	}
	.table input {
		width: 16px;
		height: 16px;
		margin: 0;
		accent-color: var(--accent);
	}
	button.row {
		width: 100%;
		text-align: left;
	}
	button.row:not(:disabled):hover {
		background: var(--soft);
	}
	.year .name {
		font-weight: 600;
		color: var(--fg);
	}
	.chev {
		width: 16px;
		display: flex;
		color: var(--muted);
	}
	.year.open .chev :global(svg) {
		transform: rotate(90deg);
	}
	.row.month {
		min-height: 36px;
		padding-left: 52px;
		font-size: var(--text-callout);
		color: var(--muted);
	}
	.group {
		align-items: flex-start;
		padding-top: 16px;
		padding-bottom: 16px;
	}
	.group input {
		margin-top: 2px;
	}
	.group .what {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 4px;
	}
	.group .what .mono {
		color: var(--fg);
	}
	.group .what small {
		color: var(--muted);
		font-size: var(--text-callout);
	}
	.group.out .what .mono,
	.group.out .count {
		color: var(--faint);
	}
	.panel {
		width: 380px;
		flex-shrink: 0;
		display: flex;
		flex-direction: column;
		gap: 20px;
		padding: 24px;
		border: 1px solid var(--line);
		border-radius: var(--radius-16);
		background: var(--surface);
		position: sticky;
		top: 0;
	}
	.panel dl {
		display: flex;
		flex-direction: column;
		gap: 10px;
		padding-bottom: 20px;
		border-bottom: 1px solid var(--line);
	}
	.panel dl div {
		display: flex;
		justify-content: space-between;
		gap: 16px;
	}
	/* The engine's own words, a sentence's start here. */
	.panel dt::first-letter {
		text-transform: uppercase;
	}
	.panel dd {
		color: var(--muted);
		font-variant-numeric: tabular-nums;
	}
	.panel dd.strong {
		font-weight: 600;
		color: var(--fg);
	}
	.panel dd.ok {
		font-weight: 600;
		color: var(--ok);
	}
	.panel .warn dd,
	.panel .warn dt {
		color: var(--warn);
	}
	.panel .actions {
		display: flex;
		gap: 8px;
	}
	.panel .actions > :global(:first-child) {
		flex: 1;
	}
	.foot {
		font-size: var(--text-callout);
		line-height: 1.5;
		color: var(--faint);
	}
	.foot b {
		color: var(--fg);
	}
	:global(.narrow) .plan {
		flex-direction: column;
	}
	:global(.narrow) .panel {
		width: 100%;
		position: static;
	}
</style>
