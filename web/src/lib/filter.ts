// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// The library's search, over the photos of the page: the days from and to,
// and the badges.
import type { GridPhoto } from './api.js';
import type { IconName } from './icons.js';
import { text } from './strings.js';

/** start and end are days, "2025-04-07", both in. */
export type Filter = { start: string; end: string; badges: string[] };
export const noFilter: Filter = { start: '', end: '', badges: [] };
export const isFiltering = (f: Filter) => !!f.start || !!f.end || f.badges.length > 0;

export type Badge = { id: string; label: string; icon?: IconName; count: number };

// The apps with a badge of their own, and its logo.
const apps = { whatsapp: 'whatsapp', telegram: 'telegram', signal: 'signal', instagram: 'instagram' } as const;
export const isApp = (origin?: string): origin is keyof typeof apps => !!origin && origin in apps;

/** A photo's own badge, by its origin: none for a camera's. */
export function badgeOf(origin: GridPhoto['origin']): { id: string; label: string; icon?: IconName } | null {
	if (isApp(origin)) return { id: origin, label: text.badgeNames[origin], icon: apps[origin] };
	return origin === 'screenshot' ? { id: origin, label: text.badgeNames.screenshot, icon: 'phone' } : null;
}

const TOP = 3;

/** The badges of a page, those that have photos: the three apps with the
 *  most, Screenshots, Other (every other app), Estimated. */
export function badgesOf(photos: GridPhoto[]): Badge[] {
	const n: Record<string, number> = {};
	let estimated = 0;
	for (const p of photos) {
		if (p.origin) n[p.origin] = (n[p.origin] ?? 0) + 1;
		if (p.guessed) estimated++;
	}
	const ranked = Object.keys(n).filter(isApp).sort((a, b) => n[b] - n[a]);
	const other = [...ranked.slice(TOP), 'app'].reduce((sum, id) => sum + (n[id] ?? 0), 0);
	const own = [...ranked.slice(0, TOP), 'screenshot'] as GridPhoto['origin'][];
	return [
		...own.map((id) => ({ ...badgeOf(id)!, count: n[id!] ?? 0 })),
		{ id: 'other', label: text.badgeNames.other, count: other },
		{ id: 'estimated', label: text.estimatedTag, count: estimated }
	].filter((b) => b.count > 0);
}

/** The photos a search keeps. Two apps are either; Estimated and the dates
 *  narrow what they give. */
export function search(photos: GridPhoto[], f: Filter, badges: Badge[]): GridPhoto[] {
	if (!isFiltering(f)) return photos;
	const origins = f.badges.filter((b) => b !== 'estimated');
	const named = new Set(badges.map((b) => b.id));
	const badge = (p: GridPhoto) => (p.origin && !named.has(p.origin) ? 'other' : p.origin);
	return photos.filter((p) => {
		const day = p.taken?.slice(0, 10) ?? '';
		return (
			(!f.start || (day !== '' && day >= f.start)) &&
			(!f.end || (day !== '' && day <= f.end)) &&
			(origins.length === 0 || origins.includes(badge(p) ?? '')) &&
			(!f.badges.includes('estimated') || !!p.guessed)
		);
	});
}
