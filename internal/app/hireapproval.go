package app

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// Aprobación de ingresos por ficha (V0019): el gestor principal o secundario del cliente (o un administrador) edita y
// aprueba cada ficha por separado; al aprobarla se envía por correo a sus destinatarios.
//   - FMI (FT-GTH-12): registra el ingreso (colaborador, contrato y aviso HIRED a BDT). Aprobarla de nuevo es corregirla.
//   - USERS (FT-GS-01): creación de usuario y correo.
//   - EQUIPMENT (FT-GS-03): solo con la de usuarios aprobada.
// La solicitud queda completa cuando están aprobadas FMI, USERS y, si aplica, EQUIPMENT.

// ApprovalResult: estado de la solicitud tras aprobar una ficha.
type ApprovalResult struct {
	Form      string `json:"form"`
	Completed bool   `json:"completed"`
	HireID    string `json:"hireId,omitempty"`
}

// ApproveForm guarda las fichas como borrador (para no perder lo editado en las demás) y aprueba la indicada.
func (s *SelectionService) ApproveForm(ctx context.Context, p *domain.Principal, ip, requestID, form string, f HireForms) (*ApprovalResult, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	if !validUUID(requestID) || !contains(FormCodes, form) {
		return nil, domain.ErrValidation
	}
	if err := f.validate(false); err != nil {
		return nil, err
	}
	// el borrador conserva lo editado de todas las fichas (sin el motivo del cambio)
	draft := f
	draft.Reason = ""
	if err := s.SaveDraft(ctx, p, ip, requestID, draft); err != nil {
		return nil, err
	}
	res := &ApprovalResult{Form: form}
	switch form {
	case "FMI":
		hm, err := s.hireOf(ctx, p, tid, requestID)
		if err != nil {
			return nil, err
		}
		approved, err := s.approvedForms(ctx, p, tid, requestID)
		if err != nil {
			return nil, err
		}
		fmi := HireForms{Hire: f.Hire, Reason: f.Reason, Users: approved["USERS"].usersOf(), Equipment: approved["EQUIPMENT"].equipmentOf()}
		if hm == nil {
			id, err := s.registerHire(ctx, p, ip, tid, requestID, fmi)
			if err != nil {
				return nil, err
			}
			res.HireID = id
		} else if err := s.correctHire(ctx, p, ip, tid, requestID, fmi); err != nil {
			return nil, err
		}
	case "USERS", "EQUIPMENT":
		if err := f.validateSection(form); err != nil {
			return nil, err
		}
		data := HireForms{Hire: f.Hire}
		data.Hire.Pay = nil
		var need *bool
		if form == "USERS" {
			data.Users = f.Users
		} else {
			data.Equipment = f.Equipment
			yes := true
			need = &yes
		}
		raw, _ := json.Marshal(data)
		_, first, err := s.Repo.HireFormApprove(ctx, meta(p, ip), tid, requestID, form, raw, need)
		if err != nil {
			return nil, err
		}
		reason := "CONFIRMED"
		if !first {
			reason = "UPDATED"
		}
		s.dispatchAsync(p, tid, requestID, reason, []string{form})
	}
	st, err := s.requestStatus(ctx, p, tid, requestID)
	if err != nil {
		return nil, err
	}
	res.Completed = st == "COMPLETED"
	return res, nil
}

// validateSection: lo mínimo para aprobar la ficha de usuarios o la de equipo (los datos del ingreso que se imprimen en
// ella: cargo, área y fecha de inicio).
func (f *HireForms) validateSection(form string) error {
	h := f.Hire
	if strings.TrimSpace(h.Position) == "" || !validUUID(h.AreaID) || h.StartDate == "" {
		return domain.NewError("HIRE_DATA_REQUIRED")
	}
	switch form {
	case "USERS":
		if u := f.Users; u == nil || u.Username == "" || u.Email == "" {
			return domain.ErrValidation
		}
	case "EQUIPMENT":
		if e := f.Equipment; e == nil || e.RequestDate == "" || e.DeliveryDate == "" || len(e.Types) == 0 {
			return domain.ErrValidation
		}
	}
	return nil
}

