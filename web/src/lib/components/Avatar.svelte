<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
	import { colourOf, initials, type Account } from '#lib/library.js';

	let { account, index = 0, size = 24 }: { account: Account; index?: number; size?: number } =
		$props();
</script>

<span
	class="avatar"
	style:width="{size}px"
	style:height="{size}px"
	style:background={colourOf(index)}
	title={account.email}>{initials(account.name)}</span
>
