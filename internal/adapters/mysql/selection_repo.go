package mysql

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// SelectionRepo: bandeja de selección (RQ de BDT y solicitudes de ingreso).
type SelectionRepo struct{ *Store }

func (r SelectionRepo) RequirementUpsert(ctx context.Context, tenantID uint64, q ports.RequirementWrite) (bool, error) {
	rs, err := r.call(ctx, "sp_bdt_rq_upsert", tenantID, q.ID, q.Code, q.Title, nullStr(q.ClientID), nullStr(q.ClientLabel),
		q.StatusCode, q.StatusLabel, q.StatusGroup, q.RequestDate, q.DueDate, q.Vacancies, q.Covered, q.Candidates,
		string(q.Doc), q.UpdatedAt.UTC(), q.Deleted)
	if err != nil {
		return false, err
	}
	defer rs.Close()
	var applied bool
	rs.Next()
	if err := rs.One(&applied); err != nil {
		return false, err
	}
	return applied, rs.Close()
}

func (r SelectionRepo) Requirements(ctx context.Context, m ports.Meta, tenantID uint64, scope string) ([]json.RawMessage, error) {
	return docRepo(r).rawDocs(ctx, "sp_bdt_rq_list", m.ActorID, tenantID, scope)
}

func (r SelectionRepo) Requirement(ctx context.Context, m ports.Meta, tenantID uint64, id string) (json.RawMessage, error) {
	return firstDoc(docRepo(r).rawDocs(ctx, "sp_bdt_rq_get", m.ActorID, tenantID, id))
}

func (r SelectionRepo) HireReceive(ctx context.Context, tenantID, apiKeyID uint64, h ports.HireWrite) (*ports.HireReceived, error) {
	rs, err := r.call(ctx, "sp_hire_request_receive", tenantID, apiKeyID, h.NewID, h.CandidateID, h.Active, h.RequirementID,
		h.TalentID, h.FirstName, h.LastName, nullStr(h.SecondLastName), nullStr(h.Headline), nullStr(h.DocTypeCode),
		nullStr(h.DocTypeLabel), nullBytes(h.DocNumberEnc), nullStr(h.DocNumberBidx), nullBytes(h.EmailEnc), nullBytes(h.PhoneEnc),
		nullStr(h.ClientID), nullStr(h.ClientLabel), nullStr(h.ProfileID), nullStr(h.ProfileLabel), string(h.Doc), h.ConfirmedAt.UTC())
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out ports.HireReceived
	var id sql.NullString
	var assigned sql.NullInt64
	rs.Next()
	if err := rs.One(&id, &out.Event, &assigned); err != nil {
		return nil, err
	}
	out.ID, out.Assigned = id.String, uint64(assigned.Int64)
	return &out, rs.Close()
}

func (r SelectionRepo) Hires(ctx context.Context, m ports.Meta, tenantID uint64, status *string) ([]json.RawMessage, error) {
	return docRepo(r).rawDocs(ctx, "sp_hire_request_list", m.ActorID, tenantID, status)
}

func (r SelectionRepo) Hire(ctx context.Context, m ports.Meta, tenantID uint64, id string) (json.RawMessage, error) {
	return firstDoc(docRepo(r).rawDocs(ctx, "sp_hire_request_get", m.ActorID, tenantID, id, nullStr(m.IP)))
}

func (r SelectionRepo) HireTake(ctx context.Context, m ports.Meta, tenantID uint64, id string) error {
	return r.exec(ctx, "sp_hire_request_take", m.ActorID, tenantID, id, nullStr(m.IP))
}

func firstDoc(list []json.RawMessage, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, domain.ErrNotFound
	}
	return list[0], nil
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