// registerHire aprueba la FMI por primera vez: registra el ingreso con las fichas de usuarios y equipo ya aprobadas.
func (s *SelectionService) registerHire(ctx context.Context, p *domain.Principal, ip string, tid uint64, requestID string, f HireForms) (string, error) {
	strict := f
	strict.Users, strict.Equipment = nil, nil
	if err := strict.validate(true); err != nil {
		return "", err
	}
	if f.Hire.Pay == nil {
		if d, _, err := s.draftOf(ctx, p, tid, requestID, true); err == nil && d != nil {
			f.Hire.Pay = d.Hire.Pay
		}
	}
	if !validPay(f.Hire.Pay) {
		return "", domain.NewError("PAY_REQUIRED")
	}
	enc, err := s.encryptPay(f.Hire.Pay)
	if err != nil {
		return "", err
	}
	in := hireInput(requestID, &f, enc)
	in.HireID, in.EmployeeID = uuid.NewString(), uuid.NewString()
	if err := s.Repo.HireComplete(ctx, meta(p, ip), tid, in); err != nil {
		return "", err
	}
	s.dispatchAsync(p, tid, requestID, "CONFIRMED", []string{"FMI"})
	return in.HireID, nil
}

// correctHire vuelve a aprobar la FMI ya registrada: corrige el ingreso (motivo obligatorio si cambia la fecha de
// inicio), registra qué cambió y reenvía la FMI.
func (s *SelectionService) correctHire(ctx context.Context, p *domain.Principal, ip string, tid uint64, requestID string, f HireForms) error {
	strict := f
	strict.Users, strict.Equipment = nil, nil
	if err := strict.validate(true); err != nil {
		return err
	}
	old, err := s.formsOfHire(ctx, p, tid, requestID)
	if err != nil {
		return err
	}
	if old == nil {
		return domain.ErrNotFound
	}
	if f.Hire.Pay != nil && !validPay(f.Hire.Pay) {
		return domain.ErrValidation
	}
	if f.Hire.StartDate != old.Hire.StartDate && strings.TrimSpace(f.Reason) == "" {
		return domain.NewError("REASON_REQUIRED")
	}
	// la corrección de la FMI no toca las otras fichas: se comparan y guardan con lo aprobado de cada una
	f.Users, f.Equipment = old.Users, old.Equipment
	changes, err := s.diffForms(ctx, tid, old, &f)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	enc, err := s.encryptPay(f.Hire.Pay)
	if err != nil {
		return err
	}
	if err := s.Repo.HireUpdate(ctx, meta(p, ip), tid, ports.HireUpdate{HireInput: hireInput(requestID, &f, enc),
		ChangeReason: strings.TrimSpace(f.Reason), Changes: rawOrNil(changes)}); err != nil {
		return err
	}
	s.dispatchAsync(p, tid, requestID, "UPDATED", []string{"FMI"})
	return nil
}

// SetEquipmentRequired: el gestor decide si la solicitud lleva solicitud de equipo (no se puede quitar si ya se aprobó).
func (s *SelectionService) SetEquipmentRequired(ctx context.Context, p *domain.Principal, ip, requestID string, required bool) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if !validUUID(requestID) {
		return domain.ErrValidation
	}
	return s.Repo.HireEquipmentRequired(ctx, meta(p, ip), tid, requestID, required)
}

// approval: ficha aprobada con lo aprobado (Forms nil = aprobada antes de V0019: se imprime con el ingreso) y quién.
type approval struct {
	Form       string
	Forms      *HireForms
	SignerID   string
	SignerName string
}

func (a *approval) usersOf() *UserRequest {
	if a == nil || a.Forms == nil {
		return nil
	}
	return a.Forms.Users
}

func (a *approval) equipmentOf() *EquipmentRequest {
	if a == nil || a.Forms == nil {
		return nil
	}
	return a.Forms.Equipment
}

