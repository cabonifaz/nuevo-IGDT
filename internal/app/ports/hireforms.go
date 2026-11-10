package ports

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
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
	HireFormStyle(ctx context.Context, tenantID uint64) (json.RawMessage, error)
	HireFormStyleSave(ctx context.Context, m Meta, tenantID uint64, style json.RawMessage, clearLogo bool) error
	HireFormLogoSet(ctx context.Context, m Meta, tenantID uint64, fileUUID string) error
	HireAssertAssignee(ctx context.Context, m Meta, tenantID uint64, requestID, states string) error
	HireAssign(ctx context.Context, m Meta, tenantID uint64, requestID, userUUID string) error
	HireAssignees(ctx context.Context, m Meta, tenantID uint64) ([]json.RawMessage, error)
	UserSignatureSet(ctx context.Context, m Meta, fileUUID string) error
	UserSignatureClear(ctx context.Context, m Meta) error
	UserSignature(ctx context.Context, tenantID, actorID uint64, userUUID string) (key, mime string, err error)
	HireFormDispatchAdd(ctx context.Context, actorID, tenantID uint64, requestID, form, reason string, recipients []string, status, errText string) error
	BDTLinkSet(ctx context.Context, tenantID, apiKeyID uint64, keyEnc []byte) error
	BDTLinkClear(ctx context.Context, tenantID, apiKeyID uint64) error
	BDTLinkGet(ctx context.Context, tenantID uint64) ([]byte, error)
	CatalogLabeler
}

// ---- Fichas en PDF ----

// FormStyle: colores de las fichas (#RRGGBB). Primary: barras de sección y cabeceras de tabla; HeaderText: su texto;
// Border: bordes de la cabecera y de la ficha de ingreso, y la línea de firma; LabelBg: fondo de las etiquetas;
// Title: color del título de la ficha de ingreso.
type FormStyle struct {
	Primary, HeaderText, Border, LabelBg, Title string
}

// FormHeader: datos del formato (cuadro de la cabecera).
type FormHeader struct {
	Code, Version, Date, Title string
}

// FormCheck: casilla marcada o no con su texto.
type FormCheck struct {
	Label string
	On    bool
}

// FMIPrint: FT-GTH-12 Formulario de movimiento (ingreso). Los montos ya vienen con moneda; "" = vacío.
type FMIPrint struct {
	Name, Client, Modality, Reason, Position, Schedule string
	Base, Mobility, Bonus                              string
	HasBase, HasMobility, Monthly, Quarterly           bool
	Start, End, Project, Object, Sunat, Site, Notes    string
	Locator                                            bool // locación de servicios (RxH): sin cabecera, título subrayado
}

// EquipmentPrint: FT-GS-03 Solicitud de instalación de software y hardware.
type EquipmentPrint struct {
	Name, Client, Area, Position, DocNumber, Phone string
	RequestDate, DeliveryDate                      string
	Types                                          []FormCheck
	Processor, RAM, Disk, Brand                    string
	AnnexFixed, AnnexSoftphone                     bool
	Mobile, MobileInternet                         bool
	Accessories, Notes                             string
	Software                                       [][2]string // producto, versión
}

// UsersPrint: FT-GS-01 Solicitud de creación, modificación o desactivación de usuarios.
type UsersPrint struct {
	RequesterName, RequesterArea, RequesterPosition, RequesterAnnex, Date string
	Name, Username, Email, Area, Notes                                    string
}

// FormPrint: una ficha lista para imprimir con el formato del tenant.
type FormPrint struct {
	Form      string // FMI | EQUIPMENT | USERS
	Header    FormHeader
	Style     FormStyle
	Logo      []byte // PNG/JPG; sin logo se escribe Company en su lugar
	LogoType  string // "PNG" | "JPG"
	Company   string
	Signer    string // nombre del gestor que firma
	Signature []byte // imagen PNG de la firma del gestor (opcional)
	SignerPos string // cargo del firmante
	Footer    string // pie completo ("Generado con … — Fecha de emisión: …")
	FMI       *FMIPrint
	Equipment *EquipmentPrint
	Users     *UsersPrint
}

type FormRenderer interface {
	Render(doc FormPrint) ([]byte, error)
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
	CVSource(ctx context.Context, key, talentID, lang string) (json.RawMessage, error)
	SearchTalents(ctx context.Context, key string, q url.Values) (json.RawMessage, error)
}

// CatalogLabel: código y etiqueta (es) de un ítem de catálogo.
type CatalogLabel struct {
	Code, Label, Str1 string
}

type CatalogLabeler interface {
	CatalogLabels(ctx context.Context, tenantID uint64, ids []string) (map[string]CatalogLabel, error)
}
