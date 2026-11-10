package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// Formato de las fichas de ingreso por tenant: colores, logo, datos de cada formato (código, versión, fecha y título),
// cargo del firmante, anexo por defecto del solicitante y texto del pie. Lo editan el administrador del tenant y el
// Super Admin. Los valores por defecto reproducen las fichas originales del FMI.

// FormHeaderStyle: datos del cuadro de la cabecera de un formato.
type FormHeaderStyle struct {
	Code    string `json:"code"`
	Version string `json:"version"`
	Date    string `json:"date"`
	Title   string `json:"title"`
}

type HireFormStyle struct {
	Primary        string                     `json:"primary"`    // barras de sección y cabeceras de tabla
	HeaderText     string                     `json:"headerText"` // texto sobre el color principal
	Border         string                     `json:"border"`     // bordes de la cabecera, ficha de ingreso y línea de firma
	LabelBg        string                     `json:"labelBg"`    // fondo de las etiquetas de la ficha de ingreso
	Title          string                     `json:"title"`      // color del título de la ficha de ingreso
	LogoSource     string                     `json:"logoSource"` // FORM (logo propio, si no el de la marca) | BRAND | NONE
	SignerTitle    string                     `json:"signerTitle"`
	RequesterAnnex string                     `json:"requesterAnnex"`
	FooterText     string                     `json:"footerText"`
	Forms          map[string]FormHeaderStyle `json:"forms"`
}

func DefaultFormStyle() HireFormStyle {
	return HireFormStyle{
		Primary: "#2F5496", HeaderText: "#FFFFFF", Border: "#1083C6", LabelBg: "#F1F1F1", Title: "#8A8A8A",
		LogoSource: "FORM", SignerTitle: "Gestor de Servicios", FooterText: "Generado con IGDT",
		Forms: map[string]FormHeaderStyle{
			"FMI":       {Code: "FT-GTH-12", Version: "04", Date: "05/11/2025", Title: "Formulario de movimiento"},
			"EQUIPMENT": {Code: "FT-GS-03", Version: "01", Date: "02/07/2020", Title: "Solicitud de instalación de software y hardware"},
			"USERS":     {Code: "FT-GS-01", Version: "01", Date: "31/08/2016", Title: "Solicitud de creación, modificación o desactivación de usuarios"},
		},
	}
}

// withDefaults completa lo que no se configuró con el formato por defecto.
func (st HireFormStyle) withDefaults() HireFormStyle {
	d := DefaultFormStyle()
	pick := func(v, def string) string {
		if strings.TrimSpace(v) == "" {
			return def
		}
		return v
	}
	out := HireFormStyle{
		Primary: strings.ToUpper(pick(st.Primary, d.Primary)), HeaderText: strings.ToUpper(pick(st.HeaderText, d.HeaderText)),
		Border: strings.ToUpper(pick(st.Border, d.Border)), LabelBg: strings.ToUpper(pick(st.LabelBg, d.LabelBg)),
		Title: strings.ToUpper(pick(st.Title, d.Title)), LogoSource: pick(st.LogoSource, d.LogoSource),
		SignerTitle: pick(st.SignerTitle, d.SignerTitle), RequesterAnnex: strings.TrimSpace(st.RequesterAnnex),
		FooterText: pick(st.FooterText, d.FooterText), Forms: map[string]FormHeaderStyle{},
	}
	for _, f := range FormCodes {
		h, def := st.Forms[f], d.Forms[f]
		out.Forms[f] = FormHeaderStyle{Code: pick(h.Code, def.Code), Version: pick(h.Version, def.Version),
			Date: pick(h.Date, def.Date), Title: pick(h.Title, def.Title)}
	}
	return out
}

func (st HireFormStyle) validate() error {
	for _, c := range []string{st.Primary, st.HeaderText, st.Border, st.LabelBg, st.Title} {
		if c != "" && !hexColor.MatchString(c) {
			return domain.NewError("INVALID_COLOR")
		}
	}
	if st.LogoSource != "" && st.LogoSource != "FORM" && st.LogoSource != "BRAND" && st.LogoSource != "NONE" {
		return domain.ErrValidation
	}
	if !maxLen(80, st.SignerTitle) || !maxLen(20, st.RequesterAnnex) || !maxLen(120, st.FooterText) {
		return domain.ErrValidation
	}
	for k, h := range st.Forms {
		if !contains(FormCodes, k) || !maxLen(30, h.Code) || !maxLen(10, h.Version) || !maxLen(20, h.Date) || !maxLen(120, h.Title) {
			return domain.ErrValidation
		}
	}
	return nil
}

// FormStyleView: formato vigente para el editor (con los valores por defecto y los logos disponibles).
type FormStyleView struct {
	Style        HireFormStyle `json:"style"`
	Defaults     HireFormStyle `json:"defaults"`
	Customized   bool          `json:"customized"`
	LogoURL      string        `json:"logoUrl,omitempty"`
	LogoName     string        `json:"logoName,omitempty"`
	BrandLogoURL string        `json:"brandLogoUrl,omitempty"`
	BrandLogoOK  bool          `json:"brandLogoPrintable"` // el logo de la marca es PNG/JPG (se puede imprimir)
	Company      string        `json:"company"`
	UpdatedAt    string        `json:"updatedAt,omitempty"`
}

