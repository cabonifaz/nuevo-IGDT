package ports

import "context"

// SyncRepository registra operaciones de la cola offline (idempotencia).
type SyncRepository interface {
	Register(ctx context.Context, tenantID, userID uint64, opID, entity, op string) (duplicate bool, status string, result []byte, err error)
	Complete(ctx context.Context, tenantID uint64, opID, status string, result []byte) error
}
