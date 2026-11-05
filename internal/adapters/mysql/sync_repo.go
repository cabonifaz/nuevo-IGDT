package mysql

import (
	"context"
	"time"
)

type SyncRepo struct{ *Store }

func (r SyncRepo) Register(ctx context.Context, tenantID, userID uint64, opID, entity, op string) (bool, string, []byte, error) {
	rs, err := r.call(ctx, "sp_sync_register", tenantID, userID, opID, entity, op, nil, time.Now().UTC())
	if err != nil {
		return false, "", nil, err
	}
	defer rs.Close()
	var dup bool
	var status string
	var result []byte
	rs.Next()
	if err := rs.One(&dup, &status, &result); err != nil {
		return false, "", nil, err
	}
	return dup, status, result, rs.Close()
}

func (r SyncRepo) Complete(ctx context.Context, tenantID uint64, opID, status string, result []byte) error {
	var res any
	if len(result) > 0 {
		res = string(result) // JSON: enviar como texto utf8, no binario
	}
	return r.exec(ctx, "sp_sync_complete", tenantID, opID, status, res)
}
