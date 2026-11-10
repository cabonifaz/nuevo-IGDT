package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/mail"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// Pay: remuneración del ingreso. Planilla: base, movilidad, trimestral y semestral; recibo por honorarios: honorario.
// Se guarda cifrada y solo la ve quien tiene employee.salary.read.
type Pay struct {
	Base       float64 `json:"base,omitempty"`
	Mobility   float64 `json:"mobility,omitempty"`
	Quarterly  float64 `json:"quarterly,omitempty"`
	Semiannual float64 `json:"semiannual,omitempty"`
	Fee        float64 `json:"fee,omitempty"`
}

// HireForm: ficha de ingreso (FT-GTH-12) que completa el gestor de servicios. Pay nil = conservar la ya guardada.
type HireForm struct {
	Position       string   `json:"position"`
	ContractID     string   `json:"contractId"`
	ReasonID       string   `json:"reasonId"`
	AreaID         string   `json:"areaId"`
	SiteID         string   `json:"siteId"`
	StartDate      string   `json:"startDate"`
	EndDate        string   `json:"endDate"`
	Schedule       string   `json:"schedule"`
	ContractObject string   `json:"contractObject"`
	ProjectService string   `json:"projectService"`
	Location       string   `json:"location"`
	SunatDeclared  bool     `json:"sunatDeclared"`
	HasEquipment   bool     `json:"hasEquipment"`
	Equipment      []string `json:"equipment,omitempty"` // compatibilidad: tipos de equipo (ahora en la solicitud de equipo)
	CurrencyID     string   `json:"currencyId"`
	Pay            *Pay     `json:"pay,omitempty"`
	Notes          string   `json:"notes"`
}

// SoftwareItem: programa a instalar (FT-GS-03).
type SoftwareItem struct {
	Product string `json:"product"`
	Version string `json:"version"`
}

// EquipmentRequest: solicitud de hardware y software (FT-GS-03).
type EquipmentRequest struct {
	RequestDate    string         `json:"requestDate"`
	DeliveryDate   string         `json:"deliveryDate"`
	Types          []string       `json:"types"` // EQUIPMENT_TYPE
	Processor      string         `json:"processor"`
	RAM            string         `json:"ram"`
	Disk           string         `json:"disk"`
	Brand          string         `json:"brand"`
	Annex          string         `json:"annex"` // FIXED | SOFTPHONE | ""
	Mobile         bool           `json:"mobile"`
	MobileInternet bool           `json:"mobileInternet"`
	Accessories    string         `json:"accessories"`
	Software       []SoftwareItem `json:"software"`
	Notes          string         `json:"notes"`
}

// UserRequest: creación de usuario de red y correo (FT-GS-01).
type UserRequest struct {
	Username       string `json:"username"`
	Email          string `json:"email"`
	Notes          string `json:"notes"`
	RequesterArea  string `json:"requesterArea,omitempty"`  // área del solicitante (por defecto la del ingreso)
	RequesterAnnex string `json:"requesterAnnex,omitempty"` // anexo del solicitante (por defecto el del formato)
}

// HireForms: las tres fichas de la solicitud. Equipment/Users nil = esa ficha no aplica.
// Reason: motivo del cambio al corregir un ingreso confirmado (obligatorio si cambia la fecha de inicio).
type HireForms struct {
	Hire      HireForm          `json:"hire"`
	Equipment *EquipmentRequest `json:"equipment"`
	Users     *UserRequest      `json:"users"`
	Reason    string            `json:"reason,omitempty"`
}

