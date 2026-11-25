package mysql

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
)

// Fin de contrato (V0021): operaciones del asistente, contexto, alertas y recordatorios.

func contractDays(d []int) any {
	if len(d) == 0 {
		return nil
	}
	b, _ := json.Marshal(d)
	return string(b)
}

func (r SelectionRepo) HireContractOp(ctx context.Context, m ports.Meta, tenantID uint64, in ports.ContractOpWrite) (string, string, error) {
	var rqs any
	if len(in.RQIDs) > 0 {
		b, _ := json.Marshal(in.RQIDs)
		rqs = string(b)
	}
	rs, err := r.call(ctx, "sp_hire_contract_op", m.ActorID, tenantID, in.RequestID, in.OpID, in.Type, nullStr(in.Date), nullStr(in.EndDate),
		in.Today, nullStr(in.TerminationReasonID), in.Recover, nullStr(in.ReturnDate), nullStr(in.AreaID), nullStr(in.Position),
		nullStr(in.Schedule), nullStr(in.SiteID), nullStr(in.Location), nullStr(in.Project), nullStr(in.CurrencyID), nullBytes(in.PayEnc),
		rqs, nullStr(in.Reason), nullJSON(in.Data), nullJSON(in.Changes), nullStr(m.IP))
	if err != nil {
		return "", "", err
	}
	defer rs.Close()
	var id, status string
	rs.Next()
	if err := rs.One(&id, &status); err != nil {
		return "", "", err
	}
	return id, status, rs.Close()
}

func (r SelectionRepo) HireContractOpChange(ctx context.Context, m ports.Meta, tenantID uint64, requestID, opID, action, date, reason, today string) (string, error) {
	rs, err := r.call(ctx, "sp_hire_contract_op_change", m.ActorID, tenantID, requestID, opID, action, nullStr(date), reason, today, nullStr(m.IP))
	if err != nil {
		return "", err
	}
	defer rs.Close()
	var status string
	rs.Next()
	if err := rs.One(&status); err != nil {
		return "", err
	}
	return status, rs.Close()
}

func (r SelectionRepo) ContractApplyScheduled(ctx context.Context, today time.Time) (int, error) {
	rs, err := r.call(ctx, "sp_contract_apply_scheduled", today.Format("2006-01-02"))
	if err != nil {
		return 0, err
	}
	defer rs.Close()
	var n int
	rs.Next()
	if err := rs.One(&n); err != nil {
		return 0, err
	}
	return n, rs.Close()
}

func (r SelectionRepo) HireContract(ctx context.Context, m ports.Meta, tenantID uint64, requestID string) (json.RawMessage, error) {
	return firstDoc(docRepo(r).rawDocs(ctx, "sp_hire_contract_get", m.ActorID, tenantID, requestID))
}

func (r SelectionRepo) HireContractOpGet(ctx context.Context, m ports.Meta, tenantID uint64, requestID, opID string) (json.RawMessage, error) {
	return firstDoc(docRepo(r).rawDocs(ctx, "sp_hire_contract_op_get", m.ActorID, tenantID, requestID, opID))
}

func (r SelectionRepo) HireContractAssertManager(ctx context.Context, m ports.Meta, tenantID uint64, requestID string) error {
	return r.exec(ctx, "sp_hire_contract_assert_manager", m.ActorID, tenantID, requestID)
}

func (r SelectionRepo) ContractAlerts(ctx context.Context, m ports.Meta, tenantID uint64, today time.Time, days int) ([]json.RawMessage, error) {
	return docRepo(r).rawDocs(ctx, "sp_contract_alerts", m.ActorID, tenantID, today.Format("2006-01-02"), days)
}

func (r SelectionRepo) ContractEnding(ctx context.Context, tenantID uint64, today time.Time, days int) (items, admins []json.RawMessage, err error) {
	rs, err := r.call(ctx, "sp_contract_ending", tenantID, today.Format("2006-01-02"), days)
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
	items, admins = []json.RawMessage{}, []json.RawMessage{}
	if rs.Next() {
		if items, err = read(); err != nil {
			return nil, nil, err
		}
	}
	if rs.Next() {
		if admins, err = read(); err != nil {
			return nil, nil, err
		}
	}
	return items, admins, rs.Close()
}

func (r SelectionRepo) HireOpDispatchAdd(ctx context.Context, actorID, tenantID uint64, requestID, opID, form, reason string, recipients []string, status, errText string) error {
	if recipients == nil {
		recipients = []string{}
	}
	b, _ := json.Marshal(recipients)
	return r.exec(ctx, "sp_hire_form_dispatch_add", actorID, tenantID, requestID, form, reason, string(b), status, nullStr(errText), nullStr(opID))
}
