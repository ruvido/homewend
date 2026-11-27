// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { runnerImport } from 'vite';

const lib = fileURLToPath(new URL('../src/lib', import.meta.url));
const { module } = await runnerImport('/src/lib/api.ts', { configFile: false, resolve: { alias: { '#lib': lib } } });
const { isNegativeRaw, pictureOf } = module;

test('Convert negative is offered on a RAW only', () => {
	assert.ok(isNegativeRaw({ name: 'DSCF0050.RAF' }));
	assert.ok(isNegativeRaw({ name: 'dscf0050.raf' }));
	for (const name of ['DSCF0050.JPG', 'IMG_1.HEIC', 'raf.png', 'clip.mp4']) assert.ok(!isNegativeRaw({ name }), name);
	// A selection with one JPEG among RAWs is not offered it.
	assert.ok(![{ name: 'a.RAF' }, { name: 'b.JPG' }].every(isNegativeRaw));
});

test('a printed negative is asked by its print', () => {
	const p = { hash: 'h', profile: 'ron' };
	assert.equal(pictureOf(p), '/api/thumb/h?profile=ron');
	assert.equal(pictureOf({ ...p, edit: 'e', positive: 'f' }, 'preview'), '/api/preview/h?profile=ron&edit=e&positive=f');
});