// approvedForms: aprobaciones de la solicitud por ficha.
func (s *SelectionService) approvedForms(ctx context.Context, p *domain.Principal, tid uint64, requestID string) (map[string]*approval, error) {
	docs, err := s.Repo.HireApprovals(ctx, meta(p, ""), tid, requestID)
	if err != nil {
		return nil, err
	}
	out := map[string]*approval{}
	for _, d := range docs {
		var x struct {
			Form       string          `json:"form"`
			Data       json.RawMessage `json:"data"`
			ApprovedBy *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"approvedBy"`
			UpdatedBy *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"updatedBy"`
		}
		if json.Unmarshal(decryptNames(s.Cipher, d), &x) != nil {
			continue
		}
		a := &approval{Form: x.Form}
		if len(x.Data) > 0 && string(x.Data) != "null" {
			var f HireForms
			var probe map[string]json.RawMessage
			if json.Unmarshal(x.Data, &probe) == nil && probe["hire"] != nil && json.Unmarshal(x.Data, &f) == nil {
				a.Forms = &f
			}
		}
		if x.UpdatedBy != nil && x.UpdatedBy.ID != "" {
			a.SignerID, a.SignerName = x.UpdatedBy.ID, x.UpdatedBy.Name
		} else if x.ApprovedBy != nil {
			a.SignerID, a.SignerName = x.ApprovedBy.ID, x.ApprovedBy.Name
		}
		out[x.Form] = a
	}
	return out, nil
}

// requestStatus: estado actual de la solicitud.
func (s *SelectionService) requestStatus(ctx context.Context, p *domain.Principal, tid uint64, requestID string) (string, error) {
	doc, err := s.Repo.Hire(ctx, meta(p, ""), tid, requestID)
	if err != nil {
		return "", err
	}
	var x struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(doc, &x)
	return x.Status, nil
}

// ---------------- Gestores por cliente ----------------

// ClientManagers: gestores principal y secundario de cada cliente.
func (s *SelectionService) ClientManagers(ctx context.Context, p *domain.Principal) ([]json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	docs, err := s.Repo.ClientManagers(ctx, meta(p, ""), tid)
	if err != nil {
		return nil, err
	}
	for i := range docs {
		docs[i] = decryptNames(s.Cipher, docs[i])
	}
	return docs, nil
}

// SetClientManagers asigna los gestores de un cliente ("" = sin gestor en ese rol).
func (s *SelectionService) SetClientManagers(ctx context.Context, p *domain.Principal, ip, clientID, primaryID, secondaryID string) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if !validUUID(clientID) || !optUUID(primaryID) || !optUUID(secondaryID) || (primaryID != "" && primaryID == secondaryID) {
		return domain.ErrValidation
	}
	if primaryID == "" && secondaryID != "" { // el secundario solo pasa a principal
		primaryID, secondaryID = secondaryID, ""
	}
	return s.Repo.ClientManagersSet(ctx, meta(p, ip), tid, clientID, primaryID, secondaryID)
}

// ClientManagerCandidates: usuarios que pueden ser gestores de un cliente.
func (s *SelectionService) ClientManagerCandidates(ctx context.Context, p *domain.Principal) ([]json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	docs, err := s.Repo.ClientManagerCandidates(ctx, meta(p, ""), tid)
	if err != nil {
		return nil, err
	}
	for i := range docs {
		docs[i] = decryptNames(s.Cipher, docs[i])
	}
	return docs, nil
}

// ---------------- Alertas de solicitudes pendientes ----------------

// DueSoonDays: una solicitud pendiente está por vencer si faltan estos días o menos para la fecha de inicio.
const DueSoonDays = 3

// dueState: OVERDUE (ya pasó el inicio), DUE_SOON (faltan DueSoonDays días o menos) u OK.
func dueState(startDate string, today time.Time) string {
	d, err := time.ParseInLocation("2006-01-02", dateOnly(startDate), today.Location())
	if err != nil {
		return "OK"
	}
	t := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	switch days := int(d.Sub(t).Hours() / 24); {
	case days < 0:
		return "OVERDUE"
	case days <= DueSoonDays:
		return "DUE_SOON"
	}
	return "OK"
}

