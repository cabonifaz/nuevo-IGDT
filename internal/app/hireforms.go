package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// Fichas de ingreso: FMI = FT-GTH-12 Formulario de Ingreso, EQUIPMENT = FT-GS-03 Solicitud de Hardware y Software,
// USERS = FT-GS-01 Solicitud de Creación de Usuarios.
var FormCodes = []string{"FMI", "EQUIPMENT", "USERS"}

var formMeta = map[string]struct{ Code, Title, File string }{
	"FMI":       {"FT-GTH-12", "Formulario de Ingreso", "Ficha_de_ingreso"},
	"EQUIPMENT": {"FT-GS-03", "Solicitud de Requerimiento de Hardware y Software", "Solicitud_de_equipo"},
	"USERS":     {"FT-GS-01", "Solicitud de Creación de Usuarios", "Solicitud_de_usuarios"},
}

// HireFormSettings: destinatarios de cada ficha, copia al gestor, días de gracia para corregir y dominio corporativo.
type HireFormSettings struct {
	Recipients      map[string][]string `json:"recipients"`
	CCManager       bool                `json:"ccManager"`
	EditGraceDays   int                 `json:"editGraceDays"`
	CorporateDomain string              `json:"corporateDomain"`
	Company         string              `json:"company,omitempty"`
	TaxID           string              `json:"taxId,omitempty"`
}


func (s *SelectionService) settings(ctx context.Context, tid uint64) (*HireFormSettings, error) {
	doc, err := s.Repo.HireFormSetting(ctx, tid)
	if err != nil {
		return nil, err
	}
	var out HireFormSettings
	if err := json.Unmarshal(doc, &out); err != nil {
		return nil, err
	}
	if out.Recipients == nil {
		out.Recipients = map[string][]string{}
	}
	for _, f := range FormCodes {
		if out.Recipients[f] == nil {
			out.Recipients[f] = []string{}
		}
	}
	return &out, nil
}

func (s *SelectionService) FormSettings(ctx context.Context, p *domain.Principal) (*HireFormSettings, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	return s.settings(ctx, tid)
}

func (s *SelectionService) SaveFormSettings(ctx context.Context, p *domain.Principal, ip string, in HireFormSettings) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if in.EditGraceDays < 0 || in.EditGraceDays > 60 {
		return domain.ErrValidation
	}
	in.CorporateDomain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(in.CorporateDomain), "@"))
	if in.CorporateDomain != "" {
		if a, err := mail.ParseAddress("x@" + in.CorporateDomain); err != nil || a.Address != "x@"+in.CorporateDomain || len(in.CorporateDomain) > 120 {
			return domain.ErrValidation
		}
	}
	rec := map[string][]string{}
	for _, f := range FormCodes {
		list := []string{}
		seen := map[string]bool{}
		for _, e := range in.Recipients[f] {
			e = strings.ToLower(strings.TrimSpace(e))
			if e == "" || seen[e] {
				continue
			}
			if a, err := mail.ParseAddress(e); err != nil || a.Address != e || len(e) > 150 {
				return domain.ErrValidation
			}
			seen[e] = true
			list = append(list, e)
		}
		if len(list) > 20 {
			return domain.ErrValidation
		}
		rec[f] = list
	}
	raw, _ := json.Marshal(rec)
	return s.Repo.HireFormSettingSave(ctx, meta(p, ip), tid, raw, in.CCManager, in.EditGraceDays, in.CorporateDomain)
}

// ---------------- Impresión ----------------

type formContext struct {
	req      map[string]any // solicitud (datos personales descifrados)
	forms    *HireForms
	labels   map[string]ports.CatalogLabel
	settings *HireFormSettings
	manager  string
	issued   time.Time
	withPay  bool
}

func (c *formContext) lab(id string) string { return c.labels[id].Label }

func yesNo(v bool) string {
	if v {
		return "Sí"
	}
	return "No"
}

func fmtDate(d string) string {
	t, err := time.Parse("2006-01-02", dateOnly(d))
	if err != nil {
		return d
	}
	return t.Format("02/01/2006")
}

func money(cur string, v float64) string {
	if v <= 0 {
		return ""
	}
	return fmt.Sprintf("%s %s", cur, formatAmount(v))
}

func formatAmount(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	intPart, dec := s[:len(s)-3], s[len(s)-3:]
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String() + dec
}