func validAmount(v float64) bool {
	return v >= 0 && v <= 10_000_000 && !math.IsNaN(v) && math.Round(v*100) == v*100
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{2,60}$`)

// validate revisa las fichas. strict: lo exigido para confirmar; si no, solo formatos y largos (borrador).
func (f *HireForms) validate(strict bool) error {
	h := &f.Hire
	h.Position, h.Schedule = strings.TrimSpace(h.Position), strings.TrimSpace(h.Schedule)
	bad := !optUUID(h.ContractID) || !optUUID(h.ReasonID) || !optUUID(h.AreaID) || !optUUID(h.SiteID) || !optUUID(h.CurrencyID) ||
		(h.StartDate != "" && !validDate(h.StartDate)) || (h.EndDate != "" && !validDate(h.EndDate)) ||
		!maxLen(150, h.Position) || !maxLen(200, h.Schedule, h.ProjectService, h.Location) || !maxLen(1000, h.ContractObject, h.Notes) ||
		!maxLen(500, f.Reason)
	if h.StartDate != "" && h.EndDate != "" && h.EndDate < h.StartDate {
		bad = true
	}
	if h.Pay != nil {
		for _, v := range []float64{h.Pay.Base, h.Pay.Mobility, h.Pay.Quarterly, h.Pay.Semiannual, h.Pay.Fee} {
			if !validAmount(v) {
				bad = true
			}
		}
	}
	if e := f.Equipment; e != nil {
		bad = bad || (e.RequestDate != "" && !validDate(e.RequestDate)) || (e.DeliveryDate != "" && !validDate(e.DeliveryDate)) ||
			len(e.Types) > 20 || len(e.Software) > 40 || !maxLen(120, e.Processor, e.RAM, e.Disk, e.Brand) ||
			!maxLen(300, e.Accessories) || !maxLen(1000, e.Notes) || (e.Annex != "" && e.Annex != "FIXED" && e.Annex != "SOFTPHONE")
		for _, t := range e.Types {
			bad = bad || !validUUID(t)
		}
		for i := range e.Software {
			e.Software[i].Product, e.Software[i].Version = strings.TrimSpace(e.Software[i].Product), strings.TrimSpace(e.Software[i].Version)
			bad = bad || !maxLen(120, e.Software[i].Product, e.Software[i].Version)
		}
	}
	if u := f.Users; u != nil {
		u.Username, u.Email = strings.TrimSpace(u.Username), strings.TrimSpace(u.Email)
		bad = bad || !maxLen(1000, u.Notes) || !maxLen(80, u.RequesterArea) || !maxLen(20, u.RequesterAnnex) || (u.Username != "" && !usernameRe.MatchString(u.Username)) || len(u.Email) > 150
		if u.Email != "" {
			if a, err := mail.ParseAddress(u.Email); err != nil || a.Address != u.Email {
				bad = true
			}
		}
	}
	if bad {
		return domain.ErrValidation
	}
	if !strict {
		return nil
	}
	if h.Position == "" || h.Schedule == "" || !validUUID(h.ContractID) || !validUUID(h.ReasonID) || !validUUID(h.AreaID) ||
		!validUUID(h.CurrencyID) || h.StartDate == "" {
		return domain.ErrValidation
	}
	if e := f.Equipment; e != nil && (e.RequestDate == "" || e.DeliveryDate == "" || len(e.Types) == 0) {
		return domain.ErrValidation
	}
	if u := f.Users; u != nil && (u.Username == "" || u.Email == "") {
		return domain.ErrValidation
	}
	return nil
}

func validPay(p *Pay) bool { return p != nil && (p.Base > 0 || p.Fee > 0) }

// encryptPay devuelve la remuneración cifrada (nil si no se envió).
func (s *SelectionService) encryptPay(p *Pay) ([]byte, error) {
	if p == nil {
		return nil, nil
	}
	raw, _ := json.Marshal(p)
	return s.Cipher.Encrypt(string(raw))
}

func (s *SelectionService) decryptPay(enc string) *Pay {
	if enc == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil
	}
	plain, err := s.Cipher.Decrypt(b)
	if err != nil {
		return nil
	}
	var pay Pay
	if json.Unmarshal([]byte(plain), &pay) != nil {
		return nil
	}
	return &pay
}

func rawOrNil(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

// hireInput arma el registro para el repositorio a partir de las fichas.
func hireInput(requestID string, f *HireForms, payEnc []byte) ports.HireInput {
	h := f.Hire
	in := ports.HireInput{RequestID: requestID, Position: h.Position, ContractID: h.ContractID, ReasonID: h.ReasonID, AreaID: h.AreaID,
		SiteID: h.SiteID, StartDate: h.StartDate, EndDate: h.EndDate, Schedule: h.Schedule, ContractObject: h.ContractObject,
		ProjectService: h.ProjectService, Location: h.Location, Notes: h.Notes, CurrencyID: h.CurrencyID, SunatDeclared: h.SunatDeclared,
		HasEquipment: h.HasEquipment, PayEnc: payEnc}
	if f.Equipment != nil {
		in.Equipment = f.Equipment.Types
		in.EquipmentRequest = rawOrNil(f.Equipment)
	}
	if f.Users != nil {
		in.UserRequest = rawOrNil(f.Users)
	}
	return in
}

// SaveDraft guarda las fichas como borrador (la solicitud queda en curso a cargo del gestor).
func (s *SelectionService) SaveDraft(ctx context.Context, p *domain.Principal, ip, requestID string, f HireForms) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if !validUUID(requestID) {
		return domain.ErrValidation
	}
	if err := f.validate(false); err != nil {
		return err
	}
	enc, err := s.encryptPay(f.Hire.Pay)
	if err != nil {
		return err
	}
	f.Hire.Pay, f.Reason = nil, ""
	raw, _ := json.Marshal(f)
	return s.Repo.HireDraftSave(ctx, meta(p, ip), tid, requestID, raw, enc)
}

// draftOf: borrador guardado con la remuneración (descifrada solo para uso interno o si el usuario puede verla).
func (s *SelectionService) draftOf(ctx context.Context, p *domain.Principal, tid uint64, requestID string, withPay bool) (*HireForms, map[string]any, error) {
	doc, err := s.Repo.HireDraft(ctx, meta(p, ""), tid, requestID)
	if err != nil || doc == nil {
		return nil, nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(decryptNames(s.Cipher, doc), &m); err != nil {
		return nil, nil, err
	}
	var f HireForms
	if b, err := json.Marshal(m["forms"]); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	enc, _ := m["payEnc"].(string)
	delete(m, "payEnc")
	if pay := s.decryptPay(enc); pay != nil {
		if withPay {
			f.Hire.Pay = pay
		}
		m["hasPay"] = true
	}
	return &f, m, nil
}

// CompleteHire confirma las fichas: registra el ingreso, el talento queda como colaborador, BDT se entera (HIRED) y
// cada ficha se envía a sus destinatarios.
func (s *SelectionService) CompleteHire(ctx context.Context, p *domain.Principal, ip, requestID string, f HireForms) (string, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return "", err
	}
	if !validUUID(requestID) {
		return "", domain.ErrValidation
	}
	if err := f.validate(true); err != nil {
		return "", err
	}
	if f.Hire.Pay == nil { // remuneración del borrador (la pudo ingresar otro gestor)
		if d, _, err := s.draftOf(ctx, p, tid, requestID, true); err == nil && d != nil {
			f.Hire.Pay = d.Hire.Pay
		}
	}
	if !validPay(f.Hire.Pay) {
		return "", domain.ErrValidation
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
	s.dispatchAsync(p, tid, requestID, "CONFIRMED", nil)
	return in.HireID, nil
}

// UpdateHire corrige el ingreso confirmado (hasta la fecha de inicio más los días de gracia). Registra qué cambió,
// avisa a BDT si cambió la fecha de inicio y reenvía las fichas actualizadas.
func (s *SelectionService) UpdateHire(ctx context.Context, p *domain.Principal, ip, requestID string, f HireForms) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if !validUUID(requestID) {
		return domain.ErrValidation
	}
	if err := f.validate(true); err != nil {
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
	s.dispatchAsync(p, tid, requestID, "UPDATED", nil)
	return nil
}

// RevokeHire: el cliente anuló el ingreso. BDT lo sabe (REVOKED) y los destinatarios de las fichas reciben el aviso.
func (s *SelectionService) RevokeHire(ctx context.Context, p *domain.Principal, ip, requestID, reason string) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if !validUUID(requestID) || !maxLen(500, reason) {
		return domain.ErrValidation
	}
	if reason == "" {
		return domain.NewError("REASON_REQUIRED")
	}
	if err := s.Repo.HireRevoke(ctx, meta(p, ip), tid, requestID, reason); err != nil {
		return err
	}
	s.dispatchAsync(p, tid, requestID, "REVOKED", nil)
	return nil
}

// ResendForms reenvía las fichas confirmadas (todas o las indicadas).
func (s *SelectionService) ResendForms(ctx context.Context, p *domain.Principal, requestID string, forms []string) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if !validUUID(requestID) {
		return domain.ErrValidation
	}
	for _, f := range forms {
		if !contains(FormCodes, f) {
			return domain.ErrValidation
		}
	}
	if err := s.Repo.HireAssertAssignee(ctx, meta(p, ""), tid, requestID, "COMPLETED"); err != nil {
		return err
	}
	h, err := s.formsOfHire(ctx, p, tid, requestID)
	if err != nil {
		return err
	}
	if h == nil {
		return domain.NewError("HIRE_STATE")
	}
	s.dispatchAsync(p, tid, requestID, "RESENT", forms)
	return nil
}

// hireOf: ingreso registrado de la solicitud, con la remuneración descifrada solo si el usuario puede verla.
func (s *SelectionService) hireOf(ctx context.Context, p *domain.Principal, tid uint64, requestID string) (map[string]any, error) {
	doc, err := s.Repo.HireOf(ctx, meta(p, ""), tid, requestID)
	if err != nil || doc == nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(decryptNames(s.Cipher, doc), &m); err != nil {
		return nil, err
	}
	enc, _ := m["payEnc"].(string)
	delete(m, "payEnc")
	if p.Has("employee.salary.read") {
		if pay := s.decryptPay(enc); pay != nil {
			m["pay"] = pay
		}
	}
	m["payEnc"] = enc // se quita antes de responder (lo usa formsOfHire)
	return m, nil
}

func str(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	switch v := cur.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprint(v)
	}
	return ""
}

// formsOfHire reconstruye las fichas del ingreso confirmado (con la remuneración, para uso interno).
func (s *SelectionService) formsOfHire(ctx context.Context, p *domain.Principal, tid uint64, requestID string) (*HireForms, error) {
	m, err := s.hireOf(ctx, p, tid, requestID)
	if err != nil || m == nil {
		return nil, err
	}
	return s.formsFromHire(m), nil
}

// formsFromHire: las fichas del ingreso confirmado (documento de sp_hire_of_request).
func (s *SelectionService) formsFromHire(m map[string]any) *HireForms {
	f := &HireForms{Hire: HireForm{Position: str(m, "position"), ContractID: str(m, "contract", "id"), ReasonID: str(m, "reason", "id"),
		AreaID: str(m, "area", "id"), SiteID: str(m, "site", "id"), StartDate: dateOnly(str(m, "startDate")), EndDate: dateOnly(str(m, "endDate")),
		Schedule: str(m, "schedule"), ContractObject: str(m, "contractObject"), ProjectService: str(m, "projectService"),
		Location: str(m, "location"), CurrencyID: str(m, "currency", "id"), Notes: str(m, "notes")}}
	f.Hire.SunatDeclared, _ = m["sunatDeclared"].(bool)
	f.Hire.HasEquipment, _ = m["hasEquipment"].(bool)
	f.Hire.Pay = s.decryptPay(str(m, "payEnc"))
	if v, ok := m["equipmentRequest"]; ok && v != nil {
		var e EquipmentRequest
		if b, err := json.Marshal(v); err == nil && json.Unmarshal(b, &e) == nil {
			f.Equipment = &e
		}
	} else if ids, ok := m["equipmentIds"].([]any); ok && len(ids) > 0 { // ingresos anteriores a las fichas
		e := EquipmentRequest{}
		for _, id := range ids {
			if s, ok := id.(string); ok {
				e.Types = append(e.Types, s)
			}
		}
		f.Equipment = &e
	}
	if v, ok := m["userRequest"]; ok && v != nil {
		var u UserRequest
		if b, err := json.Marshal(v); err == nil && json.Unmarshal(b, &u) == nil {
			f.Users = &u
		}
	}
	return f
}

func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// FieldChange: un campo corregido del ingreso (la remuneración solo se marca como cambiada).
type FieldChange struct {
	Field string `json:"field"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
}