// HireAlertItem: solicitud pendiente que el usuario puede atender.
type HireAlertItem struct {
	ID         string   `json:"id"`
	Talent     string   `json:"talent"`
	Client     string   `json:"client"`
	RQ         string   `json:"rq"`
	StartDate  string   `json:"startDate,omitempty"`
	Due        string   `json:"due"`
	Pending    []string `json:"pending"` // fichas por aprobar
	ReceivedAt string   `json:"receivedAt"`
}

// HireAlerts: resumen de lo que el usuario tiene pendiente (para la alerta destacada y el aviso diario).
type HireAlerts struct {
	Pending int             `json:"pending"`
	DueSoon int             `json:"dueSoon"`
	Overdue int             `json:"overdue"`
	Items   []HireAlertItem `json:"items"`
}

// pendingForms: fichas por aprobar según lo aprobado y si lleva solicitud de equipo.
func pendingForms(approved []string, equipmentRequired bool) []string {
	out := []string{}
	for _, f := range []string{"FMI", "USERS", "EQUIPMENT"} {
		if f == "EQUIPMENT" && !equipmentRequired {
			continue
		}
		if !contains(approved, f) {
			out = append(out, f)
		}
	}
	return out
}

// Alerts: solicitudes pendientes que el usuario puede atender, con las vencidas y por vencer primero.
func (s *SelectionService) Alerts(ctx context.Context, p *domain.Principal) (*HireAlerts, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	docs, err := s.Repo.Hires(ctx, meta(p, ""), tid, nil)
	if err != nil {
		return nil, err
	}
	today := time.Now().In(limaTZ())
	out := &HireAlerts{Items: []HireAlertItem{}}
	for _, d := range docs {
		var x struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			CanAct      bool   `json:"canAct"`
			StartDate   string `json:"startDate"`
			ReceivedAt  string `json:"receivedAt"`
			EquipReq    bool   `json:"equipmentRequired"`
			Requirement struct {
				Code string `json:"code"`
			} `json:"requirement"`
			Client struct {
				Label string `json:"label"`
			} `json:"client"`
			Talent struct {
				FirstName      string `json:"firstName"`
				LastName       string `json:"lastName"`
				SecondLastName string `json:"secondLastName"`
			} `json:"talent"`
			Approvals []struct {
				Form string `json:"form"`
			} `json:"approvals"`
		}
		if json.Unmarshal(d, &x) != nil || !x.CanAct || (x.Status != "PENDING" && x.Status != "IN_PROGRESS") {
			continue
		}
		approved := []string{}
		for _, a := range x.Approvals {
			approved = append(approved, a.Form)
		}
		it := HireAlertItem{ID: x.ID, Talent: strings.Join(nonEmpty([]string{x.Talent.FirstName, x.Talent.LastName, x.Talent.SecondLastName}), " "),
			Client: x.Client.Label, RQ: x.Requirement.Code, StartDate: dateOnly(x.StartDate), Due: dueState(x.StartDate, today),
			Pending: pendingForms(approved, x.EquipReq), ReceivedAt: x.ReceivedAt}
		out.Pending++
		switch it.Due {
		case "OVERDUE":
			out.Overdue++
		case "DUE_SOON":
			out.DueSoon++
		}
		out.Items = append(out.Items, it)
	}
	rank := map[string]int{"OVERDUE": 0, "DUE_SOON": 1, "OK": 2}
	sort.SliceStable(out.Items, func(i, j int) bool {
		if rank[out.Items[i].Due] != rank[out.Items[j].Due] {
			return rank[out.Items[i].Due] < rank[out.Items[j].Due]
		}
		return out.Items[i].StartDate < out.Items[j].StartDate
	})
	return out, nil
}
