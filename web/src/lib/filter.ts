// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// The library's search, over the photos of the page: the words in their
// file names, the days from and to, and the badges.
import type { GridPhoto } from './api.js';
import type { IconName } from './icons.js';
import { text } from './strings.js';

/** start and end are days, "2025-04-07", both in. */
export type Filter = { name: string; start: string; end: string; badges: string[] };
export const noFilter: Filter = { name: '', start: '', end: '', badges: [] };
export const isFiltering = (f: Filter) => wordsOf(f.name).length > 0 || !!f.start || !!f.end || f.badges.length > 0;

// As compared: capitals and accents are no difference. Most names have no accent to look for.
function fold(s: string): string {
	const low = s.toLowerCase();
	return /[^\x00-\x7f]/.test(low) ? low.normalize('NFD').replace(/\p{M}/gu, '') : low;
}
/** The words typed, without the dots around them: a name has them all, in any order. */
const wordsOf = (name: string) => fold(name).split(/\s+/).map((w) => w.replace(/^\.+|\.+$/g, '')).filter(Boolean);

export type Badge = { id: string; label: string; icon?: IconName; count: number };

// The apps with a badge of their own, each with its logo under the same name.
const apps = ['whatsapp', 'telegram', 'signal', 'instagram'] as const;
type App = (typeof apps)[number];
const isApp = (origin?: string): origin is App => apps.includes(origin as App);

/** A photo's own badge, by its origin: none for a camera's. */
export function badgeOf(origin: GridPhoto['origin']): Omit<Badge, 'count'> | null {
	if (isApp(origin)) return { id: origin, label: text.badgeNames[origin], icon: origin };
	return origin === 'screenshot' ? { id: origin, label: text.badgeNames.screenshot, icon: 'phone' } : null;
}

const TOP = 3;

/** How many photos of a page each badge could have. */
export function countBadges(photos: GridPhoto[]): Record<string, number> {
	const n: Record<string, number> = {};
	for (const p of photos) {
		if (p.origin) n[p.origin] = (n[p.origin] ?? 0) + 1;
		if (p.guessed) n.estimated = (n.estimated ?? 0) + 1;
	}
	return n;
}

/** The badges of a page, those that have photos: the three apps with the
 *  most, Screenshots, Other (every other app), Estimated. One picked stays
 *  while it is, whatever happens to its photos. */
export function badgesOf(n: Record<string, number>, picked: string[]): Badge[] {
	const ranked = [...apps].sort((a, b) => (n[b] ?? 0) - (n[a] ?? 0));
	const own = ranked.filter((id, i) => i < TOP || picked.includes(id));
	const other = ['app', ...ranked.filter((id) => !own.includes(id))].reduce((sum, id) => sum + (n[id] ?? 0), 0);
	return [
		...own.map((id) => ({ ...badgeOf(id)!, count: n[id] ?? 0 })),
		{ ...badgeOf('screenshot')!, label: text.badgeNames.screenshots, count: n.screenshot ?? 0 },
		{ id: 'other', label: text.badgeNames.other, count: other },
		{ id: 'estimated', label: text.estimatedTag, count: n.estimated ?? 0 }
	].filter((b) => b.count > 0 || picked.includes(b.id));
}

/** The photos a search keeps, those with every word in their names. Two apps are either; Estimated and the dates
 *  narrow what they give. */
export function search(photos: GridPhoto[], f: Filter, badges: Badge[]): GridPhoto[] {
	if (!isFiltering(f)) return photos;
	const words = wordsOf(f.name);
	const origins = f.badges.filter((b) => b !== 'estimated');
	const named = new Set(badges.map((b) => b.id));
	const badge = (p: GridPhoto) => (p.origin && !named.has(p.origin) ? 'other' : p.origin);
	return photos.filter((p) => {
		const name = words.length > 0 ? fold(p.name) : '';
		const day = p.taken?.slice(0, 10) ?? '';
		return (
			(!f.start || (day !== '' && day >= f.start)) &&
			(!f.end || (day !== '' && day <= f.end)) &&
			(origins.length === 0 || origins.includes(badge(p) ?? '')) &&
			(!f.badges.includes('estimated') || !!p.guessed) &&
			words.every((w) => name.includes(w))
		);
	});
}
