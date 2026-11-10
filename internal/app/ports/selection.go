package ports

import (
	"context"
	"encoding/json"
	"time"
)

// RequirementWrite: último estado de un RQ de BDT (copia de solo lectura). Doc es el snapshot normalizado.
type RequirementWrite struct {
	ID, Code, Title, ClientID, ClientLabel, StatusCode, StatusLabel, StatusGroup string
	RequestDate, DueDate                                                         *string
	Vacancies, Covered, Candidates                                               int
	Doc                                                                          json.RawMessage
	UpdatedAt                                                                    time.Time
	Deleted                                                                      bool
}

// HireWrite: estado de un candidato confirmado en BDT. Los datos personales van cifrados con la llave de IGDT.
type HireWrite struct {
	NewID, CandidateID, RequirementID, TalentID    string
	Active                                         bool
	FirstName, LastName, SecondLastName, Headline  string
	DocTypeCode, DocTypeLabel, DocNumberBidx       string
	DocNumberEnc, EmailEnc, PhoneEnc               []byte
	ClientID, ClientLabel, ProfileID, ProfileLabel string
	Doc                                            json.RawMessage
	ConfirmedAt                                    time.Time
}

// HireReceived: resultado de recibir un candidato. Event: NEW | REOPENED | UPDATED | CANCELLED | NONE.
type HireReceived struct {
	ID, Event string
	Assigned  uint64
}

type SelectionRepository interface {
	HireRepository
	RequirementUpsert(ctx context.Context, tenantID uint64, r RequirementWrite) (bool, error)
	Requirements(ctx context.Context, m Meta, tenantID uint64, scope string) ([]json.RawMessage, error)
	Requirement(ctx context.Context, m Meta, tenantID uint64, id string) (json.RawMessage, error)
	HireReceive(ctx context.Context, tenantID, apiKeyID uint64, h HireWrite) (*HireReceived, error)
	Hires(ctx context.Context, m Meta, tenantID uint64, status *string) ([]json.RawMessage, error)
	Hire(ctx context.Context, m Meta, tenantID uint64, id string) (json.RawMessage, error)
	HireTake(ctx context.Context, m Meta, tenantID uint64, id string) error
}

// HireInput: registro del ingreso desde la solicitud. PayEnc: remuneración cifrada (JSON con los montos).
type HireInput struct {
	RequestID, HireID, EmployeeID, Position, ContractID, ReasonID, AreaID, SiteID string
	StartDate, EndDate, Schedule, ContractObject, ProjectService, Location, Notes string
	CurrencyID                                                                    string
	SunatDeclared, HasEquipment                                                   bool
	Equipment                                                                     []string
	EquipmentRequest, UserRequest                                                 json.RawMessage // FT-GS-03 y FT-GS-01 (nil = no aplica)
	PayEnc                                                                        []byte
}

// HireRepository: ingresos registrados, colaboradores y eventos para BDT.
type HireRepository interface {
	HireComplete(ctx context.Context, m Meta, tenantID uint64, in HireInput) error
	HireOf(ctx context.Context, m Meta, tenantID uint64, requestID string) (json.RawMessage, error)
	Employees(ctx context.Context, m Meta, tenantID uint64) ([]json.RawMessage, error)
	HireEvents(ctx context.Context, tenantID, since uint64, limit int) ([]CatalogChange, error)
	HireFormsRepository
}
