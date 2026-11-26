// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// The library's search, held by the page under the title bar and typed in it.
import { tick } from 'svelte';
import { isFiltering, noFilter, type Filter } from './filter.js';

class Finder {
	/** The page shown has photos to search. */
	offered = $state(false);
	open = $state(false);
	// Raw: every change is another filter, and the grid starts again at its top.
	filter = $state.raw<Filter>({ ...noFilter });
	/** The title bar's Search, and the field in its place. */
	button = $state<HTMLElement>();
	input = $state<HTMLInputElement>();

	show() {
		this.open = true;
		tick().then(() => {
			this.input?.focus();
			this.input?.select();
		});
	}
	/** Closed, it keeps nothing: what is not seen does not filter. */
	close() {
		this.open = false;
		if (isFiltering(this.filter)) this.filter = { ...noFilter };
		tick().then(() => this.button?.focus());
	}
}
export const finder = new Finder();
