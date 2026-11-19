<!-- homewend — Copyright (C) 2026 Ron Alder -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- What the library itself has to say, in the strip under the titlebar:
     its disk not answering (G4), or photos of this page whose file is gone
     (G1), with the way to see them all. -->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { CircleAlert } from '@lucide/svelte';
	import { app, missingTotal } from '#lib/app.svelte.js';
	import type { Place } from '#lib/library.js';
	import { urlOf } from '#lib/places.js';
	import { text } from '#lib/strings.js';
	import Strip from './Strip.svelte';

	let { place }: { place: Place } = $props();

	const missing = $derived.by(() => {
		const o = app.overview;
		if (!o) return 0;
		if (place.kind === 'all') return missingTotal(o);
		if (place.kind === 'year') return o.missing[place.label === text.noDate ? '' : place.label] ?? 0;
		return 0;
	});
</script>

{#if app.overview?.gone}
	<Strip tone="err" title={text.libraryNotAnswering} detail={text.libraryNotAnsweringDesc(app.overview.gone)}>
		{#snippet icon()}<CircleAlert size={18} />{/snippet}
	</Strip>
{:else if missing > 0}
	<Strip
		tone="warn"
		title={text.missingBanner(missing, place.kind === 'year' ? place.label : undefined)}
		detail={text.missingDetail}
		action={text.showThem}
		onaction={() => goto(urlOf({ kind: 'missing' }))}
	>
		{#snippet icon()}<CircleAlert size={18} />{/snippet}
	</Strip>
{/if}
