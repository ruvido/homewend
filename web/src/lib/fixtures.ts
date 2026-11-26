// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// Made-up data for the components page only: the screens read the engine.
import type { Sidebar } from './library.js';

export const twoPeople: Sidebar = {
	people: [
		{ id: 'alex', name: 'alex', email: '', shown: true, index: 0 },
		{ id: 'sam', name: 'sam', email: '', shown: true, index: 1 }
	],
	accounts: [
		{ id: 'a', name: 'Alex Morgan', email: 'alex.morgan@example.com', index: 0 },
		{ id: 'b', name: 'Sam Rivera', email: 'sam.rivera@example.com', index: 1 }
	],
	total: 38584,
	years: [
		{ label: '2025', count: 2031, state: 'none' },
		{ label: '2024', count: 1610, state: 'arriving' },
		{ label: '2023', count: 2950, state: 'none' },
		{ label: '2022', count: 812, state: 'arriving' },
		{ label: 'Still sorting', count: 1240, state: 'arriving' },
		{ label: 'No date', count: 37, state: 'none' }
	],
	albums: [
		{ account: 'a', name: 'Greece 2019', count: 412 },
		{ account: 'a', name: 'Wedding', count: 1088 },
		{ account: 'a', name: 'Paris 2021', count: 230 },
		{ account: 'a', name: 'Lisbon', count: 96 },
		{ account: 'b', name: 'Kids', count: 6530 }
	],
	takeouts: 5,
	archived: 12,
	trashed: 3,
	favorites: 148,
	missing: 0
};

export const tenPeople: Sidebar = {
	...twoPeople,
	people: [
		...twoPeople.people,
		...['jordan', 'casey', 'riley', 'taylor', 'morgan', 'avery', 'quinn', 'drew'].map((name, i) => ({ id: name, name, email: '', shown: false, index: i + 2 }))
	]
};
