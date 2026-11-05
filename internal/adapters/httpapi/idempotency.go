package httpapi

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// idempotent: si la petición trae X-Client-Op-Id (operación encolada offline), se registra
// y un reenvío de la misma operación no se vuelve a aplicar.
func (s *Server) idempotent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := r.Header.Get("X-Client-Op-Id")
		p := principal(r)
		if op == "" || r.Method == http.MethodGet || p == nil || p.TenantID == nil {
			next.ServeHTTP(w, r)
			return
		}
		if _, err := uuid.Parse(op); err != nil {
			s.writeErr(w, r, domain.ErrValidation)
			return
		}
		entity := uuidRe.ReplaceAllString(strings.TrimPrefix(r.URL.Path, "/api/v1/tenant/"), "{id}")
		if len(entity) > 60 {
			entity = entity[:60]
		}
		dup, status, result, err := s.Sync.Register(r.Context(), *p.TenantID, p.UserID, op, entity, r.Method)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		if dup {
			w.Header().Set("X-Idempotent-Replay", "true")
			writeJSON(w, http.StatusOK, map[string]any{"replayed": true, "status": status, "result": json.RawMessage(nonEmpty(result))})
			return
		}
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		st := "APPLIED"
		if rec.status >= 400 {
			st = "REJECTED"
		}
		res, _ := json.Marshal(map[string]any{"httpStatus": rec.status})
		_ = s.Sync.Complete(r.Context(), *p.TenantID, op, st, res)
	})
}

var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

func nonEmpty(b []byte) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }

func (s *Server) noContent(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
