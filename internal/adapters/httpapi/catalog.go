package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
)

// Catálogos maestros que IGDT gobierna y comparte con BDT: contactos de cliente y tarifario (clientes y perfiles son
// ítems de maestro y van por /tenant/masters/items).
func (s *Server) catalogRoutes(r chi.Router) {
	manage := s.requirePerm("catalog.manage")
	r.With(s.requirePerm("hire.read")).Get("/catalog/clients/{client}/contacts", s.catalogContacts)
	r.With(manage).Put("/catalog/clients/{client}/contacts/{id}", s.catalogSaveContact)
	r.With(manage).Delete("/catalog/contacts/{id}", s.catalogDeleteContact)
	r.With(manage).Get("/catalog/tariffs", s.catalogTariffs)
	r.With(manage).Put("/catalog/tariffs/{id}", s.catalogSaveTariff)
	r.With(manage).Delete("/catalog/tariffs/{id}", s.catalogDeleteTariff)
}

func (s *Server) catalogContacts(w http.ResponseWriter, r *http.Request) {
	out, err := s.Catalogs.Contacts(r.Context(), principal(r), chi.URLParam(r, "client"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) catalogSaveContact(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FirstName      string `json:"firstName"`
		LastName       string `json:"lastName"`
		SecondLastName string `json:"secondLastName"`
		Position       string `json:"position"`
		Phone          string `json:"phone"`
		Phone2         string `json:"phone2"`
		Email          string `json:"email"`
		Email2         string `json:"email2"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.noContent(w, r, s.Catalogs.SaveContact(r.Context(), principal(r), s.clientIP(r), ports.ContactWrite{
		ID: chi.URLParam(r, "id"), ClientID: chi.URLParam(r, "client"), FirstName: in.FirstName, LastName: in.LastName,
		SecondLastName: in.SecondLastName, Position: in.Position, Phone: in.Phone, Phone2: in.Phone2, Email: in.Email, Email2: in.Email2}))
}

func (s *Server) catalogDeleteContact(w http.ResponseWriter, r *http.Request) {
	s.noContent(w, r, s.Catalogs.DeleteContact(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id")))
}

func (s *Server) catalogTariffs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := s.Catalogs.Tariffs(r.Context(), principal(r), q.Get("clientId"), q.Get("profileId"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) catalogSaveTariff(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ClientID   string  `json:"clientId"`
		ProfileID  string  `json:"profileId"`
		CurrencyID string  `json:"currencyId"`
		RateTypeID string  `json:"rateTypeId"`
		Rate       float64 `json:"rate"`
		ValidFrom  string  `json:"validFrom"`
		ValidTo    string  `json:"validTo"`
		Notes      string  `json:"notes"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.noContent(w, r, s.Catalogs.SaveTariff(r.Context(), principal(r), s.clientIP(r), ports.TariffWrite{
		ID: chi.URLParam(r, "id"), ClientID: in.ClientID, ProfileID: in.ProfileID, CurrencyID: in.CurrencyID, RateTypeID: in.RateTypeID,
		Rate: in.Rate, ValidFrom: in.ValidFrom, ValidTo: in.ValidTo, Notes: in.Notes}))
}

func (s *Server) catalogDeleteTariff(w http.ResponseWriter, r *http.Request) {
	s.noContent(w, r, s.Catalogs.DeleteTariff(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id")))
}

// integrationCatalogImport: carga inicial de los catálogos que BDT ya tenía (una vez, al activar la integración).
func (s *Server) integrationCatalogImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items    json.RawMessage `json:"items"`
		Contacts json.RawMessage `json:"contacts"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, err := s.Catalogs.Import(r.Context(), apiClient(r).TenantID, in.Items, in.Contacts)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"map": out})
}

// integrationCatalogChanges: cambios de los catálogos compartidos desde el cursor ?since= (BDT sincroniza su copia
// de solo lectura). Sin since, desde el inicio.
func (s *Server) integrationCatalogChanges(w http.ResponseWriter, r *http.Request) {
	c := apiClient(r)
	q := r.URL.Query()
	since, _ := strconv.ParseUint(q.Get("since"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := s.Catalogs.Changes(r.Context(), c.TenantID, since, limit)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, out)
}