type styleFile struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Mime string `json:"mime"`
	Name string `json:"name"`
}

type styleDoc struct {
	Style       *HireFormStyle `json:"style"`
	UpdatedAt   string         `json:"updatedAt"`
	Logo        *styleFile     `json:"logo"`
	BrandLogo   *styleFile     `json:"brandLogo"`
	Company     string         `json:"company"`
	DisplayName string         `json:"displayName"`
}

func (s *SelectionService) styleDoc(ctx context.Context, tid uint64) (*styleDoc, error) {
	raw, err := s.Repo.HireFormStyle(ctx, tid)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, domain.ErrNotFound
	}
	var d styleDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func printable(mime string) string {
	switch mime {
	case "image/png":
		return "PNG"
	case "image/jpeg":
		return "JPG"
	}
	return ""
}

func (s *SelectionService) FormStyle(ctx context.Context, tid uint64) (*FormStyleView, error) {
	d, err := s.styleDoc(ctx, tid)
	if err != nil {
		return nil, err
	}
	v := &FormStyleView{Defaults: DefaultFormStyle(), Company: d.Company, UpdatedAt: d.UpdatedAt}
	if d.Style != nil {
		v.Style, v.Customized = d.Style.withDefaults(), true
	} else {
		v.Style = HireFormStyle{}.withDefaults()
	}
	if d.Logo != nil {
		v.LogoURL, v.LogoName = "/api/v1/public/files/"+d.Logo.ID, d.Logo.Name
	}
	if d.BrandLogo != nil {
		v.BrandLogoURL, v.BrandLogoOK = "/api/v1/public/files/"+d.BrandLogo.ID, printable(d.BrandLogo.Mime) != ""
	}
	return v, nil
}

// SaveFormStyle guarda el formato (reset = volver al formato por defecto; clearLogo = quitar el logo propio).
func (s *SelectionService) SaveFormStyle(ctx context.Context, p *domain.Principal, ip string, tid uint64, in HireFormStyle, reset, clearLogo bool) (*FormStyleView, error) {
	var raw json.RawMessage
	if !reset {
		if err := in.validate(); err != nil {
			return nil, err
		}
		b, _ := json.Marshal(in.withDefaults())
		raw = b
	}
	if err := s.Repo.HireFormStyleSave(ctx, meta(p, ip), tid, raw, clearLogo); err != nil {
		return nil, err
	}
	return s.FormStyle(ctx, tid)
}

const maxFormLogoBytes = 2 << 20

