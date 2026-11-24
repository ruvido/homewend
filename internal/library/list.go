// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Listing is one profile's library as a phone lists it: its months, newest
// first, each with a tag that moves when its photos do, and its albums by
// name. What changed is found by comparing: nothing is kept in the catalog.
type Listing struct {
	Shelf  string        `json:"shelf"`
	Months []ListedMonth `json:"months"`
	Albums []ListedAlbum `json:"albums"`
}

// ListedMonth is a month, "2026-11", or undatedMonth.
type ListedMonth struct {
	Month string `json:"month"`
	Count int    `json:"count"`
	Tag   string `json:"tag"`
}

// ListedAlbum is an album of the library. Cover is its newest photo.
type ListedAlbum struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
	Cover string `json:"cover"`
}

// listed is a photo of a month; taken is as the window shows it, with no zone.
type listed struct {
	Hash     string   `json:"hash"`
	Taken    string   `json:"taken,omitempty"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Bytes    int64    `json:"bytes"`
	Turn     int      `json:"turn"`
	Archived bool     `json:"archived"`
	Albums   []string `json:"albums"`
	albums   []string // their names
}

// undatedMonth is the month of the photos whose date nobody knows, and
// monthLayout how the others are written: as the window groups them.
const (
	undatedMonth = "none"
	monthLayout  = "2006-01"
)

func (p listed) month() string {
	if p.Taken == "" {
		return undatedMonth
	}
	return p.Taken[:len(monthLayout)]
}

// Listing is the listing of profile's library.
func (c *Catalog) Listing(profile string) (Listing, error) {
	l := Listing{Months: []ListedMonth{}, Albums: []ListedAlbum{}}
	photos, shelf, err := c.listed(profile, "", nil)
	if err != nil {
		return l, err
	}
	l.Shelf = shelf
	albums := map[string]*ListedAlbum{}
	var answer bytes.Buffer
	for len(photos) > 0 {
		month, n := photos[0].month(), 1
		for n < len(photos) && photos[n].month() == month {
			n++
		}
		for _, p := range photos[:n] {
			for i, name := range p.albums {
				// The newest come first.
				if albums[name] == nil {
					albums[name] = &ListedAlbum{ID: p.Albums[i], Name: name, Cover: p.Hash}
				}
				albums[name].Count++
			}
		}
		answer.Reset()
		if err := writeListed(&answer, photos[:n]); err != nil {
			return l, err
		}
		l.Months = append(l.Months, ListedMonth{month, n, tagOf(answer.Bytes())})
		photos = photos[n:]
	}
	for _, a := range albums {
		l.Albums = append(l.Albums, *a)
	}
	slices.SortFunc(l.Albums, func(a, b ListedAlbum) int { return cmp.Compare(a.Name, b.Name) })
	return l, nil
}

// Month is a month of profile's library: its photos, newest first, as JSON,
// and their tag, the listing's; no photos when it has none.
func (c *Catalog) Month(profile, month string) (tag string, photos []byte, err error) {
	where, args := "taken IS NULL", []any{}
	if month != undatedMonth {
		from, err := time.Parse(monthLayout, month)
		if err != nil {
			return "", nil, nil
		}
		where, args = "taken >= ? AND taken < ?", []any{from.Unix(), from.AddDate(0, 1, 0).Unix()}
	}
	list, _, err := c.listed(profile, where, args)
	if err != nil || len(list) == 0 {
		return "", nil, err
	}
	var answer bytes.Buffer
	err = writeListed(&answer, list)
	return tagOf(answer.Bytes()), answer.Bytes(), err
}

// tagOf is a digest of an answer.
func tagOf(answer []byte) string {
	sum := sha256.Sum256(answer)
	return hex.EncodeToString(sum[:16])
}

// Tag is the listing's, as its JSON is.
func (l Listing) Tag() (tag string, answer []byte, err error) {
	answer, err = json.Marshal(l)
	return tagOf(answer), answer, err
}

func writeListed(into *bytes.Buffer, photos []listed) error {
	out := json.NewEncoder(into)
	for i, p := range photos {
		into.WriteByte("[,"[min(i, 1)])
		if err := out.Encode(p); err != nil {
			return err
		}
	}
	into.WriteByte(']')
	return nil
}

// listed are the photos of profile's library that a list has, archived ones
// too, newest first and by hash, and the library's shelf: all, or those of
// where. The catalog may be written between its queries: the listing then
// moves again.
func (c *Catalog) listed(profile, where string, args []any) (photos []listed, shelf string, err error) {
	s, err := c.Shelf(profile)
	if err != nil {
		return nil, "", err
	}
	from := ` FROM library_photos WHERE profile = ? AND NOT ` + away
	if where != "" {
		from += " AND " + where
	}
	args = append([]any{profile}, args...)
	rows, err := c.db.Query(`SELECT id, hash, taken, name, bytes, turn, archived`+from, args...)
	if err != nil {
		return nil, "", fmt.Errorf("reading the catalog: %w", err)
	}
	defer rows.Close()
	place := map[int64]int{}
	for rows.Next() {
		var id int64
		var taken sql.NullInt64
		p := listed{Albums: []string{}}
		if err := rows.Scan(&id, &p.Hash, &taken, &p.Name, &p.Bytes, &p.Turn, &p.Archived); err != nil {
			return nil, "", err
		}
		if taken.Valid {
			p.Taken = time.Unix(taken.Int64, 0).UTC().Format("2006-01-02T15:04:05")
		}
		p.Type = TypeOf(p.Name)
		place[id] = len(photos)
		photos = append(photos, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	rows.Close()
	members, err := c.db.Query(`SELECT photo, album FROM album_members WHERE photo IN (SELECT id`+from+`) ORDER BY album`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("reading the catalog: %w", err)
	}
	defer members.Close()
	ids := map[string]string{}
	for members.Next() {
		var id int64
		var album string
		if err := members.Scan(&id, &album); err != nil {
			return nil, "", err
		}
		if ids[album] == "" {
			ids[album] = tagOf([]byte(s.ID + "/" + album))
		}
		if i, in := place[id]; in {
			photos[i].Albums, photos[i].albums = append(photos[i].Albums, ids[album]), append(photos[i].albums, album)
		}
	}
	// No date is "", after every date.
	slices.SortFunc(photos, func(a, b listed) int {
		return cmp.Or(strings.Compare(b.Taken, a.Taken), strings.Compare(a.Hash, b.Hash))
	})
	return photos, s.ID, members.Err()
}

// Here is the photo k, unless its library has it not, or deleted: in the
// Trash it is still there to be restored.
func (c *Catalog) Here(k Key) (Photo, bool, error) {
	photos, err := c.photos(`WHERE hash = ? AND profile = ? AND out = 0`, k.Hash, k.Profile)
	if err != nil || len(photos) == 0 {
		return Photo{}, false, err
	}
	return photos[0], true, nil
}
