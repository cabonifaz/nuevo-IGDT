package httpapi

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/cabonifaz/nuevo-IGDT/internal/app"
)

// decodeForms acepta las tres fichas ({"hire":…,"equipment":…,"users":…}) o, por compatibilidad, solo la ficha de ingreso.
func decodeForms(w http.ResponseWriter, r *http.Request) (app.HireForms, error) {
	var raw json.RawMessage
	var out app.HireForms
	if err := decode(w, r, &raw); err != nil {
		return out, err
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return out, err
	}
	if _, ok := probe["hire"]; ok {
		err := json.Unmarshal(raw, &out)
		return out, err
	}
	err := json.Unmarshal(raw, &out.Hire)
	if err == nil && len(out.Hire.Equipment) > 0 && !out.Hire.HasEquipment {
		out.Equipment = &app.EquipmentRequest{Types: out.Hire.Equipment}
	}
	return out, err
}

func (s *Server) selCompleteHire(w http.ResponseWriter, r *http.Request) {
	in, err := decodeForms(w, r)
	if err != nil {
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

func (s *Server) selUpdateHire(w http.ResponseWriter, r *http.Request) {
	in, err := decodeForms(w, r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.noContent(w, r, s.Selection.UpdateHire(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id"), in))
}

func (s *Server) selSaveDraft(w http.ResponseWriter, r *http.Request) {
	in, err := decodeForms(w, r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.noContent(w, r, s.Selection.SaveDraft(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id"), in))
}

func (s *Server) selRevokeHire(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.noContent(w, r, s.Selection.RevokeHire(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id"), in.Reason))
}

func (s *Server) selResendForms(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Forms []string `json:"forms"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.Selection.ResendForms(r.Context(), principal(r), chi.URLParam(r, "id"), in.Forms); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func writePDF(w http.ResponseWriter, b []byte, name string) {
	noStore(w)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

func (s *Server) selFormPDF(w http.ResponseWriter, r *http.Request) {
	b, name, err := s.Selection.FormPDF(r.Context(), principal(r), chi.URLParam(r, "id"), chi.URLParam(r, "form"), nil)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writePDF(w, b, name)
}

func (s *Server) selFormPreview(w http.ResponseWriter, r *http.Request) {
	in, err := decodeForms(w, r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	b, name, err := s.Selection.FormPDF(r.Context(), principal(r), chi.URLParam(r, "id"), chi.URLParam(r, "form"), &in)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writePDF(w, b, name)
}

func (s *Server) selFormSettings(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.FormSettings(r.Context(), principal(r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) selSaveFormSettings(w http.ResponseWriter, r *http.Request) {
	var in app.HireFormSettings
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.noContent(w, r, s.Selection.SaveFormSettings(r.Context(), principal(r), s.clientIP(r), in))
}

func (s *Server) selTalent(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.Talent(r.Context(), principal(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) selTalentFile(w http.ResponseWriter, r *http.Request) {
	ct, body, err := s.Selection.TalentFile(r.Context(), principal(r), chi.URLParam(r, "id"), chi.URLParam(r, "fid"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer body.Close()
	noStore(w)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, io.LimitReader(body, 25<<20))
}

// integrationBDTLink: BDT entrega (PUT) o retira (DELETE) la llave con la que IGDT lee los candidatos.
func (s *Server) integrationBDTLink(w http.ResponseWriter, r *http.Request) {
	c := apiClient(r)
	switch r.Method {
	case http.MethodGet:
		ok, err := s.Selection.BDTLinked(r.Context(), c.TenantID)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		noStore(w)
		writeJSON(w, http.StatusOK, map[string]bool{"linked": ok})
	case http.MethodDelete:
		s.noContent(w, r, s.Selection.UnlinkBDT(r.Context(), c))
	default:
		var in struct {
			APIKey string `json:"apiKey"`
		}
		if err := decode(w, r, &in); err != nil {
			s.writeErr(w, r, err)
			return
		}
		s.noContent(w, r, s.Selection.LinkBDT(r.Context(), c, in.APIKey))
	}
}

func (s *Server) selAssignHire(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID string `json:"userId"`
	}
	if err := decode(w, r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.Selection.AssignHire(r.Context(), principal(r), s.clientIP(r), chi.URLParam(r, "id"), in.UserID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) selAssignees(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.Assignees(r.Context(), principal(r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) selTalentCVSource(w http.ResponseWriter, r *http.Request) {
	out, err := s.Selection.TalentCVSource(r.Context(), principal(r), chi.URLParam(r, "id"), r.URL.Query().Get("lang"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// selSearchTalents: GET /tenant/talents?q=&position=&education=&skill=&english=&minYears=&page=&pageSize= — banco de
// talentos de BDT en solo lectura.
func (s *Server) selSearchTalents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("pageSize"))
	out, err := s.Selection.SearchTalents(r.Context(), principal(r), app.TalentSearch{
		Q: q.Get("q"), Position: q.Get("position"), Education: q.Get("education"), Skill: q.Get("skill"),
		English: q.Get("english"), MinYears: q.Get("minYears"), Page: page, PageSize: size,
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}
