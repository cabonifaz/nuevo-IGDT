package app

import (
	"fmt"
	"strings"
)

// Correos del fin de contrato, con el mismo formato que los de ingreso:
//   - FMI (renovación, baja, traspaso o cambio de RQ) y NOTICE (Selección debe gestionar el ingreso en el nuevo RQ en BDT):
//     estilo de los correos del FMI (saludo al solicitante y tabla con el color de las fichas).
//   - USERS (baja o modificación de credenciales) y RECOVERY (recuperación de equipo): estilo de los correos a Soporte
//     (logo, «Favor atender…», tabla, «Slds» y pie de bandeja desatendida).

type mailParts struct {
	b, t    strings.Builder
	color   string
	company string
}

func newMailParts(c *formContext) *mailParts {
	m := &mailParts{color: "#2F5496"}
	if c.print != nil {
		if c.print.style.Primary != "" {
			m.color = c.print.style.Primary
		}
		m.company = strings.ToUpper(strings.TrimSpace(strings.Split(c.print.company, " - ")[0]))
	}
	fmt.Fprintf(&m.b, `<div style="%s">`, mailFont)
	return m
}

func (m *mailParts) p(text string) {
	fmt.Fprintf(&m.b, "<p>%s</p>", esc(text))
	fmt.Fprintf(&m.t, "%s\n\n", text)
}

func (m *mailParts) strong(text, color string) {
	style := ""
	if color != "" {
		style = fmt.Sprintf(` style="color:%s"`, color)
	}
	fmt.Fprintf(&m.b, "<p><b%s>%s</b></p>", style, esc(text))
	fmt.Fprintf(&m.t, "%s\n\n", text)
}

// table: encabezados y una o más filas (valores en texto plano; se escapan).
func (m *mailParts) table(head []string, rows [][]string) {
	escaped := make([][]string, len(rows))
	for i, r := range rows {
		escaped[i] = make([]string, len(r))
		for j, v := range r {
			escaped[i][j] = esc(v)
			if i == 0 || len(rows) == 1 {
				fmt.Fprintf(&m.t, "%s: %s\n", head[j], v)
			}
		}
		if i > 0 && len(rows) > 1 {
			fmt.Fprintf(&m.t, "- %s\n", strings.Join(r, " · "))
		}
	}
	m.b.WriteString(mailTable(m.color, head, escaped))
	m.t.WriteString("\n")
}

func (m *mailParts) greet(greet string) {
	if greet != "" {
		m.p(fmt.Sprintf("Estimado %s,", greet))
	} else {
		m.p("Estimados,")
	}
}

func (m *mailParts) closeFMI() (string, string) {
	m.b.WriteString("<p>Saludos,</p></div>")
	m.t.WriteString("Saludos,\n")
	return m.b.String(), m.t.String()
}

func (m *mailParts) closeSupport() (string, string) {
	m.b.WriteString("<p>Slds</p>")
	m.t.WriteString("Slds\n")
	if m.company != "" {
		footer := fmt.Sprintf("Este correo ha sido enviado automáticamente desde %s. Esta es una bandeja DESATENDIDA; por favor, no respondas a este mensaje.", m.company)
		fmt.Fprintf(&m.b, `<hr style="border:0;border-top:1px solid #ccc"><p style="font-size:12px;color:#555">%s</p>`, esc(footer))
		fmt.Fprintf(&m.t, "\n%s\n", footer)
	}
	m.b.WriteString("</div>")
	return m.b.String(), m.t.String()
}

func endText(d string) string {
	if d == "" {
		return "Indeterminado"
	}
	return fmtDate(d)
}

var mailPrefix = map[string]string{"RESENT": "Reenvío - ", "RESCHEDULED": "Cambio de fecha - ", "OP_CANCELLED": "Anulado - "}

