package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Aprobación por ficha, alertas de pendientes y gestores por cliente (V0019).

// POST /tenant/hire-requests/{id}/forms/{form}/approve — guarda las fichas como borrador y aprueba la indicada (FMI,
// USERS o EQUIPMENT); se envía por correo a sus destinatarios.
func (s *Server) selApproveForm(w http.ResponseWriter, r *http.Request) {
	in, err := decodeForms(w, r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, err := s.Selection.ApproveForm(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id"), chi.URLParam(r, "form"), in)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PUT /tenant/hire-requests/{id}/equipment-required {"required": bool}
func (s *Server) selEquipmentRequired(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Required bool `json:"required"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.noContent(w, r, s.Selection.SetEquipmentRequired(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id"), in.Required))
}

// GET /tenant/hire-alerts — solicitudes pendientes que el usuario puede atender (vencidas y por vencer primero).
func (s *Server) selHireAlerts(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.Alerts(r.Context(), principal(r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) selClientManagers(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.ClientManagers(r.Context(), principal(r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PUT /tenant/client-managers/{clientId} {"primaryId": uuid|"", "secondaryId": uuid|""}
func (s *Server) selSetClientManagers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PrimaryID   string `json:"primaryId"`
		SecondaryID string `json:"secondaryId"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.noContent(w, r, s.Selection.SetClientManagers(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "clientId"), in.PrimaryID, in.SecondaryID))
}

func (s *Server) selClientManagerCandidates(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.ClientManagerCandidates(r.Context(), principal(r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /integration/v1/managers (alcance hires.write): gestores de IGDT y los gestores de cada cliente, para que BDT
// permita dirigir la solicitud de ingreso a un gestor.
func (s *Server) integrationManagers(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.IntegrationManagers(r.Context(), apiClient(r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, out)
}
