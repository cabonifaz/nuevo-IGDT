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
	req       map[string]any // solicitud (datos personales descifrados)
	forms     *HireForms
	labels    map[string]ports.CatalogLabel
	settings  *HireFormSettings
	manager   string
	issued    time.Time
	withPay   bool
	print     *printStyle // formato y logo del tenant
	signature []byte      // firma del gestor (PNG)
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
	return fmt.Sprintf("%s © %d — Fecha de emisión: %s", c.print.style.FooterText, c.issued.Year(), longDateES(c.issued))
}

// header: datos del formato configurados por el tenant (código, versión, fecha y título).
func (c *formContext) header(form string) ports.FormHeader {
	h := c.print.style.Forms[form]
	return ports.FormHeader{Code: h.Code, Version: h.Version, Date: h.Date, Title: h.Title}
}

// currencySymbol: como en las fichas originales ("S/.565.00").
func currencySymbol(code string) string {
	switch code {
	case "PEN":
		return "S/."
	case "USD":
		return "US$"
	case "EUR":
		return "€"
	case "":
		return ""
	}
	return code + " "
}

func amount(sym string, v float64) string {
	if v <= 0 {
		return ""
	}
	return sym + formatAmount(v)
}

func (c *formContext) build(form string) ports.FormPrint {
	st := c.print.style
	h := c.forms.Hire
	out := ports.FormPrint{Form: form, Header: c.header(form), Logo: c.print.logo, LogoType: c.print.logoType,
		Company: c.print.company, Signer: c.manager, Signature: c.signature, SignerPos: st.SignerTitle, Footer: c.footer(),
		Style: ports.FormStyle{Primary: st.Primary, HeaderText: st.HeaderText, Border: st.Border, LabelBg: st.LabelBg, Title: st.Title}}
	client := str(c.req, "client", "label")
	switch form {
	case "FMI":
		contract := c.labels[h.ContractID]
		rxh := contract.Str1 == "RXH"
		sym := currencySymbol(c.labels[h.CurrencyID].Code)
		f := &ports.FMIPrint{Name: c.talentName(), Client: client, Modality: contract.Label, Reason: c.lab(h.ReasonID),
			Position: h.Position, Schedule: h.Schedule, Start: fmtDate(h.StartDate), End: fmtDate(h.EndDate),
			Project: h.ProjectService, Object: h.ContractObject, Sunat: yesNo(h.SunatDeclared && !rxh), Notes: h.Notes, Locator: rxh}
		if h.EndDate == "" {
			f.End = "Indeterminado"
		}
		if h.SunatDeclared && !rxh {
			f.Site = c.lab(h.SiteID)
		}
		switch {
		case !c.withPay:
			f.Base = "Reservado"
		case h.Pay != nil && rxh:
			f.Base, f.HasBase = amount(sym, h.Pay.Fee), h.Pay.Fee > 0
		case h.Pay != nil:
			f.Base, f.HasBase = amount(sym, h.Pay.Base), h.Pay.Base > 0
			f.Mobility, f.HasMobility = amount(sym, h.Pay.Mobility), h.Pay.Mobility > 0
			f.Quarterly = h.Pay.Quarterly > 0
			bonus := []string{}
			if h.Pay.Quarterly > 0 {
				bonus = append(bonus, amount(sym, h.Pay.Quarterly))
			}
			if h.Pay.Semiannual > 0 {
				bonus = append(bonus, amount(sym, h.Pay.Semiannual)+" (semestral)")
			}
			f.Bonus = strings.Join(bonus, " · ")
		}
		out.FMI = f
	case "EQUIPMENT":
		e := c.forms.Equipment
		if e == nil {
			e = &EquipmentRequest{}
		}
		types := []ports.FormCheck{{Label: "PC"}, {Label: "Laptop"}}
		for _, t := range e.Types {
			l := c.labels[t]
			key := strings.ToUpper(l.Code + " " + l.Label)
			switch {
			case strings.Contains(key, "LAPTOP") || strings.Contains(key, "PORTÁTIL") || strings.Contains(key, "PORTATIL"):
				types[1].On = true
			case l.Code == "PC" || strings.Contains(key, "PC") || strings.Contains(key, "ESCRITORIO") || strings.Contains(key, "DESKTOP"):
				types[0].On = true
			case l.Label != "":
				types = append(types, ports.FormCheck{Label: l.Label, On: true})
			}
		}
		doc := str(c.req, "talent", "docNumber")
		if dt := str(c.req, "talent", "docType", "label"); dt != "" && !strings.EqualFold(dt, "DNI") && doc != "" {
			doc = dt + " " + doc
		}
		sw := [][2]string{}
		for _, x := range e.Software {
			sw = append(sw, [2]string{x.Product, x.Version})
		}
		out.Equipment = &ports.EquipmentPrint{Name: c.talentName(), Client: client, Area: c.lab(h.AreaID), Position: h.Position,
			DocNumber: doc, Phone: str(c.req, "talent", "phone"), RequestDate: fmtDate(e.RequestDate), DeliveryDate: fmtDate(e.DeliveryDate),
			Types: types, Processor: e.Processor, RAM: e.RAM, Disk: e.Disk, Brand: e.Brand,
			AnnexFixed: e.Annex == "FIXED", AnnexSoftphone: e.Annex == "SOFTPHONE", Mobile: e.Mobile, MobileInternet: e.MobileInternet,
			Accessories: e.Accessories, Notes: e.Notes, Software: sw}
	case "USERS":
		u := c.forms.Users
		if u == nil {
			u = &UserRequest{}
		}
		area, annex := u.RequesterArea, u.RequesterAnnex
		if area == "" {
			area = c.lab(h.AreaID)
		}
		if annex == "" {
			annex = st.RequesterAnnex
		}
		out.Users = &ports.UsersPrint{RequesterName: c.manager, RequesterArea: area, RequesterPosition: st.SignerTitle,
			RequesterAnnex: annex, Date: c.issued.Format("02/01/2006"), Name: c.talentName(), Username: u.Username,
			Email: u.Email, Area: c.lab(h.AreaID), Notes: u.Notes}
	}
	return out
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
	// firma quien confirmó el ingreso (o lo corrigió por última vez); en borradores y vistas previas, el usuario actual
	signerID, signerName := "", ""
	if forms == nil {
		hm, err := s.hireOf(ctx, p, tid, requestID)
		if err != nil {
			return nil, err
		}
		if hm != nil {
			forms = s.formsFromHire(hm)
			signerID, signerName = str(hm, "signerId"), str(hm, "signerName")
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
	var sig []byte
	if signerID != "" {
		if signerName != "" {
			manager = signerName
		}
		sig, err = s.signature(ctx, tid, 0, signerID)
	} else {
		sig, err = s.signature(ctx, tid, p.UserID, "")
	}
	if err != nil {
		return nil, err
	}
	ps, err := s.printStyle(ctx, tid, nil)
	if err != nil {
		return nil, err
	}
	return &formContext{req: req, forms: forms, labels: labels, settings: st, manager: manager, signature: sig, issued: time.Now().In(limaTZ()), withPay: withPay, print: ps}, nil
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
		m := c.header(form)
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

// TalentCVSource: insumos de BDT para generar el CV con marca (es | en) en el navegador, igual que en BDT.
func (s *SelectionService) TalentCVSource(ctx context.Context, p *domain.Principal, talentID, lang string) (json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	if !validUUID(talentID) || (lang != "es" && lang != "en") {
		return nil, domain.ErrValidation
	}
	key, err := s.bdtKey(ctx, tid)
	if err != nil {
		return nil, err
	}
	return s.BDT.CVSource(ctx, key, talentID, lang)
}