func (c *formContext) talentName() string {
	return strings.Join(nonEmpty([]string{str(c.req, "talent", "firstName"), str(c.req, "talent", "lastName"),
		str(c.req, "talent", "secondLastName")}), " ")
}

func (c *formContext) document() string {
	return strings.TrimSpace(str(c.req, "talent", "docType", "label") + " " + str(c.req, "talent", "docNumber"))
}

func (c *formContext) footer() string {
	return fmt.Sprintf("Emitido el %s por %s · IGDT", c.issued.Format("02/01/2006 15:04"), c.manager)
}

func (c *formContext) build(form string) ports.FormDoc {
	m := formMeta[form]
	h := c.forms.Hire
	company := c.settings.Company
	if c.settings.TaxID != "" {
		company += " · RUC " + c.settings.TaxID
	}
	rq := strings.TrimSpace(str(c.req, "requirement", "code") + " · " + str(c.req, "requirement", "title"))
	doc := ports.FormDoc{Code: m.Code, Title: m.Title, Company: company, Footer: c.footer(),
		Subtitle: "Requerimiento " + strings.Trim(rq, " ·")}
	client := str(c.req, "client", "label")
	switch form {
	case "FMI":
		contract := c.labels[h.ContractID]
		rxh := contract.Str1 == "RXH"
		if rxh {
			doc.Title += " — Locación de servicios"
		}
		cur := c.labels[h.CurrencyID].Code
		pay := []ports.FormField{{"Moneda", cur}}
		switch {
		case !c.withPay:
			pay = append(pay, ports.FormField{"Remuneración", "Reservado"})
		case h.Pay != nil && rxh:
			pay = append(pay, ports.FormField{"Honorario", money(cur, h.Pay.Fee)})
		case h.Pay != nil:
			pay = append(pay, ports.FormField{"Monto base", money(cur, h.Pay.Base)}, ports.FormField{"Movilidad", money(cur, h.Pay.Mobility)},
				ports.FormField{"Bono trimestral", money(cur, h.Pay.Quarterly)}, ports.FormField{"Bono semestral", money(cur, h.Pay.Semiannual)})
		}
		end := fmtDate(h.EndDate)
		if h.EndDate == "" {
			end = "Indeterminado"
		}
		sunat := yesNo(h.SunatDeclared)
		if rxh {
			sunat = "No"
		}
		doc.Sections = []ports.FormSection{
			{Title: "Datos del colaborador", Fields: []ports.FormField{{"Nombres y apellidos", c.talentName()}, {"Documento", c.document()},
				{"Correo", str(c.req, "talent", "email")}, {"Teléfono", str(c.req, "talent", "phone")}}},
			{Title: "Ingreso", Fields: []ports.FormField{{"Unidad / área", c.lab(h.AreaID)}, {"Modalidad de contrato", contract.Label},
				{"Motivo de ingreso", c.lab(h.ReasonID)}, {"Cargo", h.Position}, {"Horario de trabajo", h.Schedule}, {"Cliente", client},
				{"Proyecto / servicio", h.ProjectService}, {"Objeto del contrato", h.ContractObject}, {"Ubicación", h.Location},
				{"Fecha de inicio", fmtDate(h.StartDate)}, {"Fecha de término", end}, {"Declarado en SUNAT", sunat},
				{"Sede a declarar", c.lab(h.SiteID)}, {"Cuenta con equipo propio", yesNo(h.HasEquipment)}}},
			{Title: "Estructura salarial", Fields: pay},
		}
		if h.Notes != "" {
			doc.Sections = append(doc.Sections, ports.FormSection{Title: "Observaciones", Fields: []ports.FormField{{"Notas", h.Notes}}})
		}
		doc.Sections = append(doc.Sections, ports.FormSection{Title: "Aprobación", Fields: []ports.FormField{
			{"Gestor de servicios", c.manager}, {"Fecha de emisión", c.issued.Format("02/01/2006")}}})
	case "EQUIPMENT":
		e := c.forms.Equipment
		if e == nil {
			e = &EquipmentRequest{}
		}
		types := []string{}
		for _, t := range e.Types {
			types = append(types, c.lab(t))
		}
		annex := map[string]string{"FIXED": "Fijo", "SOFTPHONE": "Softphone"}[e.Annex]
		rows := [][]string{}
		for _, sw := range e.Software {
			rows = append(rows, []string{sw.Product, sw.Version})
		}
		doc.Sections = []ports.FormSection{
			{Title: "Solicitante", Fields: []ports.FormField{{"Gestor de servicios", c.manager}, {"Fecha de solicitud", fmtDate(e.RequestDate)},
				{"Fecha de entrega", fmtDate(e.DeliveryDate)}}},
			{Title: "Colaborador", Fields: []ports.FormField{{"Nombres y apellidos", c.talentName()}, {"Documento", c.document()},
				{"Teléfono", str(c.req, "talent", "phone")}, {"Cliente", client}, {"Área", c.lab(h.AreaID)}, {"Cargo", h.Position},
				{"Fecha de inicio", fmtDate(h.StartDate)}}},
			{Title: "Hardware", Fields: []ports.FormField{{"Tipo de equipo", strings.Join(types, ", ")}, {"Procesador", e.Processor},
				{"Memoria RAM", e.RAM}, {"Disco duro", e.Disk}, {"Marca", e.Brand}, {"Anexo", annex}, {"Celular", yesNo(e.Mobile)},
				{"Internet móvil", yesNo(e.MobileInternet)}, {"Accesorios", e.Accessories}}},
			{Title: "Software", Header: []string{"Producto", "Versión"}, Rows: rows},
		}
		if e.Notes != "" {
			doc.Sections = append(doc.Sections, ports.FormSection{Title: "Observaciones", Fields: []ports.FormField{{"Notas", e.Notes}}})
		}
	case "USERS":
		u := c.forms.Users
		if u == nil {
			u = &UserRequest{}
		}
		doc.Sections = []ports.FormSection{
			{Title: "Solicitante", Fields: []ports.FormField{{"Nombre", c.manager}, {"Cargo", "Gestor de servicios"},
				{"Fecha de solicitud", c.issued.Format("02/01/2006")}}},
			{Title: "Creación de usuarios", Fields: []ports.FormField{{"Nombres y apellidos", c.talentName()}, {"Nombre de usuario", u.Username},
				{"Correo", u.Email}, {"Área", c.lab(h.AreaID)}, {"Cargo", h.Position}, {"Cliente", client}, {"Fecha de inicio", fmtDate(h.StartDate)}}},
		}
		if u.Notes != "" {
			doc.Sections = append(doc.Sections, ports.FormSection{Title: "Observaciones", Fields: []ports.FormField{{"Notas", u.Notes}}})
		}
	}
	return doc
}

