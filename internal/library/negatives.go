// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"cmp"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/ruvido/homewend/internal/edit"
)

// negativesSchema keeps the RAWs of film negatives shown printed, with the
// bounds they are printed with (raw.Bounds as text): computed once, over the
// photos converted together, so a photo looks the same tomorrow. Part of
// the development, like turn, and the computer's alone: the phone sees the
// print through its pictures. crop is the crop its edit was given, to the
// film's frame, and was the one it replaced: "" none.
const negativesSchema = `
CREATE TABLE negatives (
  photo  INTEGER PRIMARY KEY REFERENCES library_photos(id),
  bounds TEXT NOT NULL,
  crop   TEXT NOT NULL DEFAULT '',
  was    TEXT NOT NULL DEFAULT ''
);
CREATE TRIGGER negative_forgotten AFTER DELETE ON library_photos BEGIN
  DELETE FROM negatives WHERE photo = OLD.id;
END;
`

// PositiveName names the print of a negative with bounds: another print,
// and its pictures are others. "" for none.
func PositiveName(bounds string) string {
	if bounds == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(bounds))
	return hex.EncodeToString(sum[:8])
}

// Print has the photo shown printed with bounds, and its edit cropped to
// frame (x0, y0, x1, y1, in shares of the picture upright) unless the person
// framed it already: an edit, so the phone crops it too. A photo printed
// before loses the crop given then first.
func (c *Catalog) Print(k Key, bounds string, frame [4]float64) error {
	return c.inTx(func(tx *sql.Tx) error {
		var turn int
		if err := tx.QueryRow(`SELECT turn FROM library_photos WHERE hash = ? AND profile = ?`, k.Hash, k.Profile).Scan(&turn); err == sql.ErrNoRows {
			return nil
		} else if err != nil {
			return err
		}
		data, was, err := unprinted(tx, k)
		if err != nil {
			return err
		}
		out, crop, before, framed := edit.Framed(data, frame, turn)
		if framed {
			if _, err := setEdit(tx, k, was, out); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`INSERT INTO negatives VALUES (?, ?, ?, ?)
			ON CONFLICT DO UPDATE SET bounds = excluded.bounds, crop = excluded.crop, was = excluded.was`, was.id, bounds, crop, before)
		return err
	})
}

// Unprint has the photos shown as they came: the crop Print gave each is
// taken back, if it is still the one given.
func (c *Catalog) Unprint(photos []Key) error {
	return c.inTx(func(tx *sql.Tx) error {
		for _, k := range photos {
			data, was, err := unprinted(tx, k)
			if err != nil {
				return err
			}
			if was.id == 0 {
				continue
			}
			if data != cmp.Or(was.value, "null") {
				if _, err := setEdit(tx, k, was, data); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(`DELETE FROM negatives WHERE photo = ?`, was.id); err != nil {
				return err
			}
		}
		return nil
	})
}

// unprinted is k's edit without the crop a print gave it, if it is still
// that one, and the edit as it is.
func unprinted(tx *sql.Tx, k Key) (data string, was mark, err error) {
	edits, err := markIn(tx, Edit, false)
	if err != nil {
		return "", was, err
	}
	if was, err = edits.of(k, ""); err != nil {
		return "", was, err
	}
	data = cmp.Or(was.value, "null")
	var crop, before string
	err = tx.QueryRow(`SELECT crop, was FROM negatives WHERE photo = ?`, was.id).Scan(&crop, &before)
	if err == sql.ErrNoRows || err == nil && crop == "" {
		return data, was, nil
	}
	if err != nil {
		return "", was, err
	}
	return edit.Unframed(data, crop, before), was, nil
}

// setEdit gives k the edit data, now, as the window's Rotate does.
func setEdit(tx *sql.Tx, k Key, was mark, data string) (bool, error) {
	edits, err := markIn(tx, Edit, false)
	if err != nil {
		return false, err
	}
	return edits.give(k, "", was, mark{value: data, at: time.Now().UnixMilli()})
}

// negatives are the bounds of the photos shown printed, by their ids: a few,
// read whole. None where the catalog could not be given the table.
func (c *Catalog) negatives() (map[int64]string, error) {
	kept, err := hasTable(c.db, "negatives")
	if err != nil || !kept {
		return nil, err
	}
	rows, err := c.db.Query(`SELECT photo, bounds FROM negatives`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var bounds string
		if err := rows.Scan(&id, &bounds); err != nil {
			return nil, err
		}
		out[id] = bounds
	}
	return out, rows.Err()
}

// Negative is the bounds the photo is printed with; "" when it is not.
func (c *Catalog) Negative(k Key) (string, error) {
	kept, err := hasTable(c.db, "negatives")
	if err != nil || !kept {
		return "", err
	}
	var bounds string
	err = c.db.QueryRow(`SELECT n.bounds FROM negatives n JOIN library_photos p ON p.id = n.photo WHERE p.hash = ? AND p.profile = ?`, k.Hash, k.Profile).Scan(&bounds)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return bounds, err
}

// giveNegatives gives photos, of a list, the bounds they are printed with.
func (c *Catalog) giveNegatives(photos []Photo) error {
	kept, err := hasTable(c.db, "negatives")
	if err != nil || !kept {
		return err
	}
	rows, err := c.db.Query(`SELECT p.hash, p.profile, n.bounds FROM negatives n JOIN library_photos p ON p.id = n.photo`)
	if err != nil {
		return err
	}
	defer rows.Close()
	of := map[Key]string{}
	for rows.Next() {
		var k Key
		var bounds string
		if err := rows.Scan(&k.Hash, &k.Profile, &bounds); err != nil {
			return err
		}
		of[k] = bounds
	}
	if err := rows.Err(); err != nil || len(of) == 0 {
		return err
	}
	for i := range photos {
		photos[i].Negative = of[photos[i].Key()]
	}
	return nil
}
