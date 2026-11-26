// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { runnerImport } from 'vite';

const lib = fileURLToPath(new URL('../src/lib', import.meta.url));
const { module } = await runnerImport('/src/lib/api.ts', { configFile: false, resolve: { alias: { '#lib': lib } } });
const { asPhotos, copiesOf, holds, patched, photosIn } = module;

// As the engine lists them: newest first, a photo of two libraries one
// tile, its first person's copy with the others'.
const list = () => ({
	profiles: ['ann', 'ron'],
	photos: [
		{ hash: 'b', name: 'b.jpg', taken: '2026-11-02T00:00:00Z' },
		{ hash: 'a', name: 'a.jpg', taken: '2026-11-01T00:00:00Z', favorite: true, also: [{ hash: 'a', of: 1, name: 'a.jpg', taken: '2026-11-03T00:00:00Z' }] },
		{ hash: 'c', of: 1, name: 'c.jpg' }
	]
});

test('a tile of two libraries holds each one’s copy, the first its own', () => {
	const tiles = asPhotos(list());
	assert.deepEqual(tiles.map((t) => t.id), ['ann/b', 'ann/a', 'ron/c']);
	const a = tiles[1];
	assert.deepEqual(a.copies.map((c) => c.id), ['ann/a', 'ron/a']);
	assert.equal(a.favorite, true);
	assert.equal(a.copies[1].favorite, undefined);
	assert.equal(a.copies[0].copies, undefined);
	assert.equal(tiles[0].copies, undefined);
	assert.deepEqual(copiesOf(tiles).map((c) => c.id), ['ann/b', 'ann/a', 'ron/a', 'ron/c']);
	assert.equal(photosIn(copiesOf(tiles)), 3);
	assert.ok(holds(a, 'ron/a') && !holds(a, 'ron/c'));
});

test('a copy changed changes its tile alone; the tile shows the first', () => {
	const tiles = asPhotos(list());
	const ron = patched(tiles, (p) => (p.id === 'ron/a' ? { ...p, favorite: true } : p));
	assert.equal(ron[0], tiles[0]);
	assert.equal(ron[1].copies[1].favorite, true);
	const ann = patched(tiles, (p) => (p.id === 'ann/a' ? { ...p, favorite: false } : p));
	assert.equal(ann[1].favorite, false);
	assert.equal(ann[1].copies[0].favorite, false);
	assert.equal(patched(tiles, (p) => p)[1], tiles[1]);
});

