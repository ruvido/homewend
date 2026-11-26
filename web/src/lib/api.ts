// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// The engine, as the page reaches it: one function per call of internal/web.

import type { IconName } from './icons.js';
import type { View } from './library.js';

export type Theme = 'system' | 'light' | 'dark';

export interface Settings {
	theme?: Theme;
	/** The profiles whose photos the window leaves out. */
	hidden?: string[];
	/** The profile of each account, by id, once it has been asked. */
	profiles?: Record<string, string>;
}

export interface AccountInfo {
	/** "google/<email>" */
	id: string;
	service: string;
	email: string;
	/** Whose photos they are: the folder of the library they go in. */
	profile: string;
}

export interface Overview {
	accounts: AccountInfo[];
	total: number;
	years: { year: string; count: number }[];
	albums: { account: string; name: string; count: number }[];
	takeouts: number;
	/** The photos of the people shown in the Archive, in the Trash, with a heart, and the videos. */
	archived: number;
	trashed: number;
	favorites: number;
	videos: number;
	/** Those there are, for an import to choose among; the one it goes to when none is named, "" when asked. */
	profiles: string[];
	profile: string;
	/** By year ("" for no date), the photos whose file is gone. */
	missing: Record<string, number>;
	/** The library folder, when it is not there: nothing is counted. */
	gone?: string;
}

