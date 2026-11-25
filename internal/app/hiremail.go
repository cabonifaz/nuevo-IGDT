package app

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// Envío de las fichas: cada ficha sale en su propio correo al aprobarla, con el formato de los correos del FMI.
//   - FMI: para los solicitantes (quien confirmó en BDT y el analista del RQ), con copia a Talento y a las copias fijas.
//   - Usuarios y equipo: para Soporte, con copia a los solicitantes y a las copias fijas.
// El gestor que aprueba va en copia si así está configurado.

// formContextFor: contexto de impresión de una ficha con lo aprobado y la firma de quien la aprobó (la FMI, con el ingreso).
func (s *SelectionService) formContextFor(ctx context.Context, p *domain.Principal, tid uint64, requestID, form string, internal bool) (*formContext, error) {
	approved, err := s.approvedForms(ctx, p, tid, requestID)
	if err != nil {
		return nil, err
	}
	a := approved[form]
	var forms *HireForms
	if form != "FMI" && a != nil && a.Forms != nil {
		cp := *a.Forms
		forms = &cp
	}
	c, err := s.formContext(ctx, p, tid, requestID, forms, internal)
	if err != nil {
		return nil, err
	}
	if form != "FMI" && a != nil && a.SignerID != "" {
		if a.SignerName != "" {
			c.manager = a.SignerName
		}
		if sig, err := s.signature(ctx, tid, 0, a.SignerID); err == nil {
			c.signature = sig
		}
	}
	return c, nil
}

type requester struct{ Name, Email string }

func (c *formContext) requesters() []requester {
	out := []requester{}
	list, _ := c.req["requesters"].([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		e, _ := m["email"].(string)
		n, _ := m["name"].(string)
		if e != "" {
			out = append(out, requester{Name: n, Email: e})
		}
	}
	return out
}

func (c *formContext) condition(key string) bool {
	m, _ := c.req["conditions"].(map[string]any)
	v, _ := m[key].(bool)
	return v
}

func (c *formContext) conditionText(key string) string {
	m, _ := c.req["conditions"].(map[string]any)
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

// dedupe quita repetidos (sin distinguir mayúsculas) y los que ya están en exclude.
func dedupe(list []string, exclude ...[]string) []string {
	seen := map[string]bool{}
	for _, ex := range exclude {
		for _, e := range ex {
			seen[strings.ToLower(e)] = true
		}
	}
	out := []string{}
	for _, e := range list {
		k := strings.ToLower(strings.TrimSpace(e))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, strings.TrimSpace(e))
	}
	return out
}

// recipientsFor: para y copia de una ficha, y el nombre a quien se saluda (el primer solicitante en la FMI).
func (s *SelectionService) recipientsFor(form string, c *formContext, approverEmail string) (to, cc []string, greet string) {
	reqs := c.requesters()
	reqEmails := []string{}
	for _, r := range reqs {
		reqEmails = append(reqEmails, r.Email)
	}
	st := c.settings
	var extra []string
	if st.CCManager && approverEmail != "" {
		extra = []string{approverEmail}
	}
	if form == "FMI" {
		to = dedupe(reqEmails)
		cc = append(append([]string{}, st.TalentEmails...), st.CCEmails...)
		if len(to) == 0 {
			to, cc = dedupe(st.TalentEmails), st.CCEmails
		} else if n := strings.Fields(reqs[0].Name); len(n) > 0 {
			greet = n[0]
		}
	} else {
		to = dedupe(st.SupportEmails)
		cc = append(append([]string{}, reqEmails...), st.CCEmails...)
		if len(to) == 0 {
			to, cc = dedupe(reqEmails), st.CCEmails
		}
	}
	cc = dedupe(append(cc, extra...), to)
	return to, cc, greet
}

func attachName(form, code, name string) string {
	clean := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) {
			return -1
		}
		return r
	}, name)
	return fmt.Sprintf("%s %s - %s.pdf", code, formMeta[form].Title, strings.TrimSpace(clean))
}

