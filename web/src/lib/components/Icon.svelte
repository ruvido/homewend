<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- A Bootstrap icon by its name, in the text's colour. -->
<script lang="ts">
	import { icons, type IconName } from '#lib/icons.js';

	let { name, size = 16 }: { name: IconName; size?: number } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">{@html icons[name]}</svg>