func (s *SelectionService) diffForms(ctx context.Context, tid uint64, a, b *HireForms) ([]FieldChange, error) {
	ids := []string{a.Hire.ContractID, b.Hire.ContractID, a.Hire.ReasonID, b.Hire.ReasonID, a.Hire.AreaID, b.Hire.AreaID,
		a.Hire.SiteID, b.Hire.SiteID, a.Hire.CurrencyID, b.Hire.CurrencyID}
	labels, err := s.Repo.CatalogLabels(ctx, tid, nonEmpty(ids))
	if err != nil {
		return nil, err
	}
	lab := func(id string) string {
		if l, ok := labels[id]; ok {
			return l.Label
		}
		return ""
	}
	yes := func(v bool) string {
		if v {
			return "Sí"
		}
		return "No"
	}
	out := []FieldChange{}
	add := func(field, from, to string) {
		if from != to {
			out = append(out, FieldChange{field, from, to})
		}
	}
	ah, bh := a.Hire, b.Hire
	add("position", ah.Position, bh.Position)
	add("contract", lab(ah.ContractID), lab(bh.ContractID))
	add("reason", lab(ah.ReasonID), lab(bh.ReasonID))
	add("area", lab(ah.AreaID), lab(bh.AreaID))
	add("site", lab(ah.SiteID), lab(bh.SiteID))
	add("startDate", ah.StartDate, bh.StartDate)
	add("endDate", ah.EndDate, bh.EndDate)
	add("schedule", ah.Schedule, bh.Schedule)
	add("contractObject", ah.ContractObject, bh.ContractObject)
	add("projectService", ah.ProjectService, bh.ProjectService)
	add("location", ah.Location, bh.Location)
	add("sunatDeclared", yes(ah.SunatDeclared), yes(bh.SunatDeclared))
	add("hasEquipment", yes(ah.HasEquipment), yes(bh.HasEquipment))
	add("currency", labels[ah.CurrencyID].Code, labels[bh.CurrencyID].Code)
	add("notes", ah.Notes, bh.Notes)
	if bh.Pay != nil && !samePay(ah.Pay, bh.Pay) {
		out = append(out, FieldChange{Field: "pay"})
	}
	if string(rawOrNil(a.Equipment)) != string(rawOrNil(b.Equipment)) {
		out = append(out, FieldChange{Field: "equipmentRequest"})
	}
	if string(rawOrNil(a.Users)) != string(rawOrNil(b.Users)) {
		out = append(out, FieldChange{Field: "userRequest"})
	}
	return out, nil
}

func samePay(a, b *Pay) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func nonEmpty(list []string) []string {
	out := []string{}
	for _, v := range list {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (s *SelectionService) Employees(ctx context.Context, p *domain.Principal) ([]json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	return s.Repo.Employees(ctx, meta(p, ""), tid)
}

// HireEvents: eventos de ingreso para BDT desde el cursor (API de integración, hires.read).
func (s *SelectionService) HireEvents(ctx context.Context, tenantID, since uint64, limit int) (*CatalogChanges, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	list, err := s.Repo.HireEvents(ctx, tenantID, since, limit)
	if err != nil {
		return nil, err
	}
	out := &CatalogChanges{Changes: make([]json.RawMessage, 0, len(list)), Next: since, More: len(list) == limit}
	for _, c := range list {
		out.Changes = append(out.Changes, c.Doc)
		out.Next = c.Cursor
	}
	return out, nil
}
