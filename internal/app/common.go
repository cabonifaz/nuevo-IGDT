package app

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// tenantOf: el tenant del usuario; los usuarios de plataforma (Super Admin) no operan datos de un tenant.
func tenantOf(p *domain.Principal) (uint64, error) {
	if p == nil || p.TenantID == nil {
		return 0, domain.ErrForbidden
	}
	return *p.TenantID, nil
}

func validUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// decryptNames reemplaza los campos "<x>NameEnc" (nombre cifrado en base64) por "<x>Name" descifrado, a cualquier profundidad.
// ("nameEnc" a secas queda como "name").
func decryptNames(c ports.Cipher, doc json.RawMessage) json.RawMessage {
	var v any
	if json.Unmarshal(doc, &v) != nil {
		return doc
	}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for k, val := range t {
				if enc, ok := val.(string); ok && (strings.HasSuffix(k, "NameEnc") || k == "nameEnc") {
					name := ""
					if b, err := base64.StdEncoding.DecodeString(enc); err == nil {
						name, _ = c.Decrypt(b)
					}
					delete(t, k)
					t[strings.TrimSuffix(k, "Enc")] = name
					continue
				}
				walk(val)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	out, err := json.Marshal(v)
	if err != nil {
		return doc
	}
	return out
}
