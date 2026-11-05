package mysql

import (
	"context"
	"encoding/json"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
)

type CatalogRepo struct{ *Store }

func (r CatalogRepo) Contacts(ctx context.Context, m ports.Meta, tenantID uint64, clientID string) ([]json.RawMessage, error) {
	return docRepo(r).rawDocs(ctx, "sp_client_contact_list", m.ActorID, tenantID, clientID)
}

func (r CatalogRepo) ContactSave(ctx context.Context, m ports.Meta, tenantID uint64, c ports.ContactWrite) error {
	return r.exec(ctx, "sp_client_contact_save", m.ActorID, tenantID, c.ID, c.ClientID, c.FirstName, c.LastName, nullStr(c.SecondLastName),
		nullStr(c.Position), nullStr(c.Phone), nullStr(c.Phone2), nullStr(c.Email), nullStr(c.Email2), nullStr(m.IP))
}

func (r CatalogRepo) ContactDelete(ctx context.Context, m ports.Meta, tenantID uint64, id string) error {
	return r.exec(ctx, "sp_client_contact_delete", m.ActorID, tenantID, id, nullStr(m.IP))
}

func (r CatalogRepo) Tariffs(ctx context.Context, m ports.Meta, tenantID uint64, clientID, profileID *string) ([]json.RawMessage, error) {
	return docRepo(r).rawDocs(ctx, "sp_tariff_list", m.ActorID, tenantID, clientID, profileID)
}

func (r CatalogRepo) TariffSave(ctx context.Context, m ports.Meta, tenantID uint64, t ports.TariffWrite) error {
	return r.exec(ctx, "sp_tariff_save", m.ActorID, tenantID, t.ID, nullStr(t.ClientID), t.ProfileID, t.CurrencyID, t.RateTypeID,
		t.Rate, t.ValidFrom, nullStr(t.ValidTo), nullStr(t.Notes), nullStr(m.IP))
}

func (r CatalogRepo) TariffDelete(ctx context.Context, m ports.Meta, tenantID uint64, id string) error {
	return r.exec(ctx, "sp_tariff_delete", m.ActorID, tenantID, id, nullStr(m.IP))
}

func (r CatalogRepo) Import(ctx context.Context, tenantID uint64, items, contacts json.RawMessage) ([]ports.ImportMap, error) {
	rs, err := r.call(ctx, "sp_catalog_import", tenantID, optJSON(items), optJSON(contacts))
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []ports.ImportMap{}
	if !rs.Next() {
		return out, nil
	}
	err = rs.Each(func(scan func(...any) error) error {
		var m ports.ImportMap
		if err := scan(&m.Entity, &m.SourceID, &m.IgdtID, &m.Created); err != nil {
			return err
		}
		out = append(out, m)
		return nil
	})
	return out, err
}

func (r CatalogRepo) Changes(ctx context.Context, tenantID, since uint64, limit int) ([]ports.CatalogChange, error) {
	rs, err := r.call(ctx, "sp_catalog_changes", tenantID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []ports.CatalogChange{}
	if !rs.Next() {
		return out, nil
	}
	err = rs.Each(func(scan func(...any) error) error {
		var c ports.CatalogChange
		var doc []byte
		if err := scan(&c.Cursor, &doc); err != nil {
			return err
		}
		c.Doc = doc
		out = append(out, c)
		return nil
	})
	return out, err
}