/** A call the engine refused; status 401 means Google signed the account out. */
export class ApiError extends Error {
	constructor(
		readonly status: number,
		message: string
	) {
		super(message);
	}
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
	const res = await fetch(path, {
		method,
		headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (!res.ok) throw new ApiError(res.status, `${method} ${path}: ${res.status} ${await res.text()}`);
	return res.json();
}

export const getSettings = () => call<Settings>('GET', '/api/settings');
export const putSettings = (s: Settings) => call<Settings>('PUT', '/api/settings', s);
export const putProfile = (account: string, profile: string) =>
	call<{ profile: string }>('PUT', `/api/profile?account=${encodeURIComponent(account)}`, { profile });
/** Renames a profile: its folder in the library, and all that names it. */
export const renameProfile = (from: string, to: string) => call<unknown>('POST', '/api/profile/rename', { from, to });
export const getOverview = () => call<Overview>('GET', '/api/overview');
/** Moves each time the library's catalog changes, whoever changed it; with it, the thumbnails being made, the phones backing up and how many raw files were developed. */
export const getChanges = () => call<{ changes: string; thumbs: Thumbs; emptying: Emptying; backups: Backing[]; developed: number }>('GET', '/api/changes');
/** A phone backing up: done of the photos it said it would send; gone when they are in, or it went quiet. */
export type Backing = { id: string; name: string; done: number; of: number };
/** How many of the photos an Empty Trash was asked for it has done; of is 0 while none runs. */
export type Emptying = { done: number; of: number };
/** How many photos of the library have their thumbnail: made in the background. */
export type Thumbs = { done: number; of: number };
/** Opens the system browser on Google's sign-in and waits for it. */
export const login = (account?: string) =>
	call<AccountInfo>('POST', '/api/login' + (account ? `?account=${encodeURIComponent(account)}` : ''));

/** A photo of one library: its content, and whose library it is in. The same
 *  content in two libraries is two photos. */
export interface PhotoKey {
	hash: string;
	profile: string;
}
const keyOf = ({ hash, profile }: PhotoKey): PhotoKey => ({ hash, profile });
// Each profile's end of an address, put together once: a tile's is asked at
// every row that scrolls in.
const ends = new Map<string, string>();
function of(p: PhotoKey) {
	let end = ends.get(p.profile);
	if (end === undefined) ends.set(p.profile, (end = '?profile=' + encodeURIComponent(p.profile)));
	return p.hash + end;
}

/** Its picture as it is shown, small or at the size of a screen; and its own file. */
export const pictureOf = (p: PhotoKey & Shown, what: 'thumb' | 'preview' = 'thumb') => `/api/${what}/${of(p)}` + (p.edit ? `&edit=${p.edit}` : '');
export const originalOf = (p: PhotoKey) => `/api/original/${of(p)}`;

/** How a photo is shown. */
export interface Shown {
	/** Quarter turns anticlockwise the person gave it, of one with no edit. */
	turn?: number;
	/** Names its edit: another, and its pictures are others. */
	edit?: string;
	/** Its edit was made by a newer Homewend: shown as it came, and not turned. */
	newer?: boolean;
}

export interface GridPhoto extends PhotoKey, Shown {
	/** Its library and its content in one: what a list and a selection know it by. */
	id: string;
	/** Whose library's, by its place in the engine's profiles: none is the first. */
	of?: number;
	/** The engine's: the other libraries' copies of it in the list. */
	also?: GridPhoto[];
	/** A tile of a photo two or more libraries have (W2): each one's copy in
	 *  the list, in the order of the people, the tile showing the first. */
	copies?: GridPhoto[];
	name: string;
	taken?: string;
	/** How much of taken is a guess: Google's folder gave the year, the file's name the day or the time. */
	guessed?: 'year' | 'day' | 'time';
	video?: boolean;
	/** Its file is gone from the library, deleted or moved by hand. */
	missing?: boolean;
	/** In the Archive, shown in an album. */
	archived?: boolean;
	/** Given a heart. */
	favorite?: boolean;
	/** The app it came through (app: one with no badge of its own), or a screenshot. */
	origin?: 'whatsapp' | 'telegram' | 'signal' | 'instagram' | 'app' | 'screenshot';
}

// Whose each photo is and its id, once as the list comes. The id names the
// person, not their place among the others: the next list may have more, and
// what is picked or open must not become someone else's.
// A photo of two libraries comes as one tile (W2), the others' copies on it.
export const asPhotos = ({ profiles, photos }: { profiles: string[]; photos: GridPhoto[] }) => {
	const named = (p: GridPhoto) => {
		p.profile = profiles[p.of ?? 0];
		p.id = `${p.profile}/${p.hash}`;
	};
	for (const p of photos) {
		named(p);
		if (!p.also) continue;
		p.also.forEach(named);
		p.copies = [{ ...p }, ...p.also];
	}
	return photos;
};

/** The copies a tile stands for: its own, or each person's. */
export const copiesOf = (tiles: GridPhoto[]) => tiles.flatMap((t) => t.copies ?? [t]);
/** How many photos copies are: a photo of two people is one. */
export const photosIn = (copies: PhotoKey[]) => new Set(copies.map((p) => p.hash)).size;
/** The tile is this copy, or holds it. */
export const holds = (t: GridPhoto | undefined, id: string) => !!t && (t.id === id || !!t.copies?.some((c) => c.id === id));
/** The tiles with their copies changed by change, which returns a copy
 *  untouched as it is; a tile shows its first copy. */
export const patched = (tiles: GridPhoto[], change: (p: GridPhoto) => GridPhoto) =>
	tiles.map((t) => {
		if (!t.copies) return change(t);
		const copies = t.copies.map(change);
		return copies.every((c, i) => c === t.copies![i]) ? t : { ...copies[0], copies };
	});

export function getPhotos(q: { year?: string; album?: string; /** The album's, "" when not known. */ account?: string; noDate?: boolean; view?: View; /** The first of them; all when not given. */ limit?: number }) {
	const p = new URLSearchParams();
	if (q.limit) p.set('limit', String(q.limit));
	if (q.year) p.set('year', q.year);
	if (q.album) p.set('album', q.album);
	if (q.account) p.set('account', q.account);
	if (q.noDate) p.set('nodate', '1');
	if (q.view) p.set('view', q.view);
	return call<Parameters<typeof asPhotos>[0]>('GET', `/api/photos?${p}`).then(asPhotos);
}

/** What a photo's owner sets on it, from the selection's bar or the viewer:
 *  archived or in the Trash (on), or back. ask: Delete asks first (Q2). */
export type MarkState = 'archived' | 'trashed' | 'favorite';
export type MarkAct = { icon: IconName; label: string; state: MarkState; on: boolean; ask?: boolean };

/** Photos archived, put in the Trash or given a heart (on), or brought back. */
export const mark = (photos: PhotoKey[], state: MarkState, on: boolean) =>
	call<{ marked: number }>('POST', '/api/mark', { photos: photos.map(keyOf), state, on });

/** Photos shown in the Trash deleted for good; those that could not be are left in it. */
export const emptyTrash = (photos: PhotoKey[]) => call<{ left: PhotoKey[] }>('POST', '/api/trash/empty', { photos: photos.map(keyOf) });

export interface JobState {
	running: boolean;
	year: number;
	stage?: string;
	export?: string;
	parts: number;
	of: number;
	done: number;
	total: number;
	years: { year: string; arrived: number; of: number }[];
	retry?: string;
	/** When the stage began, as the engine saw it. */
	stageSince?: string;
	finished?: boolean;
	lastLooked?: string;
	unassigned: number;
	error?: string;
	problem?: 'signed-out' | 'no-space' | 'expired' | 'not-offered' | 'password-closed';
	/** With not-offered: the years Google has photos of. */
	offered?: number[];
	need?: number;
	free?: number;
}

const q = (o: Record<string, string | number | undefined>) =>
	new URLSearchParams(
		Object.entries(o).filter(([, v]) => v !== undefined && v !== '') as [string, string][]
	).toString();

/** How many photos and videos an account has on Google, by year, newest first. */
export interface YearCount {
	year: number;
	items: number;
	videos: number;
}
export interface YearCounts {
	counted?: string;
	years?: YearCount[];
	/** Old: counted again in the background, this one shown meanwhile. */
	stale?: boolean;
	counting: boolean;
	error?: string;
}
export const getYears = (account: string) => call<YearCounts>('GET', `/api/years?${q({ account })}`);
export const countYears = (account: string) => call<Record<string, never>>('POST', `/api/years?${q({ account })}`);
/** A phone paired with this computer (docs/phone.md). */
export type Phone = { id: string; name: string; profile: string; paired: string; seen?: string; backup?: string; photos: number };
/** A phone no longer paired: what it brought, and where it was unpaired. */
export type Unpaired = { name: string; profile: string; paired: string; unpaired: string; photos: number; by: 'phone' | 'computer' };
export const getPhones = () => call<{ phones: Phone[]; unpaired: Unpaired[]; pairing: boolean; unreachable?: boolean }>('GET', '/api/phones');
export const startPairing = (profile: string) => call<{ qr: string }>('POST', '/api/phones/pair', { profile });
export const stopPairing = () => call<null>('DELETE', '/api/phones/pair');
export const unpair = (id: string) => call<null>('DELETE', `/api/phones/${encodeURIComponent(id)}`);
export const getLibrary = () => call<{ dir: string; free: number }>('GET', '/api/library');
/** Takes out of its library the photo whose file is gone, or with none every one; keepOut keeps them out of the imports. */
export const takeOutMissing = (photo: PhotoKey | undefined, keepOut: boolean) =>
	call<{ taken: number }>('POST', '/api/missing/out', { photo: photo && keyOf(photo), keep_out: keepOut });
export const putLibrary = (dir: string) => call<{ dir: string }>('PUT', '/api/library', { dir });
export const startGet = (o: { account: string; year?: number; export?: string; fresh?: boolean; again?: boolean }) =>
	call<JobState>(
		'POST',
		`/api/get?${q({ account: o.account, year: o.year, export: o.export, new: o.fresh ? '1' : undefined, again: o.again ? '1' : undefined })}`
	);
export const getJob = (account: string) => call<JobState>('GET', `/api/job?${q({ account })}`);
export const stopJob = (account: string) => call<JobState>('POST', `/api/job/stop?${q({ account })}`);

export interface Takeout {
	id: string;
	status: 'preparing' | 'ready' | 'expired' | '';
	year: number;
	known: boolean;
	Created: string;
	Bytes: number;
	Parts: unknown[] | null;
	Expires: string;
	years?: string[];
	/** What of it the library holds; absent when it never came here. */
	local?: { profile: string; parts: number; of: number; bytes?: number; declared?: number; present?: number };
}

/** local: only what this computer knows, Google not asked; then no status is known. */
export const getTakeouts = (account: string, local = false) =>
	call<Takeout[]>('GET', `/api/takeouts?${q({ account, local: local ? '1' : undefined })}`);

export const logout = (account: string) => call<object>('POST', `/api/logout?${q({ account })}`);
export const getVersion = () => call<{ version: string }>('GET', '/api/about');

export const pathOf = (p: PhotoKey) => call<{ path: string }>('GET', `/api/path/${of(p)}`);

export const folderOf = (o: { account?: string; profile?: string; year?: string; album?: string; noDate?: boolean }) =>
	call<{ path: string }>(
		'GET',
		`/api/folder?${q({ account: o.account, profile: o.profile, year: o.year, album: o.album, nodate: o.noDate ? '1' : undefined })}`
	);

export const getLatest = () => call<Parameters<typeof asPhotos>[0]>('GET', '/api/latest').then(asPhotos);
export const giveDates = (photos: PhotoKey[], when: string) => call<{ moved: number }>('POST', '/api/dates', { photos: photos.map(keyOf), when });
/** A quarter turn anticlockwise, or with right clockwise. */
export const turn = (p: PhotoKey, right: boolean) => call<Shown>('POST', `/api/turn/${of(p)}${right ? '?right' : ''}`);
/** A square picture turned as the person turned the photo. */
export const turned = (p: { turn?: number }) => (p.turn ? `rotate(${-90 * p.turn}deg)` : undefined);
export interface PhotoAbout {
	albums: string[];
	accounts: string[];
	local: string[];
	/** Where its date comes from. */
	dated: 'sidecar' | 'exif' | 'video' | 'given' | 'folder' | 'name' | 'nameday' | 'none';
	bytes: number;
	/** In the library. */
	path: string;
}
export const getAbout = (p: PhotoKey) =>
	call<PhotoAbout>('GET', `/api/about/${of(p)}`);

/** What a photo's file says of itself: its pixels, 0 when not known; its camera, '' when it does not say. */
export interface PhotoFile {
	width: number;
	height: number;
	camera: string;
}
export const getFile = (p: PhotoKey) => call<PhotoFile>('GET', `/api/file/${of(p)}`);

export const readContents = (account: string, exportId: string) =>
	call<{ years: string[] }>('POST', `/api/takeouts/contents?${q({ account, export: exportId })}`);

/** A disk with a camera's photos on it: a card, a camera, a phone. */
export interface Card {
	path: string;
	name: string;
}

/** Photos whose camera had the date wrong, and the date they were taken:
 *  the first of them by the camera's clock takes it, the others keep their
 *  distance from it. */
export interface Redate {
	/** Paths below the folder. */
	names: string[];
	/** A clock, written as UTC: "2025-09-14T10:21:00Z" is 10:21 where it was taken. */
	when: string;
}

/** A photo or video of the folder, as its camera dated it. */
export interface ImportFile {
	path: string;
	/** The camera's clock, as UTC; none, no date. */
	taken?: string;
	video?: boolean;
	/** Its group's folder. */
	group: string;
	/** An image of a few hundred pixels at most: an icon, a logo. */
	small?: boolean;
}

/** A group of the folder's photos: its own folders, a project, an app's previews. */
export interface ImportGroup {
	folder: string;
	kind: 'photos' | 'unsure' | 'project' | 'previews';
	/** What made it so: "package.json", "Lightroom". */
	mark?: string;
	/** Comes in whole. */
	in: boolean;
	photos: number;
	videos: number;
	in_library: number;
	first?: string;
	last?: string;
}

/** Which groups come in: left out, brought in against their kind, photos picked one by one. */
export interface Choice {
	out: string[];
	in: string[];
	pick: string[];
}

/** What a look and its copy both count, and how it ends (the engine's library.Counts). */
export interface Counts {
	in_library: number;
	twins: number;
	/** Moved to a date given. */
	redated: number;
	/** Deleted from the library: left out, and brought back; those left out by path, and how many more. */
	deleted: number;
	brought_back: number;
	deleted_names?: string[];
	deleted_more?: number;
	unreadable: number;
	/** The groups that stay out, by their kind or as chosen. */
	left_out?: (Omit<ImportGroup, 'in' | 'photos' | 'videos' | 'in_library'> & { files: number })[];
	outcome: Outcome;
}

export interface ImportPlan extends Counts {
	photos: number;
	videos: number;
	bytes: number;
	files: ImportFile[];
	groups: ImportGroup[];
	/** Newest first; "" is the photos with no date. */
	years: { year: string; photos: number; videos: number; months?: { month: number; photos: number; videos: number }[] }[];
	/** What stays out, by its name at the folder's top: "FWUP0013.DAT", "FFDB/". */
	others: { name: string; files: number; bytes: number }[];
	/** Those left unnamed past the engine's limit; the files of all of them. */
	others_more?: number;
	other_files: number;
}

export interface Imported extends Counts {
	placed: number;
	bytes: number;
	undated: number;
	/** Of those placed, a camera's raw files and the videos: the rest are photos. */
	raw: number;
	videos: number;
}

/** In the library and checked, by year and month; year "" month 0, the undated. */
export type Filed = Record<string, Record<number, number>>;

/** How a look or a copy ends, decided by the engine. */
export type Outcome = 'new' | 'redated' | 'left-out' | 'deleted' | 'known-noted' | 'known' | 'unread' | 'empty';

/** What the page asks of a look or a copy; kept in the state for a copy taken up again. */
export interface ImportAsk {
	dir: string;
	/** Whose photos they are. */
	profile: string;
	redates?: Redate[];
	choice?: Choice | null;
	/** Hard links, not copies. */
	link?: boolean;
	/** Photos deleted from the library to bring in again, by name, or "all". */
	bring_back?: string[];
}

/** Among the names to bring back, every photo deleted from the library. */
export const BRING_ALL_BACK = 'all';

/** Where the window's import stands: the look at a folder, then the copy. */
export interface ImportState extends Partial<ImportAsk> {
	running: boolean;
	/** Whether dir was a card when the run began. */
	card?: boolean;
	/** How the folder is named on every screen. */
	top?: string;
	kind?: 'plan' | 'import';
	/** The file being read or copied, n of of; done of total its bytes. */
	stage?: 'read' | 'copy';
	n: number;
	of: number;
	name?: string;
	done: number;
	total: number;
	filed?: Filed;
	/** Of them, the videos, by year. */
	filedVideos?: Record<string, number>;
	/** The folder's bytes gone through, of all of them: the copy's pace. */
	moved: number;
	bytes: number;
	plan?: ImportPlan;
	/** The folder is on the library's disk: it can come in as hard links. */
	canLink?: boolean;
	imported?: Imported;
	stopped?: boolean;
	error?: string;
	problem?: 'no-link' | 'taken-out' | 'no-space' | 'busy' | 'library-gone' | 'unknown-name' | 'ambiguous-name';
	need?: number;
	free?: number;
}

export const getCards = () => call<Card[]>('GET', '/api/cards');
export const ejectCard = (path: string) => call<null>('POST', '/api/cards/eject', { path });
/** Runs what ran last once more, as it was asked: a card come back. */
export const againImport = () => call<ImportState>('POST', '/api/import/again');
export const planImport = (ask: ImportAsk) => call<ImportState>('POST', '/api/import/plan', ask);
export const startImport = (ask: ImportAsk) => call<ImportState>('POST', '/api/import', ask);
export const importThumbUrl = (path: string) => `/api/import/thumb?${q({ file: path })}`;
export const getImport = () => call<ImportState>('GET', '/api/import');
export const stopImport = () => call<ImportState>('POST', '/api/import/stop');
