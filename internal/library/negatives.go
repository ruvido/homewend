// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
)

// negativesSchema keeps the RAWs of film negatives shown printed, with the
// bounds they are printed with (raw.Bounds as text): computed once, over the
// photos converted together, so a photo looks the same tomorrow. Part of
// the development, like turn, and the computer's alone: the phone sees the
// print through its pictures.
const negativesSchema = `
CREATE TABLE negatives (
  photo  INTEGER PRIMARY KEY REFERENCES library_photos(id),
  bounds TEXT NOT NULL
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

// SetNegatives has the photos shown printed with bounds, or, with "", as
// they came.
func (c *Catalog) SetNegatives(photos []Key, bounds string) error {
	return c.inTx(func(tx *sql.Tx) error {
		for _, k := range photos {
			var id int64
			if err := tx.QueryRow(`SELECT id FROM library_photos WHERE hash = ? AND profile = ?`, k.Hash, k.Profile).Scan(&id); err == sql.ErrNoRows {
				continue
			} else if err != nil {
				return err
			}
			var err error
			if bounds == "" {
				_, err = tx.Exec(`DELETE FROM negatives WHERE photo = ?`, id)
			} else {
				_, err = tx.Exec(`INSERT INTO negatives VALUES (?, ?) ON CONFLICT DO UPDATE SET bounds = excluded.bounds`, id, bounds)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
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
