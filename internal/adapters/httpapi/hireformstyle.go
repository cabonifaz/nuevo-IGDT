package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/cabonifaz/nuevo-IGDT/internal/app"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// Formato de las fichas de ingreso: el administrador del tenant en /tenant/hire-forms/style y el Super Admin sobre
// cualquier tenant en /platform/tenants/{tenantId}/hire-forms/style.

func (s *Server) formStyleRoutes(r chi.Router) {
	r.Get("/", s.formStyleGet)
	r.Put("/", s.formStyleSave)
	r.Post("/logo", s.formStyleLogo)
	r.Post("/preview/{form}", s.formStylePreview)
}

// styleTenant: el tenant del usuario o, en /platform, el indicado en la ruta.
func (s *Server) styleTenant(w http.ResponseWriter, r *http.Request) (uint64, string, bool) {
	p := principal(r)
	if t := chi.URLParam(r, "tenantId"); t != "" && strings.Contains(r.URL.Path, "/platform/") {
		id, _, err := s.Tenants.Resolve(r.Context(), p, t)
		if err != nil {
			s.writeErr(w, r, err)
			return 0, "", false
		}
		return id, t, true
	}
	if p.TenantID == nil {
		s.writeErr(w, r, domain.ErrForbidden)
		return 0, "", false
	}
	return *p.TenantID, p.TenantUUID, true
}

func (s *Server) formStyleGet(w http.ResponseWriter, r *http.Request) {
	tid, _, ok := s.styleTenant(w, r)
	if !ok {
		return
	}
	out, err := s.Selection.FormStyle(r.Context(), tid)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) formStyleSave(w http.ResponseWriter, r *http.Request) {
	tid, _, ok := s.styleTenant(w, r)
	if !ok {
		return
	}
	var in struct {
		Style     app.HireFormStyle `json:"style"`
		Reset     bool              `json:"reset"`
		ClearLogo bool              `json:"clearLogo"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, err := s.Selection.SaveFormStyle(r.Context(), principal(r), s.clientIP(r), tid, in.Style, in.Reset, in.ClearLogo)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) formStyleLogo(w http.ResponseWriter, r *http.Request) {
	tid, tenantUUID, ok := s.styleTenant(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 3<<20)
	if err := r.ParseMultipartForm(3 << 20); err != nil {
		s.writeErr(w, r, domain.NewError("FILE_TOO_LARGE"))
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		s.writeErr(w, r, domain.ErrValidation)
		return
	}
	defer f.Close()
	out, err := s.Selection.UploadFormLogo(r.Context(), principal(r), s.clientIP(r), tid, tenantUUID, hdr.Filename, f)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) formStylePreview(w http.ResponseWriter, r *http.Request) {
	tid, _, ok := s.styleTenant(w, r)
	if !ok {
		return
	}
	var in struct {
		Style *app.HireFormStyle `json:"style"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	form := strings.ToUpper(chi.URLParam(r, "form"))
	b, err := s.Selection.StylePreview(r.Context(), principal(r), tid, form, in.Style)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writePDF(w, b, "Vista_previa_"+form+".pdf")
}