// context arma lo necesario para imprimir. forms nil = las del ingreso confirmado o, si no hay, las del borrador.
func (s *SelectionService) formContext(ctx context.Context, p *domain.Principal, tid uint64, requestID string, forms *HireForms, internal bool) (*formContext, error) {
	doc, err := s.Repo.Hire(ctx, meta(p, ""), tid, requestID)
	if err != nil {
		return nil, err
	}
	var req map[string]any
	if err := json.Unmarshal(decryptFields(s.Cipher, decryptNames(s.Cipher, doc), "docNumberEnc", "emailEnc", "phoneEnc"), &req); err != nil {
		return nil, err
	}
	withPay := internal || p.Has("employee.salary.read")
	if forms == nil {
		if forms, err = s.formsOfHire(ctx, p, tid, requestID); err != nil {
			return nil, err
		}
		if forms == nil {
			if forms, _, err = s.draftOf(ctx, p, tid, requestID, true); err != nil {
				return nil, err
			}
		}
		if forms == nil {
			forms = &HireForms{}
		}
	} else if forms.Hire.Pay == nil && withPay {
		if d, _, err := s.draftOf(ctx, p, tid, requestID, true); err == nil && d != nil {
			forms.Hire.Pay = d.Hire.Pay
		}
	}
	ids := []string{forms.Hire.ContractID, forms.Hire.ReasonID, forms.Hire.AreaID, forms.Hire.SiteID, forms.Hire.CurrencyID}
	if forms.Equipment != nil {
		ids = append(ids, forms.Equipment.Types...)
	}
	labels, err := s.Repo.CatalogLabels(ctx, tid, nonEmpty(ids))
	if err != nil {
		return nil, err
	}
	st, err := s.settings(ctx, tid)
	if err != nil {
		return nil, err
	}
	manager := p.FullName
	if manager == "" {
		manager = str(req, "assignedName")
	}
	return &formContext{req: req, forms: forms, labels: labels, settings: st, manager: manager, issued: time.Now().In(limaTZ()), withPay: withPay}, nil
}

