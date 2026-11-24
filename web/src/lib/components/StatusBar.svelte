<!-- homewend — Copyright (C) 2026 ruvido -->
<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- The download, in one line under every screen of the library. -->
<script lang="ts">
	import Icon from '#lib/components/Icon.svelte';
	import type { JobState } from '#lib/api.js';
	import { stopJob } from '#lib/api.js';
	import { bytes } from '#lib/format.js';
	import { busy, failure, headline, fraction } from '#lib/job.js';
	import { text } from '#lib/strings.js';

	import Avatar from './Avatar.svelte';
	import { app, personOfAccount, resumeJob } from '#lib/app.svelte.js';
	import { roughly } from '#lib/format.js';

	let { account, job, ondetails }: { account: string; job: JobState; ondetails?: () => void } =
		$props();
	const email = $derived(app.overview?.accounts.find((a) => a.id === account)?.email ?? '');
	const left = $derived(app.left[account]);
</script>

<span class="icon" class:err={job.error && job.problem !== 'not-offered'} class:warn={job.problem === 'not-offered'} class:live={job.running && !busy(job)} class:spin={busy(job)}>
	{#if job.error}<Icon name="exclamation-circle" size={16} />{:else if busy(job)}<Icon name="arrow-repeat" size={16} />{:else}<Icon name="download" size={16} />{/if}
</span>
<span class="what">{headline(job)}</span>
{#if email}<span class="who"><Avatar account={{ id: account, name: email, email, shown: true }} index={personOfAccount(account)} />{email}</span>{/if}
{#if job.total > 0}
	<span class="track"><b style:width="{fraction(job) * 100}%"></b></span>
	<span class="numbers"
		>{text.gbOf(bytes(job.done), bytes(job.total))}{left ? ` · ${text.aboutTime(text.left(roughly(left)))}` : ''}</span
	>
{/if}
{#if job.retry}<span class="note">{text.retrying(job.retry)}</span>{/if}
{#if job.error}<span class="note" class:err={job.problem !== 'not-offered'} title={failure(job)}>{failure(job)}</span>{/if}
{#if job.running && !job.retry}<span class="note sleep">{text.pickUpAfterSleep}</span>{/if}
<span class="spacer"></span>
<button class="details" onclick={ondetails}>{text.details}<Icon name="chevron-up" size={14} /></button>
{#if job.running}
	<button class="ctl" aria-label={text.pause} onclick={() => stopJob(account)}><Icon name="pause-fill" size={16} /></button>
{:else if !job.finished && job.problem !== 'not-offered'}
	<button class="ctl" aria-label={text.resume} onclick={() => resumeJob(account, job)}
		><Icon name="play-fill" size={16} /></button
	>
{/if}

<style>
	.icon {
		display: grid;
		color: var(--accent);
	}
	.icon.live {
		animation: pulse var(--motion-pulse) ease-in-out infinite;
	}
	@keyframes pulse {
		50% {
			opacity: 0.35;
		}
	}
	.icon.spin {
		animation: spin var(--motion-spin) linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
	.icon.warn {
		color: var(--warn);
	}
	.icon.err {
		color: var(--err);
	}
	.what {
		font-weight: 600;
		color: var(--fg);
		white-space: nowrap;
	}
	.who {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		padding: 2px 12px 2px 2px;
		border: 1px solid var(--border);
		border-radius: 999px;
		background: var(--soft);
		color: var(--fg);
		font-weight: 600;
		white-space: nowrap;
	}
	.track {
		width: 160px;
		height: 8px;
		flex-shrink: 0;
		border-radius: 999px;
		background: var(--line);
		overflow: hidden;
	}
	.track b {
		display: block;
		height: 100%;
		border-radius: 999px;
		background: var(--accent);
	}
	.numbers {
		color: var(--muted);
		white-space: nowrap;
	}
	.note {
		color: var(--faint);
		font-size: var(--text-callout);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		min-width: 0;
	}
	.note.sleep {
		order: 10;
	}
	.note.err {
		color: var(--err);
	}
	.spacer {
		flex: 1;
	}
	.details {
		display: flex;
		align-items: center;
		gap: 4px;
		color: var(--accent);
		font-weight: 600;
	}
	.ctl {
		display: grid;
		color: var(--muted);
	}
</style>
