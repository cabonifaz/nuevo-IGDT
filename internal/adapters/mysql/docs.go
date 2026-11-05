package mysql

import (
	"context"
	"encoding/json"
)

// docRepo: SP que devuelven un result set de una columna JSON (doc) por fila.
type docRepo struct{ *Store }

func (r docRepo) rawDocs(ctx context.Context, proc string, args ...any) ([]json.RawMessage, error) {
	rs, err := r.call(ctx, proc, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []json.RawMessage{}
	if !rs.Next() {
		return out, nil
	}
	err = rs.Each(func(scan func(...any) error) error {
		var doc []byte
		if err := scan(&doc); err != nil {
			return err
		}
		out = append(out, json.RawMessage(doc))
		return nil
	})
	return out, err
}

func optJSON(j json.RawMessage) any {
	if len(j) == 0 || string(j) == "null" {
		return nil
	}
	return string(j)
}
