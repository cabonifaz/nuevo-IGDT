package app

import (
	"context"
	"encoding/json"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// CatalogService: catálogos maestros que IGDT gobierna y comparte con BDT (contactos de cliente y tarifario; los
// ítems de maestro como clientes y perfiles van por MasterService). BDT los recibe en solo lectura con Changes.
type CatalogService struct {
	Repo ports.CatalogRepository
}

func (s *CatalogService) Contacts(ctx context.Context, p *domain.Principal, clientID string) ([]json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	if !validUUID(clientID) {
		return nil, domain.ErrValidation
	}
	return s.Repo.Contacts(ctx, meta(p, ""), tid, clientID)
}

func (s *CatalogService) SaveContact(ctx context.Context, p *domain.Principal, ip string, c ports.ContactWrite) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	c.FirstName, c.LastName = strings.TrimSpace(c.FirstName), strings.TrimSpace(c.LastName)
	if !validUUID(c.ID) || !validUUID(c.ClientID) || c.FirstName == "" || c.LastName == "" ||
		!maxLen(100, c.FirstName, c.LastName, c.SecondLastName) || !maxLen(150, c.Position) || !maxLen(30, c.Phone, c.Phone2) ||
		!optEmail(c.Email) || !optEmail(c.Email2) {
		return domain.ErrValidation
	}
	return s.Repo.ContactSave(ctx, meta(p, ip), tid, c)
}

func (s *CatalogService) DeleteContact(ctx context.Context, p *domain.Principal, ip, id string) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if !validUUID(id) {
		return domain.ErrValidation
	}
	return s.Repo.ContactDelete(ctx, meta(p, ip), tid, id)
}

func (s *CatalogService) Tariffs(ctx context.Context, p *domain.Principal, clientID, profileID string) ([]json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	var c, pr *string
	if clientID != "" {
		if !validUUID(clientID) {
			return nil, domain.ErrValidation
		}
		c = &clientID
	}
	if profileID != "" {
		if !validUUID(profileID) {
			return nil, domain.ErrValidation
		}
		pr = &profileID
	}
	return s.Repo.Tariffs(ctx, meta(p, ""), tid, c, pr)
}

func (s *CatalogService) SaveTariff(ctx context.Context, p *domain.Principal, ip string, t ports.TariffWrite) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	from, err1 := time.Parse("2006-01-02", t.ValidFrom)
	if !validUUID(t.ID) || !validUUID(t.ProfileID) || !validUUID(t.CurrencyID) || !validUUID(t.RateTypeID) ||
		(t.ClientID != "" && !validUUID(t.ClientID)) || err1 != nil || t.Rate <= 0 || t.Rate > 9_999_999 || !maxLen(300, t.Notes) {
		return domain.ErrValidation
	}
	if t.ValidTo != "" {
		to, err := time.Parse("2006-01-02", t.ValidTo)
		if err != nil || to.Before(from) {
			return domain.ErrValidation
		}
	}
	return s.Repo.TariffSave(ctx, meta(p, ip), tid, t)
}

func (s *CatalogService) DeleteTariff(ctx context.Context, p *domain.Principal, ip, id string) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if !validUUID(id) {
		return domain.ErrValidation
	}
	return s.Repo.TariffDelete(ctx, meta(p, ip), tid, id)
}

// CatalogChanges: página de cambios para la sincronización de BDT (API de integración). Next es el cursor a enviar en
// la próxima llamada; More indica que quedan cambios.
type CatalogChanges struct {
	Changes []json.RawMessage `json:"changes"`
	Next    uint64            `json:"next"`
	More    bool              `json:"more"`
}

func (s *CatalogService) Changes(ctx context.Context, tenantID, since uint64, limit int) (*CatalogChanges, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	list, err := s.Repo.Changes(ctx, tenantID, since, limit)
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

func maxLen(n int, vals ...string) bool {
	for _, v := range vals {
		if utf8.RuneCountInString(v) > n {
			return false
		}
	}
	return true
}

func optEmail(e string) bool {
	e = strings.TrimSpace(e)
	if e == "" {
		return true
	}
	a, err := mail.ParseAddress(e)
	return err == nil && a.Address == e && len(e) <= 254
}
