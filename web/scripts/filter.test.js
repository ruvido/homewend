// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { runnerImport } from 'vite';

const lib = fileURLToPath(new URL('../src/lib', import.meta.url));
const { module } = await runnerImport('/src/lib/filter.ts', { configFile: false, resolve: { alias: { '#lib': lib } } });
const { badgesOf, countBadges, isFiltering, noFilter, search } = module;

const photos = [
	{ name: 'PXL_20210512_101500.jpg', taken: '2021-05-12T10:15:00Z' },
	{ name: 'IMG-20210513-WA0001.jpg', taken: '2021-05-13T10:15:00Z', origin: 'whatsapp' },
	{ name: 'Papà al mare.JPG', taken: '2019-08-01T10:15:00Z', guessed: 'day' },
	// As a Mac writes it: the accent a letter of its own.
	{ name: 'Papà 2.heic' },
	{ name: 'birthday.2021.mp4', origin: 'app' }
];
const found = (f) => {
	const all = { ...noFilter, ...f };
	return search(photos, all, badgesOf(countBadges(photos), all.badges)).map((p) => photos.indexOf(p));
};

test('a name has what is typed, in capitals or not, wherever it is', () => {
	assert.deepEqual(found({ name: '2021' }), [0, 1, 4]);
	assert.deepEqual(found({ name: 'pxl' }), [0]);
	assert.deepEqual(found({ name: 'JPG' }), [0, 1, 2]);
	assert.deepEqual(found({ name: 'nothing' }), []);
});

test('with and without its accents', () => {
	for (const name of ['papa', 'Papà', 'PAPÀ']) assert.deepEqual(found({ name }), [2, 3]);
});

test('every word, in any order, without spaces and dots around it', () => {
	assert.deepEqual(found({ name: 'jpg 2021' }), [0, 1]);
	assert.deepEqual(found({ name: '  .mp4. ' }), [4]);
	assert.deepEqual(found({ name: 'birthday.2021' }), [4]);
});

test('nothing but spaces and dots is no search', () => {
	assert.equal(isFiltering({ ...noFilter, name: ' . ' }), false);
	assert.equal(search(photos, { ...noFilter, name: ' . ' }, []), photos);
	assert.equal(isFiltering({ ...noFilter, name: 'a' }), true);
});

test('a name, the days and the badges narrow each other', () => {
	assert.deepEqual(found({ name: '2021', badges: ['whatsapp'] }), [1]);
	assert.deepEqual(found({ name: 'jpg', start: '2021-05-12', end: '2021-05-12' }), [0]);
	assert.deepEqual(found({ name: 'papa', badges: ['estimated'] }), [2]);
	assert.deepEqual(found({ name: '2021', badges: ['other'] }), [4]);
});
