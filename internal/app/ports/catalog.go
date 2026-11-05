package ports

import (
	"context"
	"encoding/json"
)

// ContactWrite: contacto de un cliente (catálogo compartido con BDT).
type ContactWrite struct {
	ID, ClientID, FirstName, LastName, SecondLastName, Position, Phone, Phone2, Email, Email2 string
}

// TariffWrite: tarifa de un perfil, general (ClientID vacío) o de un cliente, con vigencia (fechas AAAA-MM-DD).
type TariffWrite struct {
	ID, ClientID, ProfileID, CurrencyID, RateTypeID, ValidFrom, ValidTo, Notes string
	Rate                                                                       float64
}

// CatalogChange: un cambio de catálogo compartido para la sincronización de BDT (Cursor creciente).
type CatalogChange struct {
	Cursor uint64
	Doc    json.RawMessage
}

// ImportMap: resultado de la carga inicial: id de BDT y el id que quedó en IGDT (igual si se creó).
type ImportMap struct {
	Entity   string `json:"entity"`
	SourceID string `json:"sourceId"`
	IgdtID   string `json:"igdtId"`
	Created  bool   `json:"created"`
}

type CatalogRepository interface {
	Import(ctx context.Context, tenantID uint64, items, contacts json.RawMessage) ([]ImportMap, error)
	Contacts(ctx context.Context, m Meta, tenantID uint64, clientID string) ([]json.RawMessage, error)
	ContactSave(ctx context.Context, m Meta, tenantID uint64, c ContactWrite) error
	ContactDelete(ctx context.Context, m Meta, tenantID uint64, id string) error
	Tariffs(ctx context.Context, m Meta, tenantID uint64, clientID, profileID *string) ([]json.RawMessage, error)
	TariffSave(ctx context.Context, m Meta, tenantID uint64, t TariffWrite) error
	TariffDelete(ctx context.Context, m Meta, tenantID uint64, id string) error
	Changes(ctx context.Context, tenantID, since uint64, limit int) ([]CatalogChange, error)
}