// buildContractMail arma asunto, HTML y texto de un documento de la operación. reason RESENT = reenvío; RESCHEDULED =
// cambio de fecha de una baja programada; OP_CANCELLED = baja anulada (aviso sin adjuntos).
func buildContractMail(form, reason string, op *contractOpData, c *formContext, greet string) (subject, htmlBody, text string) {
	in, b := op.Input, op.Before
	rq, client := b.RQ, b.Client
	if rq == "" {
		rq = str(c.req, "requirement", "code")
	}
	if client == "" {
		client = str(c.req, "client", "label")
	}
	first := str(c.req, "talent", "firstName")
	last := strings.TrimSpace(str(c.req, "talent", "lastName") + " " + str(c.req, "talent", "secondLastName"))
	name := c.talentName()
	doc := str(c.req, "talent", "docNumber")
	prefix := mailPrefix[reason]
	m := newMailParts(c)
	recover := recoverText(op)
	supportHead := func() {
		if c.print != nil && len(c.print.logo) > 0 {
			m.b.WriteString(`<p><img src="cid:logo" alt="" style="max-height:60px;max-width:220px"></p>`)
		}
		m.p("Estimados,")
	}
	changeNote := func() {
		if ch := op.change; ch != nil && reason == "RESCHEDULED" {
			m.strong(fmt.Sprintf("Se cambió la fecha de cese del %s al %s. Motivo: %s. Este correo reemplaza al enviado anteriormente.",
				fmtDate(ch.From), fmtDate(ch.To), ch.Reason), "#B54708")
		}
	}
	scheduled := func() {
		if op.Type == OpTerminate && op.Scheduled {
			m.strong(fmt.Sprintf("Baja programada: se hace efectiva el %s.", fmtDate(in.Date)), "")
		}
	}
	// baja programada anulada: aviso corto a los mismos destinatarios
	if reason == "OP_CANCELLED" {
		what := map[string]string{"FMI": "Baja de colaborador", "USERS": "Solicitud de baja de credenciales", "RECOVERY": "Solicitud de recuperación de equipo"}[form]
		subject = fmt.Sprintf("%s%s | %s | %s", prefix, what, rq, client)
		from, why := in.Date, ""
		if op.change != nil {
			from, why = op.change.From, op.change.Reason
		}
		if form == "FMI" {
			m.greet(greet)
		} else {
			supportHead()
		}
		m.strong(fmt.Sprintf("Se anuló la baja programada de %s (cese previsto el %s). Motivo: %s.", name, fmtDate(from), why), "#B42318")
		m.p("No es necesario atender la solicitud enviada anteriormente; el colaborador continúa en el servicio.")
		m.table([]string{"Nombres", "Apellidos", "Doc. Identidad", "Cargo", "Cliente", "Cese Anulado"}, [][]string{{first, last, doc, b.Position, client, fmtDate(from)}})
		if form == "FMI" {
			htmlBody, text = m.closeFMI()
		} else {
			htmlBody, text = m.closeSupport()
		}
		return subject, htmlBody, text
	}
	switch form {
	case "FMI":
		m.greet(greet)
		changeNote()
		switch op.Type {
		case OpRenew:
			subject = fmt.Sprintf("%sRenovación de contrato | %s | %s", prefix, rq, client)
			m.p(fmt.Sprintf("Comunicarte que se renovó el contrato de %s en el equipo %s hasta el %s.", name, client, fmtDate(in.EndDate)))
			m.p("Comparto el Formulario de Ingreso (FMI) actualizado con la nueva fecha de término.")
			m.table([]string{"Item", "Nombres", "Apellidos", "Doc. Identidad", "Cargo", "Inicio de Labores", "Término Anterior", "Nuevo Término", "Motivo"},
				[][]string{{"1", first, last, doc, b.Position, fmtDate(b.StartDate), endText(b.EndDate), fmtDate(in.EndDate), in.Reason}})
		case OpTerminate:
			subject = fmt.Sprintf("%sBaja de colaborador | %s | %s", prefix, rq, client)
			m.p(fmt.Sprintf("Comunicarte la baja de %s del equipo %s con fecha de cese %s.", name, client, fmtDate(in.Date)))
			scheduled()
			m.p("Adjunto el Formulario de Movimiento (FMI) con la sección de cese. Se solicitó a Soporte la baja de las credenciales (usuario de red y correo).")
			if recover != "" {
				m.strong("Recuperar laptop/equipo: "+recover+" Se solicitó a Soporte la recuperación del equipo.", "#B42318")
			} else {
				m.p("No se requiere recuperar equipo.")
			}
			ret, rec := "—", "No"
			if in.RecoverEquipment {
				ret, rec = fmtDate(in.EquipmentReturnDate), "Sí"
			}
			m.table([]string{"Item", "Nombres", "Apellidos", "Doc. Identidad", "Cargo", "Inicio de Labores", "Fecha de Cese", "Motivo de Cese", "Recuperar Equipo", "Devolución de Equipo"},
				[][]string{{"1", first, last, doc, b.Position, fmtDate(b.StartDate), fmtDate(in.Date), op.Labels.TerminationReason, rec, ret}})
			if in.Reason != "" {
				m.p("Observaciones: " + in.Reason)
			}
		case OpTransfer:
			subject = fmt.Sprintf("%sTraspaso de área | %s | %s", prefix, rq, client)
			m.p(fmt.Sprintf("Comunicarte el traspaso de %s del área %s al área %s en el equipo %s a partir del %s.", name, b.Area, op.Labels.Area, client, fmtDate(in.Date)))
			m.p("Adjunto el Formulario de Movimiento (FMI) con la sección de movimiento.")
			site := op.Labels.Site
			if site == "" {
				site = b.Site
			}
			payText := "Sin cambio"
			if op.PayEnc != "" {
				payText = "Actualizada (ver FMI)"
			}
			m.table([]string{"Item", "Nombres", "Apellidos", "Doc. Identidad", "Área Actual", "Nueva Área", "Cargo", "Jornada", "Sede", "Remuneración", "Inicio del Movimiento", "Término de Contrato"},
				[][]string{{"1", first, last, doc, b.Area, op.Labels.Area, in.Position, in.Schedule, orDash(site), payText, fmtDate(in.Date), endText(in.EndDate)}})
			if in.ModifyCredentials {
				m.p("Se solicitó a Soporte la modificación de las credenciales del colaborador.")
			}
			if in.Reason != "" {
				m.p("Observaciones: " + in.Reason)
			}
		case OpRQChange:
			codes := op.rqCodes()
			subject = fmt.Sprintf("%sCambio de requerimiento | %s → %s | %s", prefix, rq, codes, client)
			m.p(fmt.Sprintf("Comunicarte que %s deja el servicio %s (%s) el %s para pasar a: %s.", name, client, rq, fmtDate(in.Date), rqList(op)))
			m.p("Adjunto el Formulario de Movimiento (FMI) de salida del servicio actual. El ingreso al nuevo requerimiento se gestiona en BDT.")
			m.table([]string{"Item", "Nombres", "Apellidos", "Doc. Identidad", "Cargo", "RQ Actual", "Nuevos RQ", "Fecha de Salida"},
				[][]string{{"1", first, last, doc, b.Position, rq, codes, fmtDate(in.Date)}})
		}
		htmlBody, text = m.closeFMI()
	case "NOTICE":
		subject = fmt.Sprintf("%sGestionar ingreso en BDT: %s → %s", prefix, name, op.rqCodes())
		m.greet(greet)
		m.p(fmt.Sprintf("%s pasará a %s a partir del %s; su contrato en %s (%s) se cerró en IGDT.", name, rqList(op), fmtDate(in.Date), client, rq))
		m.strong("Por favor, gestionar en BDT el ingreso del talento en esos requerimientos (agregarlo y confirmarlo como candidato) para que llegue a IGDT la nueva solicitud de ingreso.", "")
		rows := [][]string{}
		for _, r := range rqsOf(op) {
			rows = append(rows, []string{name, doc, rq, r.Code, r.Title, r.Client, fmtDate(in.Date)})
		}
		m.table([]string{"Talento", "Doc. Identidad", "RQ Actual", "Nuevo RQ", "Título", "Cliente", "Fecha"}, rows)
		htmlBody, text = m.closeFMI()
	case "USERS":
		supportHead()
		changeNote()
		user, email := "", ""
		if op.Users != nil {
			user, email = op.Users.Username, op.Users.Email
		}
		if op.Type == OpTransfer {
			subject = fmt.Sprintf("%sSolicitud de modificación de usuario: %s - %s", prefix, client, rq)
			m.p(fmt.Sprintf("Favor atender la modificación de las credenciales de %s por su traspaso al área %s a partir del %s.", name, op.Labels.Area, fmtDate(in.Date)))
			m.table([]string{"Item", "Nombres", "Apellidos", "Usuario Actual", "Nuevo Usuario", "Correo Actual", "Nuevo Correo"},
				[][]string{{"1", first, last, orDash(user), orDash(in.NewUsername), orDash(email), orDash(in.NewEmail)}})
		} else {
			subject = fmt.Sprintf("%sSolicitud de baja de credenciales: %s - %s", prefix, client, rq)
			m.p(fmt.Sprintf("Favor atender la desactivación del usuario de red y del correo de %s a partir del %s (fecha de cese).", name, fmtDate(in.Date)))
			scheduled()
			m.table([]string{"Item", "Nombres", "Apellidos", "Usuario", "Correo", "Fecha de Cese"},
				[][]string{{"1", first, last, orDash(user), orDash(email), fmtDate(in.Date)}})
		}
		htmlBody, text = m.closeSupport()
	case "RECOVERY":
		subject = fmt.Sprintf("%sSolicitud de recuperación de equipo: %s - %s", prefix, client, rq)
		supportHead()
		changeNote()
		m.p(fmt.Sprintf("Favor coordinar la recuperación del equipo asignado a %s por su cese el %s. Fecha de devolución: %s.", name, fmtDate(in.Date),
			fmtDate(in.EquipmentReturnDate)))
		scheduled()
		rows := [][]string{}
		for i, e := range op.Equipment {
			rows = append(rows, []string{fmt.Sprint(i + 1), e.Label, orDash(e.Detail), orDash(fmtDate(e.DeliveredAt))})
		}
		if len(rows) == 0 {
			rows = append(rows, []string{"1", "Equipo asignado al colaborador", "Según la solicitud de equipo", "—"})
		}
		m.table([]string{"Item", "Equipo", "Detalle", "Entregado"}, rows)
		m.table([]string{"Nombres", "Apellidos", "Correo", "Celular", "Fecha de Devolución"},
			[][]string{{first, last, str(c.req, "talent", "email"), str(c.req, "talent", "phone"), fmtDate(in.EquipmentReturnDate)}})
		htmlBody, text = m.closeSupport()
	}
	return subject, htmlBody, text
}

// rqsOf: RQ destino (las operaciones de v0.13 guardaban uno solo en rqCode/rqTitle/rqClient).
func rqsOf(op *contractOpData) []rqLabel {
	if len(op.Labels.RQs) > 0 {
		return op.Labels.RQs
	}
	return []rqLabel{{Code: op.Labels.RQCode, Title: op.Labels.RQTitle, Client: op.Labels.RQClient}}
}

// rqList: «RQ-1 – Título (Cliente), RQ-2 – …».
func rqList(op *contractOpData) string {
	out := []string{}
	for _, r := range rqsOf(op) {
		out = append(out, strings.TrimSpace(fmt.Sprintf("%s %s (%s)", r.Code, dashed(r.Title), orDash(r.Client))))
	}
	return strings.Join(out, ", ")
}