func (s *SelectionService) dispatch(ctx context.Context, p *domain.Principal, tid uint64, requestID, reason string, only []string) error {
	approved, err := s.approvedForms(ctx, p, tid, requestID)
	if err != nil {
		return err
	}
	forms := only
	if len(forms) == 0 {
		for _, f := range FormCodes {
			if approved[f] != nil {
				forms = append(forms, f)
			}
		}
	}
	var revokeReason string
	if reason == "REVOKED" {
		if m, err := s.hireOf(ctx, p, tid, requestID); err == nil && m != nil {
			revokeReason = str(m, "revokeReason")
		}
	}
	for _, form := range []string{"FMI", "USERS", "EQUIPMENT"} {
		if !contains(forms, form) || approved[form] == nil {
			continue
		}
		c, err := s.formContextFor(ctx, p, tid, requestID, form, true)
		if err != nil {
			_ = s.Repo.HireFormDispatchAdd(ctx, p.UserID, tid, requestID, form, reason, nil, "FAILED", err.Error())
			continue
		}
		to, cc, greet := s.recipientsFor(form, c, p.Email)
		if len(to) == 0 {
			_ = s.Repo.HireFormDispatchAdd(ctx, p.UserID, tid, requestID, form, reason, nil, "NO_RECIPIENTS", "")
			continue
		}
		msg := ports.Mail{To: to, Cc: cc}
		msg.Subject, msg.HTML, msg.Text = buildFormMail(form, reason, c, greet, revokeReason)
		if form != "FMI" && c.print != nil && len(c.print.logo) > 0 {
			msg.Attachments = append(msg.Attachments, ports.MailAttachment{Name: "logo.png", ContentType: "image/png", Data: c.print.logo, ContentID: "logo"})
		}
		if reason != "REVOKED" {
			pdf, err := s.Renderer.Render(c.build(form))
			if err != nil {
				_ = s.Repo.HireFormDispatchAdd(ctx, p.UserID, tid, requestID, form, reason, append(to, cc...), "FAILED", err.Error())
				continue
			}
			msg.Attachments = append(msg.Attachments, ports.MailAttachment{Name: attachName(form, c.header(form).Code, c.talentName()),
				ContentType: "application/pdf", Data: pdf})
		}
		status, errText := "SENT", ""
		if err := s.Mail.Send(ctx, msg); err != nil {
			status, errText = "FAILED", err.Error()
			if err == ports.ErrNoMailer {
				status, errText = "NO_MAILER", ""
			}
		}
		_ = s.Repo.HireFormDispatchAdd(ctx, p.UserID, tid, requestID, form, reason, append(append([]string{}, to...), cc...), status, errText)
	}
	return nil
}

// ---------------- Plantillas ----------------

const mailFont = "font-family:Arial,Helvetica,sans-serif;font-size:14px;color:#000"

func esc(v string) string { return html.EscapeString(v) }

