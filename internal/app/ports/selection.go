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
	RequirementUpsert(ctx context.Context, tenantID uint64, r RequirementWrite) (bool, error)
	Requirements(ctx context.Context, m Meta, tenantID uint64, scope string) ([]json.RawMessage, error)
	Requirement(ctx context.Context, m Meta, tenantID uint64, id string) (json.RawMessage, error)
	HireReceive(ctx context.Context, tenantID, apiKeyID uint64, h HireWrite) (*HireReceived, error)
	Hires(ctx context.Context, m Meta, tenantID uint64, status *string) ([]json.RawMessage, error)
	Hire(ctx context.Context, m Meta, tenantID uint64, id string) (json.RawMessage, error)
	HireTake(ctx context.Context, m Meta, tenantID uint64, id string) error
}
