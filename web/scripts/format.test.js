// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runnerImport } from 'vite';

const { module } = await runnerImport('/src/lib/format.ts', { configFile: false });

test('a span says the year only of a day not in this one', () => {
	const now = new Date(2026, 10, 24);
	const span = (from, to) => module.span(from, to, now);
	assert.equal(span(new Date(2024, 2, 9), new Date(2026, 9, 2)), 'Mar 2024 → 2 Oct');
	assert.equal(span(new Date(2026, 0, 1), new Date(2026, 9, 2)), '1 Jan → 2 Oct');
	assert.equal(span(new Date(2023, 0, 5), new Date(2025, 11, 31)), 'Jan 2023 → Dec 2025');
});
