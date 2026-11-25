package mysql

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
)

// Aprobación por ficha, gestores por cliente y recordatorios de solicitudes pendientes (V0019).

func (r SelectionRepo) HireFormApprove(ctx context.Context, m ports.Meta, tenantID uint64, requestID, form string, data json.RawMessage, equipmentRequired *bool) (completed, first bool, err error) {
	var eq any
	if equipmentRequired != nil {
		eq = *equipmentRequired
	}
	rs, err := r.call(ctx, "sp_hire_form_approve", m.ActorID, tenantID, requestID, form, string(data), eq, nullStr(m.IP))
	if err != nil {
		return false, false, err
	}
	defer rs.Close()
	rs.Next()
	if err := rs.One(&completed, &first); err != nil {
		return false, false, err
	}
	return completed, first, rs.Close()
}

func (r SelectionRepo) HireEquipmentRequired(ctx context.Context, m ports.Meta, tenantID uint64, requestID string, required bool) error {
	return r.exec(ctx, "sp_hire_equipment_required", m.ActorID, tenantID, requestID, required, nullStr(m.IP))
}

func (r SelectionRepo) HireApprovals(ctx context.Context, m ports.Meta, tenantID uint64, requestID string) ([]json.RawMessage, error) {
	return docRepo(r).rawDocs(ctx, "sp_hire_approvals", m.ActorID, tenantID, requestID)
}

func (r SelectionRepo) ClientManagers(ctx context.Context, m ports.Meta, tenantID uint64) ([]json.RawMessage, error) {
	return docRepo(r).rawDocs(ctx, "sp_client_managers_list", m.ActorID, tenantID)
}

func (r SelectionRepo) ClientManagersSet(ctx context.Context, m ports.Meta, tenantID uint64, clientID, primaryID, secondaryID string) error {
	return r.exec(ctx, "sp_client_managers_set", m.ActorID, tenantID, clientID, nullStr(primaryID), nullStr(secondaryID), nullStr(m.IP))
}

func (r SelectionRepo) ClientManagerCandidates(ctx context.Context, m ports.Meta, tenantID uint64) ([]json.RawMessage, error) {
	return docRepo(r).rawDocs(ctx, "sp_client_manager_candidates", m.ActorID, tenantID)
}

func (r SelectionRepo) HireReminderTenants(ctx context.Context) ([]ports.ReminderTenant, error) {
	rs, err := r.call(ctx, "sp_hire_reminder_tenants")
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []ports.ReminderTenant{}
	if rs.Next() {
		if err := rs.Each(func(scan func(...any) error) error {
			var t ports.ReminderTenant
			if err := scan(&t.ID, &t.Slug, &t.Company, &t.ReminderDays); err != nil {
				return err
			}
			out = append(out, t)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return out, rs.Close()
}

func (r SelectionRepo) HireReminderClaim(ctx context.Context, tenantID uint64, kind string, today time.Time, every int) (bool, error) {
	rs, err := r.call(ctx, "sp_hire_reminder_claim", tenantID, kind, today.Format("2006-01-02"), every)
	if err != nil {
		return false, err
	}
	defer rs.Close()
	var n int
	rs.Next()
	if err := rs.One(&n); err != nil {
		return false, err
	}
	return n > 0, rs.Close()
}

func (r SelectionRepo) HirePending(ctx context.Context, tenantID uint64) (requests, admins []json.RawMessage, err error) {
	rs, err := r.call(ctx, "sp_hire_pending", tenantID)
	if err != nil {
		return nil, nil, err
	}
	defer rs.Close()
	read := func() ([]json.RawMessage, error) {
		out := []json.RawMessage{}
		err := rs.Each(func(scan func(...any) error) error {
			var b []byte
			if err := scan(&b); err != nil {
				return err
			}
			out = append(out, json.RawMessage(b))
			return nil
		})
		return out, err
	}
	requests, admins = []json.RawMessage{}, []json.RawMessage{}
	if rs.Next() {
		if requests, err = read(); err != nil {
			return nil, nil, err
		}
	}
	if rs.Next() {
		if admins, err = read(); err != nil {
			return nil, nil, err
		}
	}
	return requests, admins, rs.Close()
}

func (r SelectionRepo) IntegrationManagers(ctx context.Context, tenantID uint64) ([]ports.ManagerUser, []ports.ClientManagerRef, error) {
	rs, err := r.call(ctx, "sp_integration_managers", tenantID)
	if err != nil {
		return nil, nil, err
	}
	defer rs.Close()
	users, refs := []ports.ManagerUser{}, []ports.ClientManagerRef{}
	if rs.Next() {
		if err := rs.Each(func(scan func(...any) error) error {
			var u ports.ManagerUser
			if err := scan(&u.ID, &u.NameEnc); err != nil {
				return err
			}
			users = append(users, u)
			return nil
		}); err != nil {
			return nil, nil, err
		}
	}
	if rs.Next() {
		if err := rs.Each(func(scan func(...any) error) error {
			var c ports.ClientManagerRef
			if err := scan(&c.ClientID, &c.Role, &c.UserID); err != nil {
				return err
			}
			refs = append(refs, c)
			return nil
		}); err != nil {
			return nil, nil, err
		}
	}
	return users, refs, rs.Close()
}
