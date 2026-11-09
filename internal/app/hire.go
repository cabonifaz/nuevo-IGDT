package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"strings"
	"time"

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

// HireForm: datos del ingreso que completa el gestor de servicios.
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
	Equipment      []string `json:"equipment"`
	CurrencyID     string   `json:"currencyId"`
	Pay            Pay      `json:"pay"`
	Notes          string   `json:"notes"`
}

func validAmount(v float64) bool {
	return v >= 0 && v <= 10_000_000 && !math.IsNaN(v) && math.Round(v*100) == v*100
}

// CompleteHire registra el ingreso: la solicitud pasa a completada, el talento queda como colaborador y BDT se entera
// (evento HIRED) para marcar el ingreso del candidato y cerrar el RQ si ya cubrió sus vacantes.
func (s *SelectionService) CompleteHire(ctx context.Context, p *domain.Principal, ip, requestID string, f HireForm) (string, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return "", err
	}
	f.Position, f.Schedule = strings.TrimSpace(f.Position), strings.TrimSpace(f.Schedule)
	start, errStart := time.Parse("2006-01-02", f.StartDate)
	if !validUUID(requestID) || !validUUID(f.ContractID) || !validUUID(f.ReasonID) || !validUUID(f.AreaID) || !validUUID(f.CurrencyID) ||
		!optUUID(f.SiteID) || errStart != nil || f.Position == "" || f.Schedule == "" || !maxLen(150, f.Position) ||
		!maxLen(200, f.Schedule, f.ProjectService, f.Location) || !maxLen(1000, f.ContractObject, f.Notes) || len(f.Equipment) > 20 {
		return "", domain.ErrValidation
	}
	if f.EndDate != "" {
		end, err := time.Parse("2006-01-02", f.EndDate)
		if err != nil || end.Before(start) {
			return "", domain.ErrValidation
		}
	}
	for _, e := range f.Equipment {
		if !validUUID(e) {
			return "", domain.ErrValidation
		}
	}
	pay := f.Pay
	for _, v := range []float64{pay.Base, pay.Mobility, pay.Quarterly, pay.Semiannual, pay.Fee} {
		if !validAmount(v) {
			return "", domain.ErrValidation
		}
	}
	if pay.Base <= 0 && pay.Fee <= 0 {
		return "", domain.ErrValidation
	}
	raw, _ := json.Marshal(pay)
	enc, err := s.Cipher.Encrypt(string(raw))
	if err != nil {
		return "", err
	}
	if f.HasEquipment {
		f.Equipment = nil
	}
	id := uuid.NewString()
	err = s.Repo.HireComplete(ctx, meta(p, ip), tid, ports.HireInput{RequestID: requestID, HireID: id, EmployeeID: uuid.NewString(),
		Position: f.Position, ContractID: f.ContractID, ReasonID: f.ReasonID, AreaID: f.AreaID, SiteID: f.SiteID, StartDate: f.StartDate,
		EndDate: f.EndDate, Schedule: f.Schedule, ContractObject: f.ContractObject, ProjectService: f.ProjectService, Location: f.Location,
		Notes: f.Notes, CurrencyID: f.CurrencyID, SunatDeclared: f.SunatDeclared, HasEquipment: f.HasEquipment, Equipment: f.Equipment, PayEnc: enc})
	if err != nil {
		return "", err
	}
	return id, nil
}

// hireOf: ingreso registrado de la solicitud, con la remuneración descifrada solo si el usuario puede verla.
func (s *SelectionService) hireOf(ctx context.Context, p *domain.Principal, tid uint64, requestID string) (any, error) {
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
	if p.Has("employee.salary.read") && enc != "" {
		if b, err := base64.StdEncoding.DecodeString(enc); err == nil {
			if plain, err := s.Cipher.Decrypt(b); err == nil {
				var pay Pay
				if json.Unmarshal([]byte(plain), &pay) == nil {
					m["pay"] = pay
				}
			}
		}
	}
	return m, nil
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