func limaTZ() *time.Location {
	if loc, err := time.LoadLocation("America/Lima"); err == nil {
		return loc
	}
	return time.UTC
}

// FormPDF imprime una ficha: la confirmada, la del borrador o (preview) la que se está editando.
func (s *SelectionService) FormPDF(ctx context.Context, p *domain.Principal, requestID, form string, preview *HireForms) ([]byte, string, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, "", err
	}
	if !validUUID(requestID) || !contains(FormCodes, form) || s.Renderer == nil {
		return nil, "", domain.ErrValidation
	}
	if preview != nil {
		if err := preview.validate(false); err != nil {
			return nil, "", err
		}
	}
	c, err := s.formContext(ctx, p, tid, requestID, preview, false)
	if err != nil {
		return nil, "", err
	}
	b, err := s.Renderer.Render(c.build(form))
	if err != nil {
		return nil, "", err
	}
	return b, fileName(form, c.talentName()), nil
}

func fileName(form, name string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case strings.ContainsRune("áéíóúÁÉÍÓÚñÑüÜ", r):
			return r
		}
		return '_'
	}, name)
	return formMeta[form].File + "_" + clean + ".pdf"
}

// ---------------- Envío ----------------

func (s *SelectionService) dispatchAsync(p *domain.Principal, tid uint64, requestID, reason string, only []string) {
	if s.Renderer == nil || s.Mail == nil {
		return
	}
	pc := *p
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := s.dispatch(ctx, &pc, tid, requestID, reason, only); err != nil {
			slog.Warn("no se pudieron enviar las fichas", "request", requestID, "reason", reason, "err", err)
		}
	}()
}

var reasonSubject = map[string]string{
	"CONFIRMED": "Ingreso nuevo talento", "UPDATED": "Actualización de ingreso", "RESENT": "Reenvío de ingreso", "REVOKED": "Ingreso revocado",
}

func (s *SelectionService) dispatch(ctx context.Context, p *domain.Principal, tid uint64, requestID, reason string, only []string) error {
	c, err := s.formContext(ctx, p, tid, requestID, nil, true)
	if err != nil {
		return err
	}
	var revokeReason string
	if reason == "REVOKED" {
		if m, err := s.hireOf(ctx, p, tid, requestID); err == nil && m != nil {
			revokeReason = str(m, "revokeReason")
		}
	}
	forms := []string{"FMI"}
	if c.forms.Equipment != nil {
		forms = append(forms, "EQUIPMENT")
	}
	if c.forms.Users != nil {
		forms = append(forms, "USERS")
	}
	cc := []string{}
	if c.settings.CCManager && p.Email != "" {
		cc = append(cc, p.Email)
	}
	rq, client := str(c.req, "requirement", "code"), str(c.req, "client", "label")
	link := strings.TrimRight(s.AppURL, "/") + "/" + p.TenantSlug + "/solicitudes/" + requestID
	for _, form := range forms {
		if len(only) > 0 && !contains(only, form) {
			continue
		}
		to := c.settings.Recipients[form]
		if len(to) == 0 {
			_ = s.Repo.HireFormDispatchAdd(ctx, p.UserID, tid, requestID, form, reason, nil, "NO_RECIPIENTS", "")
			continue
		}
		m := formMeta[form]
		subject := fmt.Sprintf("%s | %s | %s | %s · %s", reasonSubject[reason], rq, client, m.Code, c.talentName())
		var text strings.Builder
		fmt.Fprintf(&text, "%s\n\nColaborador: %s\nCliente: %s\nRequerimiento: %s\nCargo: %s\nFecha de inicio: %s\n",
			m.Title, c.talentName(), client, rq, c.forms.Hire.Position, fmtDate(c.forms.Hire.StartDate))
		mailMsg := ports.Mail{To: to, Cc: cc, Subject: subject}
		if reason == "REVOKED" {
			fmt.Fprintf(&text, "\nEl ingreso fue revocado. Motivo: %s\nNo es necesario atender la solicitud adjunta anteriormente.\n", revokeReason)
		} else {
			pdf, err := s.Renderer.Render(c.build(form))
			if err != nil {
				_ = s.Repo.HireFormDispatchAdd(ctx, p.UserID, tid, requestID, form, reason, to, "FAILED", err.Error())
				continue
			}
			if reason == "UPDATED" {
				text.WriteString("\nSe actualizaron los datos del ingreso; la ficha adjunta reemplaza a la anterior.\n")
			}
			mailMsg.Attachments = []ports.MailAttachment{{Name: fileName(form, c.talentName()), ContentType: "application/pdf", Data: pdf}}
		}
		fmt.Fprintf(&text, "\nGestor de servicios: %s\nDetalle en IGDT: %s\n", c.manager, link)
		mailMsg.Text = text.String()
		status, errText := "SENT", ""
		if err := s.Mail.Send(ctx, mailMsg); err != nil {
			status, errText = "FAILED", err.Error()
			if err == ports.ErrNoMailer {
				status, errText = "NO_MAILER", ""
			}
		}
		_ = s.Repo.HireFormDispatchAdd(ctx, p.UserID, tid, requestID, form, reason, to, status, errText)
	}
	return nil
}

