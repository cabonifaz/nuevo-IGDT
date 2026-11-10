package mysql

import (
	"context"
	"encoding/json"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// Fichas de ingreso (borrador, corrección, revocación, configuración y envíos) y enlace de lectura con BDT.

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return string(b)
}

func (r SelectionRepo) HireDraftSave(ctx context.Context, m ports.Meta, tenantID uint64, requestID string, draft json.RawMessage, payEnc []byte) error {
	return r.exec(ctx, "sp_hire_draft_save", m.ActorID, tenantID, requestID, string(draft), nullBytes(payEnc), nullStr(m.IP))
}

func (r SelectionRepo) HireDraft(ctx context.Context, m ports.Meta, tenantID uint64, requestID string) (json.RawMessage, error) {
	list, err := docRepo(r).rawDocs(ctx, "sp_hire_draft_get", m.ActorID, tenantID, requestID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return list[0], nil
}

func (r SelectionRepo) HireUpdate(ctx context.Context, m ports.Meta, tenantID uint64, in ports.HireUpdate) error {
	var eq any
	if len(in.Equipment) > 0 {
		b, _ := json.Marshal(in.Equipment)
		eq = string(b)
	}
	return r.exec(ctx, "sp_hire_update", m.ActorID, tenantID, in.RequestID, in.Position, in.ContractID, in.ReasonID, in.AreaID,
		nullStr(in.SiteID), in.StartDate, nullStr(in.EndDate), in.Schedule, nullStr(in.ContractObject), nullStr(in.ProjectService),
		nullStr(in.Location), in.SunatDeclared, in.HasEquipment, eq, nullJSON(in.EquipmentRequest), nullJSON(in.UserRequest),
		in.CurrencyID, nullBytes(in.PayEnc), nullStr(in.Notes), nullStr(in.ChangeReason), nullJSON(in.Changes), nullStr(m.IP))
}

func (r SelectionRepo) HireRevoke(ctx context.Context, m ports.Meta, tenantID uint64, requestID, reason string) error {
	return r.exec(ctx, "sp_hire_revoke", m.ActorID, tenantID, requestID, reason, nullStr(m.IP))
}

func (r SelectionRepo) HireFormSetting(ctx context.Context, tenantID uint64) (json.RawMessage, error) {
	return firstDoc(docRepo(r).rawDocs(ctx, "sp_hire_form_setting_get", tenantID))
}

func (r SelectionRepo) HireFormSettingSave(ctx context.Context, m ports.Meta, tenantID uint64, recipients json.RawMessage, ccManager bool, graceDays int, dom string) error {
	return r.exec(ctx, "sp_hire_form_setting_save", m.ActorID, tenantID, string(recipients), ccManager, graceDays, nullStr(dom), nullStr(m.IP))
}

func (r SelectionRepo) HireFormStyle(ctx context.Context, tenantID uint64) (json.RawMessage, error) {
	return firstDoc(docRepo(r).rawDocs(ctx, "sp_hire_form_style_get", tenantID))
}

func (r SelectionRepo) HireFormStyleSave(ctx context.Context, m ports.Meta, tenantID uint64, style json.RawMessage, clearLogo bool) error {
	return r.exec(ctx, "sp_hire_form_style_save", m.ActorID, tenantID, nullJSON(style), clearLogo, nullStr(m.IP))
}

func (r SelectionRepo) HireFormLogoSet(ctx context.Context, m ports.Meta, tenantID uint64, fileUUID string) error {
	return r.exec(ctx, "sp_hire_form_logo_set", m.ActorID, tenantID, fileUUID, nullStr(m.IP))
}

func (r SelectionRepo) HireFormDispatchAdd(ctx context.Context, actorID, tenantID uint64, requestID, form, reason string, recipients []string, status, errText string) error {
	if recipients == nil {
		recipients = []string{}
	}
	b, _ := json.Marshal(recipients)
	return r.exec(ctx, "sp_hire_form_dispatch_add", actorID, tenantID, requestID, form, reason, string(b), status, nullStr(errText))
}

func (r SelectionRepo) BDTLinkSet(ctx context.Context, tenantID, apiKeyID uint64, keyEnc []byte) error {
	return r.exec(ctx, "sp_bdt_link_set", tenantID, apiKeyID, keyEnc)
}

func (r SelectionRepo) BDTLinkClear(ctx context.Context, tenantID, apiKeyID uint64) error {
	return r.exec(ctx, "sp_bdt_link_clear", tenantID, apiKeyID)
}

func (r SelectionRepo) BDTLinkGet(ctx context.Context, tenantID uint64) ([]byte, error) {
	rs, err := r.call(ctx, "sp_bdt_link_get", tenantID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var enc []byte
	rs.Next()
	if err := rs.One(&enc); err != nil {
		if err == domain.ErrNotFound {
			return nil, rs.Close()
		}
		return nil, err
	}
	return enc, rs.Close()
}

func (r SelectionRepo) CatalogLabels(ctx context.Context, tenantID uint64, ids []string) (map[string]ports.CatalogLabel, error) {
	out := map[string]ports.CatalogLabel{}
	if len(ids) == 0 {
		return out, nil
	}
	b, _ := json.Marshal(ids)
	rs, err := r.call(ctx, "sp_catalog_labels", tenantID, string(b))
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	if !rs.Next() {
		return out, rs.Close()
	}
	err = rs.Each(func(scan func(...any) error) error {
		var id, code, label string
		var str1 *string
		if err := scan(&id, &code, &label, &str1); err != nil {
			return err
		}
		l := ports.CatalogLabel{Code: code, Label: label}
		if str1 != nil {
			l.Str1 = *str1
		}
		out[id] = l
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, rs.Close()
}