func mailTable(color string, head []string, rows [][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<table cellspacing="0" cellpadding="6" style="border-collapse:collapse;width:100%%;%s">`, mailFont)
	b.WriteString("<tr>")
	for _, h := range head {
		fmt.Fprintf(&b, `<th style="background:%s;color:#fff;border:1px solid #000;font-weight:bold;text-align:center">%s</th>`, color, esc(h))
	}
	b.WriteString("</tr>")
	for _, r := range rows {
		b.WriteString("<tr>")
		for _, v := range r {
			fmt.Fprintf(&b, `<td style="border:1px solid #000;text-align:center">%s</td>`, v)
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</table>")
	return b.String()
}

func mailLink(email string) string {
	if email == "" {
		return ""
	}
	return fmt.Sprintf(`<a href="mailto:%s">%s</a>`, esc(email), esc(email))
}

// contractTime: tiempo de contrato (el de BDT si lo trae; si no, por las fechas del ingreso).
func (c *formContext) contractTime() string {
	if d := c.conditionText("duration"); d != "" {
		return d
	}
	h := c.forms.Hire
	if h.EndDate == "" {
		return "Indeterminado"
	}
	a, err1 := time.Parse("2006-01-02", dateOnly(h.StartDate))
	b, err2 := time.Parse("2006-01-02", dateOnly(h.EndDate))
	if err1 != nil || err2 != nil {
		return fmtDate(h.EndDate)
	}
	months := (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
	if b.Day() >= a.Day() {
		months++
	}
	return fmt.Sprintf("%d mes(es)", max(months, 1))
}

func (c *formContext) payText() string {
	pay := c.forms.Hire.Pay
	if pay == nil {
		return ""
	}
	v := pay.Base
	if v <= 0 {
		v = pay.Fee
	}
	return amount(currencySymbol(c.labels[c.forms.Hire.CurrencyID].Code), v)
}

// buildFormMail arma asunto, HTML y texto de una ficha según los correos del FMI.
func buildFormMail(form, reason string, c *formContext, greet, revokeReason string) (subject, htmlBody, text string) {
	rq, client := str(c.req, "requirement", "code"), str(c.req, "client", "label")
	first := str(c.req, "talent", "firstName")
	last := strings.TrimSpace(str(c.req, "talent", "lastName") + " " + str(c.req, "talent", "secondLastName"))
	email, phone := str(c.req, "talent", "email"), str(c.req, "talent", "phone")
	color := "#2F5496"
	company := ""
	if c.print != nil {
		if c.print.style.Primary != "" {
			color = c.print.style.Primary
		}
		company = strings.ToUpper(strings.TrimSpace(strings.Split(c.print.company, " - ")[0]))
	}
	prefix := map[string]string{"UPDATED": "Actualización - ", "RESENT": "Reenvío - ", "REVOKED": "Anulado - "}[reason]
	var note string
	switch reason {
	case "UPDATED":
		note = "Se actualizaron los datos del ingreso; la ficha adjunta reemplaza a la enviada anteriormente."
	case "REVOKED":
		note = fmt.Sprintf("Se anuló el ingreso de %s. Motivo: %s. No es necesario atender la solicitud enviada anteriormente.", c.talentName(), revokeReason)
	}
	var b, t strings.Builder
	fmt.Fprintf(&b, `<div style="%s">`, mailFont)
	if form == "FMI" {
		subject = fmt.Sprintf("%sIngreso de nuevo talento | %s | %s", prefix, rq, client)
		if greet != "" {
			fmt.Fprintf(&b, "<p>Estimado %s,</p>", esc(greet))
			fmt.Fprintf(&t, "Estimado %s,\n\n", greet)
		} else {
			b.WriteString("<p>Estimados,</p>")
			t.WriteString("Estimados,\n\n")
		}
		if note != "" {
			fmt.Fprintf(&b, "<p><b>%s</b></p>", esc(note))
			fmt.Fprintf(&t, "%s\n\n", note)
		}
		if reason != "REVOKED" {
			intro := fmt.Sprintf("Comunicarte que se está gestionando el ingreso de un nuevo talento para el equipo %s.", client)
			intro2 := "Comparto sus datos para la elaboración del Formulario de Ingreso y la solicitud de creación de usuario."
			fmt.Fprintf(&b, "<p>%s<br>%s</p>", esc(intro), esc(intro2))
			fmt.Fprintf(&t, "%s\n%s\n\n", intro, intro2)
			check := func(v bool) string {
				if v {
					return "&#9745;"
				}
				return "&#9744;"
			}
			fmt.Fprintf(&b, `<table cellspacing="0" cellpadding="6" style="border-collapse:collapse;width:100%%;%s"><tr><th colspan="2" style="background:%s;color:#fff;border:1px solid #000">Requisitos de ingreso</th></tr>`, mailFont, color)
			for _, r := range []struct {
				label string
				on    bool
			}{{"EMO", c.condition("emo")}, {"SCTR", c.condition("sctr")}, {"Asignación de laptop", c.condition("laptop")}} {
				fmt.Fprintf(&b, `<tr><td style="border:1px solid #000">%s</td><td style="border:1px solid #000;text-align:center;width:12%%;font-size:18px">%s</td></tr>`, esc(r.label), check(r.on))
				fmt.Fprintf(&t, "%s: %s\n", r.label, yesNo(r.on))
			}
			b.WriteString("</table><br>")
			h := c.forms.Hire
			equip := "NO"
			if h.HasEquipment {
				equip = "SÍ"
			}
			b.WriteString(mailTable(color, []string{"Item", "Nombres", "Apellidos", "Doc. Identidad", "Celular", "Correo", "Inicio de Labores",
				"Tiempo de Contrato", "Cargo", "Remuneración", "Modalidad", "Equipo Propio"},
				[][]string{{"1", esc(first), esc(last), esc(str(c.req, "talent", "docNumber")), esc(phone), mailLink(email), esc(fmtDate(h.StartDate)),
					esc(c.contractTime()), esc(h.Position), esc(c.payText()), esc(c.lab(h.ContractID)), equip}}))
			fmt.Fprintf(&t, "\nNombres: %s\nApellidos: %s\nDoc. identidad: %s\nCelular: %s\nCorreo: %s\nInicio de labores: %s\nTiempo de contrato: %s\nCargo: %s\nRemuneración: %s\nModalidad: %s\nEquipo propio: %s\n",
				first, last, str(c.req, "talent", "docNumber"), phone, email, fmtDate(h.StartDate), c.contractTime(), h.Position, c.payText(), c.lab(h.ContractID), equip)
		}
		b.WriteString("<p>Saludos,</p></div>")
		t.WriteString("\nSaludos,\n")
		return subject, b.String(), t.String()
	}
	what := "creación de usuario"
	subject = fmt.Sprintf("%sSolicitud de creación de usuario: %s - %s", prefix, client, rq)
	if form == "EQUIPMENT" {
		what = "equipo"
		subject = fmt.Sprintf("%sSolicitud de equipo: %s - %s", prefix, client, rq)
	}
	if c.print != nil && len(c.print.logo) > 0 {
		b.WriteString(`<p><img src="cid:logo" alt="" style="max-height:60px;max-width:220px"></p>`)
	}
	b.WriteString("<p>Estimados,</p>")
	t.WriteString("Estimados,\n\n")
	if note != "" {
		fmt.Fprintf(&b, "<p><b>%s</b></p>", esc(note))
		fmt.Fprintf(&t, "%s\n\n", note)
	}
	if reason != "REVOKED" {
		fmt.Fprintf(&b, "<p>Favor atender la solicitud de %s.</p>", what)
		fmt.Fprintf(&t, "Favor atender la solicitud de %s.\n\n", what)
	}
	b.WriteString(mailTable(color, []string{"Item", "Nombres", "Apellidos", "Correo", "Celular"},
		[][]string{{"1", esc(first), esc(last), mailLink(email), esc(phone)}}))
	fmt.Fprintf(&t, "Nombres: %s\nApellidos: %s\nCorreo: %s\nCelular: %s\n", first, last, email, phone)
	b.WriteString("<p>Slds</p>")
	t.WriteString("\nSlds\n")
	if company != "" {
		footer := fmt.Sprintf("Este correo ha sido enviado automáticamente desde %s. Esta es una bandeja DESATENDIDA; por favor, no respondas a este mensaje.", company)
		fmt.Fprintf(&b, `<hr style="border:0;border-top:1px solid #ccc"><p style="font-size:12px;color:#555">%s</p>`, esc(footer))
		fmt.Fprintf(&t, "\n%s\n", footer)
	}
	b.WriteString("</div>")
	return subject, b.String(), t.String()
}