// UploadFormLogo guarda el logo propio de las fichas (solo PNG o JPG: es lo que se puede imprimir en el PDF).
func (s *SelectionService) UploadFormLogo(ctx context.Context, p *domain.Principal, ip string, tid uint64, tenantUUID, originalName string, r io.Reader) (*FormStyleView, error) {
	if s.Storage == nil || s.Files == nil {
		return nil, domain.ErrValidation
	}
	br := bufio.NewReader(r)
	head, _ := br.Peek(512)
	mime := http.DetectContentType(head)
	ext := map[string]string{"image/png": "png", "image/jpeg": "jpg"}[mime]
	if ext == "" {
		return nil, domain.NewError("FILE_TYPE_NOT_ALLOWED")
	}
	data, err := io.ReadAll(io.LimitReader(br, maxFormLogoBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFormLogoBytes {
		return nil, domain.NewError("FILE_TOO_LARGE")
	}
	if _, ok := normalizeLogo(data); !ok {
		return nil, domain.NewError("FILE_TYPE_NOT_ALLOWED")
	}
	id := uuid.NewString()
	key := path.Join("tenants", tenantUUID, "branding", id+"."+ext)
	size, sum, err := s.Storage.Put(ctx, key, bytes.NewReader(data), maxFormLogoBytes)
	if err != nil {
		if strings.Contains(err.Error(), "tamaño") {
			return nil, domain.NewError("FILE_TOO_LARGE")
		}
		return nil, err
	}
	name := path.Base(strings.ReplaceAll(originalName, "\\", "/"))
	if len(name) > 200 {
		name = name[:200]
	}
	f := domain.FileObject{UUID: id, TenantID: &tid, OwnerType: "TENANT_BRANDING", Category: "FORM_LOGO",
		StorageKey: key, OriginalName: name, MimeType: mime, Size: size, SHA256: sum}
	if err := s.Files.Create(ctx, meta(p, ip), f, &tid); err != nil {
		_ = s.Storage.Delete(ctx, key)
		return nil, err
	}
	if err := s.Repo.HireFormLogoSet(ctx, meta(p, ip), tid, id); err != nil {
		return nil, err
	}
	return s.FormStyle(ctx, tid)
}

// printStyle: formato y logo con que se imprimen las fichas del tenant. override = formato sin guardar (vista previa).
type printStyle struct {
	style    HireFormStyle
	logo     []byte
	logoType string
	company  string
}

func (s *SelectionService) printStyle(ctx context.Context, tid uint64, override *HireFormStyle) (*printStyle, error) {
	d, err := s.styleDoc(ctx, tid)
	if err != nil {
		return nil, err
	}
	ps := &printStyle{company: d.DisplayName}
	if ps.company == "" {
		ps.company = d.Company
	}
	switch {
	case override != nil:
		ps.style = override.withDefaults()
	case d.Style != nil:
		ps.style = d.Style.withDefaults()
	default:
		ps.style = HireFormStyle{}.withDefaults()
	}
	var src *styleFile
	switch ps.style.LogoSource {
	case "FORM":
		src = d.Logo
		if src == nil {
			src = d.BrandLogo
		}
	case "BRAND":
		src = d.BrandLogo
	}
	if src != nil && printable(src.Mime) != "" && s.Storage != nil {
		if rc, err := s.Storage.Open(ctx, src.Key); err == nil {
			b, err := io.ReadAll(io.LimitReader(rc, maxFormLogoBytes+1))
			rc.Close()
			if err == nil && len(b) <= maxFormLogoBytes {
				if n, ok := normalizeLogo(b); ok {
					ps.logo, ps.logoType = n, "PNG"
				}
			}
		}
	}
	return ps, nil
}

// StylePreview imprime una ficha de ejemplo con el formato indicado (sin guardarlo).
func (s *SelectionService) StylePreview(ctx context.Context, p *domain.Principal, tid uint64, form string, st *HireFormStyle) ([]byte, error) {
	if !contains(FormCodes, form) || s.Renderer == nil {
		return nil, domain.ErrValidation
	}
	if st != nil {
		if err := st.validate(); err != nil {
			return nil, err
		}
	}
	ps, err := s.printStyle(ctx, tid, st)
	if err != nil {
		return nil, err
	}
	name := p.FullName
	if name == "" {
		name = "Nombre del gestor"
	}
	return s.Renderer.Render(sampleContext(ps, name).build(form))
}

// sampleContext: datos de ejemplo para la vista previa del formato.
func sampleContext(ps *printStyle, manager string) *formContext {
	const (
		contract = "00000000-0000-4000-8000-000000000001"
		reason   = "00000000-0000-4000-8000-000000000002"
		area     = "00000000-0000-4000-8000-000000000003"
		cur      = "00000000-0000-4000-8000-000000000004"
		laptop   = "00000000-0000-4000-8000-000000000005"
	)
	f := &HireForms{
		Hire: HireForm{Position: "Analista Programador", ContractID: contract, ReasonID: reason, AreaID: area, CurrencyID: cur,
			StartDate: time.Now().AddDate(0, 0, 7).Format("2006-01-02"), EndDate: time.Now().AddDate(0, 3, 7).Format("2006-01-02"),
			Schedule: "Lun a Vie 9:00 a.m. - 6:00 p.m.", ProjectService: "Servicios de desarrollo de sistemas",
			ContractObject: "Desarrollo y mantenimiento de sistemas", Pay: &Pay{Base: 3500, Mobility: 200}},
		Equipment: &EquipmentRequest{RequestDate: time.Now().Format("2006-01-02"), DeliveryDate: time.Now().AddDate(0, 0, 5).Format("2006-01-02"),
			Types: []string{laptop}, Processor: "i7", RAM: "16 GB", Disk: "512 GB", Software: []SoftwareItem{{"Windows", "11"}, {"Office", "365"}}},
		Users: &UserRequest{Username: "juan.perez", Email: "juan.perez@empresa.pe"},
	}
	return &formContext{
		req: map[string]any{"talent": map[string]any{"firstName": "Juan", "lastName": "Pérez", "secondLastName": "García",
			"docType": map[string]any{"label": "DNI"}, "docNumber": "12345678", "phone": "+51 999 888 777"},
			"client": map[string]any{"label": "Cliente de ejemplo S.A.C."}},
		forms: f,
		labels: map[string]ports.CatalogLabel{contract: {Code: "PLANILLA", Label: "Planilla - Reg. General"},
			reason: {Label: "Cubrir una vacante"}, area: {Label: "Outsourcing"}, cur: {Code: "PEN", Label: "Soles"},
			laptop: {Code: "LAPTOP", Label: "Laptop"}},
		settings: &HireFormSettings{}, manager: manager, issued: time.Now().In(limaTZ()), withPay: true, print: ps,
	}
}

var monthsES = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

func longDateES(t time.Time) string {
	return fmt.Sprintf("%d de %s de %d", t.Day(), monthsES[t.Month()-1], t.Year())
}

// normalizeLogo decodifica el logo (PNG o JPG, incluso PNG entrelazado) y lo vuelve a codificar como PNG simple, que es
// lo que acepta la librería de PDF. Rechaza imágenes demasiado grandes en píxeles.
func normalizeLogo(b []byte) ([]byte, bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > 16_000_000 {
		return nil, false
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, false
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}
