// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"bytes"
	"cmp"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math/bits"
	"slices"
	"strconv"
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
	Copy     string   `json:"copy,omitempty"` // a copy of the photo, made on a phone
	Taken    string   `json:"taken,omitempty"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Bytes    int64    `json:"bytes"`
	Turn     int      `json:"turn"`
	Archived bool     `json:"archived"`
	Albums   []string `json:"albums"`
	// As the catalog has them.
	id, taken    int64
	dated        bool
	turnArchived int // turn × 2, and 1 archived
	albums       []album
}

// undatedMonth is the month of the photos whose date nobody knows, and
// monthLayout how the others are written: as the window groups them.
const (
	undatedMonth = "none"
	monthLayout  = "2006-01"
)

// Listing is the listing of profile's library, and the hashes of the photos
// in it. Each photo is read once and kept by none: a month's tag is the sum
// of its photos' digests, in any order.
func (c *Catalog) Listing(profile string) (Listing, map[Hash]struct{}, error) {
	l := Listing{Months: []ListedMonth{}, Albums: []ListedAlbum{}}
	s, err := c.Shelf(profile)
	if err != nil {
		return l, nil, err
	}
	l.Shelf = s.ID
	of, err := c.albumsOf(s.ID, `SELECT album, group_concat(photo) FROM album_members GROUP BY album`)
	if err != nil {
		return l, nil, err
	}
	months, albums, hashes := map[int]*monthSum{}, map[album]*albumSum{}, map[Hash]struct{}{}
	// Its month, 0 with no date, and its sum: the photo before is often of it.
	month, sum := -1, (*monthSum)(nil)
	err = c.eachListed(profile, "", nil, func(p listed) {
		p.albums = of[p.id]
		in := 0
		if p.dated {
			year, m, _ := p.at().Date()
			in = year*12 + int(m)
		}
		if in != month {
			month = in
			if sum = months[in]; sum == nil {
				sum = &monthSum{name: p.month()}
				months[in] = sum
			}
		}
		sum.add(p)
		if p.Copy != "" {
			return
		}
		var h Hash
		if n, err := hex.Decode(h[:], []byte(p.Hash)); n == len(h) && err == nil {
			hashes[h] = struct{}{}
		}
		for _, its := range p.albums {
			a := albums[its]
			if a == nil {
				a = &albumSum{cover: p}
				albums[its] = a
			}
			a.n++
			if p.newer(a.cover) < 0 {
				a.cover = p
			}
		}
	})
	if err != nil {
		return l, nil, err
	}
	for _, m := range slices.Backward(slices.Sorted(maps.Keys(months))) {
		if m != 0 {
			l.Months = append(l.Months, months[m].month())
		}
	}
	if none := months[0]; none != nil {
		l.Months = append(l.Months, none.month())
	}
	for its, a := range albums {
		l.Albums = append(l.Albums, ListedAlbum{its.id, its.name, a.n, a.cover.Hash})
	}
	slices.SortFunc(l.Albums, func(a, b ListedAlbum) int { return cmp.Compare(a.Name, b.Name) })
	return l, hashes, nil
}

// Hash is a photo's, as bytes.
type Hash [sha256.Size]byte

type albumSum struct {
	n     int
	cover listed
}

// monthSum adds up a month's photos: md5, a third of SHA-256's time where no
// processor does it, tells a change and guards nothing.
type monthSum struct {
	name   string
	n      int
	hi, lo uint64
	line   []byte
}

func (m *monthSum) add(p listed) {
	b := append(m.line[:0], p.Hash...)
	b = append(append(b, 0), p.Copy...)
	b = append(b, p.Type...)
	for _, n := range []int64{p.taken, int64(p.turnArchived), p.Bytes, int64(len(p.albums)), int64(len(p.Type))} {
		b = binary.BigEndian.AppendUint64(b, uint64(n))
	}
	for _, its := range p.albums {
		b = append(b, its.id...)
	}
	m.line = append(b, p.Name...)
	sum := md5.Sum(m.line)
	var carry uint64
	m.lo, carry = bits.Add64(m.lo, binary.BigEndian.Uint64(sum[8:]), 0)
	m.hi, _ = bits.Add64(m.hi, binary.BigEndian.Uint64(sum[:8]), carry)
	m.n++
}

func (m *monthSum) month() ListedMonth {
	return ListedMonth{m.name, m.n, fmt.Sprintf("%016x%016x", m.hi, m.lo)}
}

// Month is a month of profile's library: its photos, newest first, as JSON,
// and their tag, the listing's; no photos when it has none. The catalog may be
// written between its queries: the listing then moves again.
func (c *Catalog) Month(profile, month string) (tag string, photos []byte, err error) {
	where, args := "taken IS NULL", []any{profile}
	if month != undatedMonth {
		from, err := time.Parse(monthLayout, month)
		if err != nil {
			return "", nil, nil
		}
		where, args = "taken >= ? AND taken < ?", []any{profile, from.Unix(), from.AddDate(0, 1, 0).Unix()}
	}
	s, err := c.Shelf(profile)
	if err != nil {
		return "", nil, err
	}
	of, err := c.albumsOf(s.ID, `SELECT album, group_concat(photo) FROM album_members
		WHERE photo IN (SELECT id FROM library_photos WHERE profile = ? AND `+where+`) GROUP BY album`, args...)
	if err != nil {
		return "", nil, err
	}
	var list []listed
	sum := monthSum{name: month}
	err = c.eachListed(profile, " AND "+where, args[1:], func(p listed) {
		p.Albums = []string{}
		if p.Copy == "" { // a copy is in no album
			p.albums = of[p.id]
		}
		for _, its := range p.albums {
			p.Albums = append(p.Albums, its.id)
		}
		p.Turn, p.Archived = p.turnArchived/2, p.turnArchived%2 == 1
		if p.dated {
			p.Taken = p.at().Format("2006-01-02T15:04:05")
		}
		sum.add(p)
		list = append(list, p)
	})
	if err != nil || len(list) == 0 {
		return "", nil, err
	}
	slices.SortFunc(list, listed.newer)
	var answer bytes.Buffer
	out := json.NewEncoder(&answer)
	for i, p := range list {
		answer.WriteByte("[,"[min(i, 1)])
		if err := out.Encode(p); err != nil {
			return "", nil, err
		}
	}
	answer.WriteByte(']')
	return sum.month().Tag, answer.Bytes(), nil
}

// Tag is the listing's, a digest of its JSON, which it is with.
func (l Listing) Tag() (tag string, answer []byte, err error) {
	answer, err = json.Marshal(l)
	return tagOf(answer), answer, err
}

// tagOf is a digest of an answer.
func tagOf(answer []byte) string {
	sum := sha256.Sum256(answer)
	return hex.EncodeToString(sum[:16])
}

// IsTag is whether tag may be one.
func IsTag(tag string) bool {
	b, err := hex.DecodeString(tag)
	return err == nil && len(b) == 16
}

// album is an album of a shelf: its id is a phone's, of the two.
type album struct{ id, name string }

// albumsOf are the albums of each photo by its id, by name, from a query of
// each album and its photos' ids with a comma between: a row an album, where
// a row a photo takes as long as the photos.
func (c *Catalog) albumsOf(shelf, query string, args ...any) (map[int64][]album, error) {
	rows, err := c.db.Query(query+` ORDER BY album`, args...)
	if err != nil {
		return nil, fmt.Errorf("reading the catalog: %w", err)
	}
	defer rows.Close()
	of := map[int64][]album{}
	for rows.Next() {
		var name, photos string
		if err := rows.Scan(&name, &photos); err != nil {
			return nil, err
		}
		its := album{tagOf([]byte(shelf + "/" + name)), name}
		for one := range strings.SplitSeq(photos, ",") {
			id, err := strconv.ParseInt(one, 10, 64)
			if err != nil {
				return nil, err
			}
			of[id] = append(of[id], its)
		}
	}
	return of, rows.Err()
}

// eachListed reads the photos of profile's library a list has, archived ones
// too, as they lie. It asks the driver itself: database/sql's Scan takes as
// long again (100,000, 24/11).
func (c *Catalog) eachListed(profile, where string, args []any, each func(listed)) error {
	copies, err := c.copiesOf(profile)
	if err != nil {
		return err
	}
	ctx := context.Background()
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reading the catalog: %w", err)
	}
	defer conn.Close()
	return conn.Raw(func(d any) error {
		named := []driver.NamedValue{{Ordinal: 1, Value: profile}}
		for i, a := range args {
			named = append(named, driver.NamedValue{Ordinal: i + 2, Value: a})
		}
		rows, err := d.(driver.QueryerContext).QueryContext(ctx, `SELECT id, hash, taken, name, bytes, turn * 2 + archived
			FROM library_photos WHERE profile = ? AND NOT `+away+where, named)
		if err != nil {
			return fmt.Errorf("reading the catalog: %w", err)
		}
		defer rows.Close()
		row := make([]driver.Value, len(rows.Columns()))
		for {
			if err := rows.Next(row); err == io.EOF {
				return nil
			} else if err != nil {
				return fmt.Errorf("reading the catalog: %w", err)
			}
			var p listed
			p.id, _ = row[0].(int64)
			p.Hash, _ = row[1].(string)
			p.taken, p.dated = row[2].(int64)
			p.Name, _ = row[3].(string)
			p.Bytes, _ = row[4].(int64)
			both, _ := row[5].(int64)
			p.turnArchived, p.Type = int(both), TypeOf(p.Name)
			each(p)
			for _, copy := range copies[p.Hash] {
				p.Copy = copy
				each(p)
			}
		}
	})
}

// copiesOf are the copies of profile's photos there are, by hash: those with
// a mark and no tombstone.
func (c *Catalog) copiesOf(profile string) (map[string][]string, error) {
	kept, err := hasTable(c.db, "extra_marks")
	if err != nil || !kept {
		return nil, err
	}
	rows, err := c.db.Query(`SELECT hash, copy FROM extra_marks WHERE profile = ? AND copy <> '' GROUP BY hash, copy
		HAVING NOT max(state = '`+string(Copy)+`' AND value = 'false') ORDER BY hash, copy`, profile)
	if err != nil {
		return nil, fmt.Errorf("reading the catalog: %w", err)
	}
	defer rows.Close()
	copies := map[string][]string{}
	for rows.Next() {
		var hash, copy string
		if err := rows.Scan(&hash, &copy); err != nil {
			return nil, err
		}
		copies[hash] = append(copies[hash], copy)
	}
	return copies, rows.Err()
}

func (p listed) at() time.Time { return time.Unix(p.taken, 0).UTC() }

func (p listed) month() string {
	if !p.dated {
		return undatedMonth
	}
	return p.at().Format(monthLayout)
}

// newer orders photos newest first, those with no date last, then by hash.
func (p listed) newer(q listed) int {
	switch {
	case p.dated && !q.dated:
		return -1
	case q.dated && !p.dated:
		return 1
	}
	return cmp.Or(cmp.Compare(q.taken, p.taken), cmp.Compare(p.Hash, q.Hash))
}

// Written is the catalog as of its last write: the same one, nothing was written.
type Written struct {
	Path, Changes string
	writes        uint32
}

// Written is the catalog's now. Its count of writes is SQLite's own
// (sqlite.org/fileformat2.html, 1.3.4), read through SQLite, as everything of
// the file (openCatalog): two writes the disk's clock shows as one are two.
func (c *Catalog) Written() (Written, error) {
	s := Written{Path: c.path, Changes: Changes(c.path)}
	var head []byte
	err := c.db.QueryRow(`SELECT data FROM sqlite_dbpage WHERE pgno = 1`).Scan(&head)
	if err != nil || len(head) < 28 {
		return s, fmt.Errorf("reading the catalog: %w", cmp.Or(err, io.ErrUnexpectedEOF))
	}
	s.writes = binary.BigEndian.Uint32(head[24:])
	return s, nil
}

// Next is whether s is before with one write more and no other. In WAL mode,
// where writes are not counted so, it is not known.
func (s Written) Next(before Written) bool {
	return s.Path == before.Path && s.writes == before.writes+1 &&
		strings.Count(s.Changes, ";") == 1 && strings.Count(before.Changes, ";") == 1
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
