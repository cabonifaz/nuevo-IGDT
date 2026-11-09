package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/cabonifaz/nuevo-IGDT/internal/app"
)

// Bandeja de selección: RQ de BDT (solo lectura) y solicitudes de ingreso.
func (s *Server) selectionRoutes(r chi.Router) {
	read := s.requirePerm("hire.read")
	feat := s.requireFeature("selection_inbox")
	r.With(read, feat).Get("/requirements", s.selRequirements)
	r.With(read, feat).Get("/requirements/{id}", s.selRequirement)
	r.With(read, feat).Get("/hire-requests", s.selHires)
	r.With(read, feat).Get("/hire-requests/{id}", s.selHire)
	r.With(s.requirePerm("hire.write"), feat).Post("/hire-requests/{id}/take", s.selTakeHire)
	r.With(s.requirePerm("hire.write"), feat).Post("/hire-requests/{id}/hire", s.selCompleteHire)
	r.With(s.requirePerm("employee.read")).Get("/employees", s.selEmployees)
}

func (s *Server) selRequirements(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.Requirements(r.Context(), principal(r), r.URL.Query().Get("scope"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) selRequirement(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.Requirement(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) selHires(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.Hires(r.Context(), principal(r), r.URL.Query().Get("status"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) selHire(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.Hire(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) selTakeHire(w http.ResponseWriter, r *http.Request) {
	s.noContent(w, r, s.Selection.TakeHire(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id")))
}

// integrationRequirements: BDT publica el último estado de sus RQ (lote de hasta 50).
func (s *Server) integrationRequirements(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, err := s.Selection.ReceiveRequirements(r.Context(), apiClient(r), in.Items)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// integrationHires: BDT envía los candidatos confirmados (o el retiro de la confirmación); lote de hasta 50.
func (s *Server) integrationHires(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, err := s.Selection.ReceiveHires(r.Context(), apiClient(r), in.Items)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

func (s *Server) selCompleteHire(w http.ResponseWriter, r *http.Request) {
	var in app.HireForm
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	id, err := s.Selection.CompleteHire(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) selEmployees(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.Employees(r.Context(), principal(r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// integrationHireEvents: ingresos registrados desde el cursor ?since= (BDT marca al candidato y cierra el RQ).
func (s *Server) integrationHireEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, _ := strconv.ParseUint(q.Get("since"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := s.Selection.HireEvents(r.Context(), apiClient(r).TenantID, since, limit)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, map[string]any{"events": out.Changes, "next": out.Next, "more": out.More})
}
