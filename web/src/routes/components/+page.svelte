<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- The window with made-up data, to check the components against the design. -->
<script lang="ts">
	import Frame from '#lib/components/Frame.svelte';
	import Settings from '#lib/screens/Settings.svelte';
	import { twoPeople, tenPeople } from '#lib/fixtures.js';
	import type { Place, Sidebar } from '#lib/library.js';

	let data = $state<Sidebar>(structuredClone(location.hash === '#ten' ? tenPeople : twoPeople));
	let place = $state<Place>({ kind: 'settings' });

	function toggle(id: string) {
		const p = data.people.find((x) => x.id === id);
		if (p) p.shown = !p.shown;
	}
</script>

<Frame sidebar={data} {place} onplace={(p) => (place = p)} ontoggle={toggle}>
	<Settings />
</Frame>
