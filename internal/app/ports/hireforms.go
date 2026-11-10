package ports

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

// HireUpdate: corrección del ingreso confirmado. PayEnc nil = la remuneración no cambia.
type HireUpdate struct {
	HireInput
	ChangeReason string
	Changes      json.RawMessage
}

// HireFormsRepository: borrador de las fichas, corrección/revocación del ingreso, configuración, envíos y enlace con BDT.
type HireFormsRepository interface {
	HireDraftSave(ctx context.Context, m Meta, tenantID uint64, requestID string, draft json.RawMessage, payEnc []byte) error
	HireDraft(ctx context.Context, m Meta, tenantID uint64, requestID string) (json.RawMessage, error)
	HireUpdate(ctx context.Context, m Meta, tenantID uint64, in HireUpdate) error
	HireRevoke(ctx context.Context, m Meta, tenantID uint64, requestID, reason string) error
	HireFormSetting(ctx context.Context, tenantID uint64) (json.RawMessage, error)
	HireFormSettingSave(ctx context.Context, m Meta, tenantID uint64, recipients json.RawMessage, ccManager bool, graceDays int, domain string) error
	HireFormDispatchAdd(ctx context.Context, actorID, tenantID uint64, requestID, form, reason string, recipients []string, status, errText string) error
	BDTLinkSet(ctx context.Context, tenantID, apiKeyID uint64, keyEnc []byte) error
	BDTLinkClear(ctx context.Context, tenantID, apiKeyID uint64) error
	BDTLinkGet(ctx context.Context, tenantID uint64) ([]byte, error)
	CatalogLabeler
}

// ---- Fichas en PDF ----

// FormField: una fila etiqueta/valor de una ficha.
type FormField [2]string // etiqueta, valor


// FormSection: bloque de una ficha (título y filas; Rows para tablas como la lista de software).
type FormSection struct {
	Title  string
	Fields []FormField
	Header []string
	Rows   [][]string
}

// FormDoc: ficha lista para imprimir (código del formato, título, empresa y secciones).
type FormDoc struct {
	Code, Title, Company, Subtitle string
	Sections                       []FormSection
	Footer                         string
	Logo                           []byte // PNG/JPG opcional
	LogoType                       string // "PNG" | "JPG"
}

type FormRenderer interface {
	Render(doc FormDoc) ([]byte, error)
}

// ---- Correo saliente ----

type MailAttachment struct {
	Name, ContentType string
	Data              []byte
}

type Mail struct {
	To, Cc      []string
	Subject     string
	Text        string
	Attachments []MailAttachment
}

// ErrNoMailer: no hay proveedor de correo configurado (el envío queda registrado como pendiente).
var ErrNoMailer = errors.New("mailer not configured")

type MailSender interface {
	Send(ctx context.Context, m Mail) error
}

// ---- Lectura de BDT ----

// BDTReader: lectura de candidatos en BDT con la llave del tenant (perfil, CV y postulaciones).
type BDTReader interface {
	Enabled() bool
	Talent(ctx context.Context, key, talentID string) (json.RawMessage, error)
	TalentFile(ctx context.Context, key, talentID, fileID string) (contentType string, body io.ReadCloser, err error)
}

// CatalogLabel: código y etiqueta (es) de un ítem de catálogo.
type CatalogLabel struct {
	Code, Label, Str1 string
}

type CatalogLabeler interface {
	CatalogLabels(ctx context.Context, tenantID uint64, ids []string) (map[string]CatalogLabel, error)
}
