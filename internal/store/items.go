package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Item is the template's example record. Replace it with the app's own.
type Item struct {
	ID      int64     `json:"id"`
	Title   string    `json:"title"`
	Note    string    `json:"note"`
	Done    bool      `json:"done"`
	Created time.Time `json:"created"`
}

// Items lists every item, open ones first, newest first.
func (s *Store) Items(ctx context.Context) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, note, done, created FROM items ORDER BY done, created DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var it Item
		var created int64
		if err := rows.Scan(&it.ID, &it.Title, &it.Note, &it.Done, &created); err != nil {
			return nil, err
		}
		it.Created = fromMS(created)
		out = append(out, it)
	}
	return out, rows.Err()
}

// SaveItem inserts an item without an ID, and updates one with an ID.
func (s *Store) SaveItem(ctx context.Context, it *Item) error {
	if it.ID == 0 {
		it.Created = time.Now()
		res, err := s.db.ExecContext(ctx,
			`INSERT INTO items (title, note, done, created) VALUES (?, ?, ?, ?)`,
			it.Title, it.Note, it.Done, ms(it.Created))
		if err != nil {
			return err
		}
		it.ID, err = res.LastInsertId()
		return err
	}
	var created int64
	err := s.db.QueryRowContext(ctx,
		`UPDATE items SET title = ?, note = ?, done = ? WHERE id = ? RETURNING created`,
		it.Title, it.Note, it.Done, it.ID).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	it.Created = fromMS(created)
	return nil
}

func (s *Store) DeleteItem(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
