// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// Svelte's transitions, timed by the design system's motion tokens
// (tokens.json, "motion"): the CSS and the JS move alike. Pages change at
// once; these are for a block coming or going, a dialog, a cross-fade.
import { cubicOut } from 'svelte/easing';
import { fade, scale, slide, type TransitionConfig } from 'svelte/transition';

const token = (name: string) =>
	typeof document === 'undefined' ? '' : getComputedStyle(document.documentElement).getPropertyValue(`--${name}`).trim();

// Nothing moves under reduced motion, as the CSS says in app.css.
const still = () => typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches;

/** A duration token in ms, 0 under reduced motion. The build writes 200ms as .2s. */
const ms = (name: 'motion-fast' | 'motion-base') => {
	const d = token(name);
	return still() ? 0 : (parseFloat(d) || 0) * (d.endsWith('ms') ? 1 : 1000);
};

/** A cubic-bezier token as an easing function: the curve the CSS uses. */
function curve(name: 'ease-out' | 'ease-in'): (t: number) => number {
	const [x1, y1, x2, y2] = (token(name).match(/-?[\d.]+/g) ?? []).map(Number);
	if ([x1, y1, x2, y2].some((n) => n === undefined || isNaN(n))) return cubicOut;
	const at = (a: number, b: number, s: number) => 3 * a * s * (1 - s) ** 2 + 3 * b * s * s * (1 - s) + s ** 3;
	return (t) => {
		// The s whose x is t, by bisection: monotonic in x, a dozen steps are enough.
		let lo = 0, hi = 1, s = t;
		for (let i = 0; i < 12; i++) {
			s = (lo + hi) / 2;
			if (at(x1, x2, s) < t) lo = s;
			else hi = s;
		}
		return at(y1, y2, s);
	};
}

// Coming: the base duration, settling. Leaving: shorter, accelerating.
const timing = (leaving: boolean) =>
	leaving ? { duration: ms('motion-fast'), easing: curve('ease-in') } : { duration: ms('motion-base'), easing: curve('ease-out') };

function opening(node: Element, leaving: boolean, axis: 'x' | 'y'): TransitionConfig {
	const opts = timing(leaving);
	const open = slide(node, { ...opts, axis });
	return { ...opts, css: (t, u) => `${open.css?.(t, u) ?? ''}; opacity: ${t}` };
}

/** A block coming or going: it opens in height as it fades, and what is
 *  below moves with it. Leaving is shorter. Use as in:reveal and
 *  out:reveal={{ leaving: true }}. */
export const reveal = (node: Element, { leaving = false } = {}) => opening(node, leaving, 'y');

/** A panel at the side: it opens in width as it fades, and what is beside
 *  it gives way. */
export const pane = (node: Element, { leaving = false } = {}) => opening(node, leaving, 'x');

/** One content giving way to another in the same place. */
export const swap = (node: Element, { leaving = false } = {}) => fade(node, timing(leaving));

/** A dialog's panel: fades and grows to its size from a little smaller. */
export const pop = (node: Element, { leaving = false } = {}) =>
	scale(node, { start: 0.98, ...timing(leaving) });

/** A list's rows moving to their new places (animate:flip). */
export const moving = () => timing(false);
