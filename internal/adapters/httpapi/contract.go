package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/cabonifaz/nuevo-IGDT/internal/app"
)

// Fin de contrato (v0.13): alertas, contexto del asistente, vista previa de los PDF, ejecución y reenvío.

// GET /tenant/contract-alerts — contratos vigentes por terminar (o vencidos) que el usuario puede gestionar.
func (s *Server) selContractAlerts(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.ContractAlerts(r.Context(), principal(r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, out)
}

// GET /tenant/hire-requests/{id}/contract — contrato, nivel de aviso, operaciones, equipo y destinatarios de cada correo.
func (s *Server) selContract(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.Contract(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, out)
}

// POST /tenant/hire-requests/{id}/contract-ops {type: RENEW|TERMINATE|TRANSFER|RQ_CHANGE, …}
func (s *Server) selContractOp(w http.ResponseWriter, r *http.Request) {
	var in app.ContractOpInput
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	out, err := s.Selection.ContractOp(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// POST /tenant/hire-requests/{id}/contract-ops/preview/{form} — PDF (FMI o USERS) de la operación antes de confirmarla.
func (s *Server) selContractPreview(w http.ResponseWriter, r *http.Request) {
	var in app.ContractOpInput
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	b, name, err := s.Selection.ContractPreview(r.Context(), principal(r), chi.URLParam(r, "id"), chi.URLParam(r, "form"), in)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writePDF(w, b, name)
}

// POST /tenant/hire-requests/{id}/contract-ops/{opId}/resend — reenvía los correos de la operación.
func (s *Server) selContractResend(w http.ResponseWriter, r *http.Request) {
	if err := s.Selection.ResendContractOp(r.Context(), principal(r), chi.URLParam(r, "id"), chi.URLParam(r, "opId")); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