// ---------------- Perfil del candidato (lectura de BDT) ----------------

func (s *SelectionService) bdtKey(ctx context.Context, tid uint64) (string, error) {
	if s.BDT == nil || !s.BDT.Enabled() {
		return "", domain.NewError("BDT_NOT_LINKED")
	}
	enc, err := s.Repo.BDTLinkGet(ctx, tid)
	if err != nil {
		return "", err
	}
	if len(enc) == 0 {
		return "", domain.NewError("BDT_NOT_LINKED")
	}
	return s.Cipher.Decrypt(enc)
}

// Talent: perfil completo del candidato en BDT (experiencia, estudios, idiomas, habilidades, CV y postulaciones).
func (s *SelectionService) Talent(ctx context.Context, p *domain.Principal, talentID string) (json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	if !validUUID(talentID) {
		return nil, domain.ErrValidation
	}
	key, err := s.bdtKey(ctx, tid)
	if err != nil {
		return nil, err
	}
	return s.BDT.Talent(ctx, key, talentID)
}

// TalentFile: CV del candidato (original o con marca) desde BDT. Solo PDF, Word o imagen.
func (s *SelectionService) TalentFile(ctx context.Context, p *domain.Principal, talentID, fileID string) (string, io.ReadCloser, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return "", nil, err
	}
	if !validUUID(talentID) || !validUUID(fileID) {
		return "", nil, domain.ErrValidation
	}
	key, err := s.bdtKey(ctx, tid)
	if err != nil {
		return "", nil, err
	}
	ct, body, err := s.BDT.TalentFile(ctx, key, talentID, fileID)
	if err != nil {
		return "", nil, err
	}
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	allowed := map[string]bool{"application/pdf": true, "application/msword": true, "image/png": true, "image/jpeg": true,
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true}
	if !allowed[base] {
		body.Close()
		return "", nil, domain.ErrNotFound
	}
	return base, body, nil
}

// ---------------- Enlace con BDT (API de integración) ----------------

func (s *SelectionService) LinkBDT(ctx context.Context, c *domain.ApiClient, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if !strings.HasPrefix(apiKey, "bdt_") || len(apiKey) > 200 || len(apiKey) < 30 {
		return domain.ErrValidation
	}
	enc, err := s.Cipher.Encrypt(apiKey)
	if err != nil {
		return err
	}
	return s.Repo.BDTLinkSet(ctx, c.TenantID, c.KeyID, enc)
}

func (s *SelectionService) UnlinkBDT(ctx context.Context, c *domain.ApiClient) error {
	return s.Repo.BDTLinkClear(ctx, c.TenantID, c.KeyID)
}

func (s *SelectionService) BDTLinked(ctx context.Context, tid uint64) (bool, error) {
	enc, err := s.Repo.BDTLinkGet(ctx, tid)
	return len(enc) > 0, err
}

// mailerEnabled: el proveedor de correo está configurado (si el adaptador lo informa).
func mailerEnabled(m ports.MailSender) bool {
	if e, ok := m.(interface{ Enabled() bool }); ok {
		return e.Enabled()
	}
	return false
}
