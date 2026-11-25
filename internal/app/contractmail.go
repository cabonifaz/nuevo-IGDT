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

// buildContractMail arma asunto, HTML y texto de un documento de la operación (reason RESENT = reenvío).
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
	prefix := ""
	if reason == "RESENT" {
		prefix = "Reenvío - "
	}
	m := newMailParts(c)
	recover := recoverText(op)
	switch form {
	case "FMI":
		m.greet(greet)
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
			subject = fmt.Sprintf("%sTraspaso de colaborador | %s | %s → %s", prefix, rq, client, op.Labels.Client)
			m.p(fmt.Sprintf("Comunicarte el traspaso de %s del equipo %s al equipo %s a partir del %s.", name, client, op.Labels.Client, fmtDate(in.Date)))
			m.p("Adjunto el Formulario de Movimiento (FMI) con la sección de movimiento.")
			m.table([]string{"Item", "Nombres", "Apellidos", "Doc. Identidad", "Cliente Actual", "Nuevo Cliente", "Área", "Cargo", "Jornada", "Inicio del Movimiento", "Término de Contrato"},
				[][]string{{"1", first, last, doc, client, op.Labels.Client, op.Labels.Area, in.Position, in.Schedule, fmtDate(in.Date), endText(in.EndDate)}})
			if in.ModifyCredentials {
				m.p("Se solicitó a Soporte la modificación de las credenciales del colaborador.")
			}
			if in.Reason != "" {
				m.p("Observaciones: " + in.Reason)
			}
		case OpRQChange:
			subject = fmt.Sprintf("%sCambio de requerimiento | %s → %s | %s", prefix, rq, op.Labels.RQCode, client)
			m.p(fmt.Sprintf("Comunicarte que %s deja el servicio %s (%s) el %s para pasar al requerimiento %s %s (%s).", name, client, rq,
				fmtDate(in.Date), op.Labels.RQCode, dashed(op.Labels.RQTitle), orDash(op.Labels.RQClient)))
			m.p("Adjunto el Formulario de Movimiento (FMI) de salida del servicio actual. El ingreso al nuevo requerimiento se gestiona en BDT.")
			m.table([]string{"Item", "Nombres", "Apellidos", "Doc. Identidad", "Cargo", "RQ Actual", "Nuevo RQ", "Cliente del Nuevo RQ", "Fecha de Salida"},
				[][]string{{"1", first, last, doc, b.Position, rq, op.Labels.RQCode, op.Labels.RQClient, fmtDate(in.Date)}})
		}
		htmlBody, text = m.closeFMI()
	case "NOTICE":
		subject = fmt.Sprintf("%sGestionar ingreso en BDT: %s → %s", prefix, name, op.Labels.RQCode)
		m.greet(greet)
		m.p(fmt.Sprintf("%s pasará al requerimiento %s %s (%s) a partir del %s; su contrato en %s (%s) se cerró en IGDT.", name, op.Labels.RQCode,
			dashed(op.Labels.RQTitle), orDash(op.Labels.RQClient), fmtDate(in.Date), client, rq))
		m.strong("Por favor, gestionar en BDT el ingreso del talento en ese requerimiento (confirmarlo como candidato) para que llegue a IGDT la nueva solicitud de ingreso.", "")
		m.table([]string{"Talento", "Doc. Identidad", "RQ Actual", "Nuevo RQ", "Cliente", "Fecha"},
			[][]string{{name, doc, rq, op.Labels.RQCode, op.Labels.RQClient, fmtDate(in.Date)}})
		htmlBody, text = m.closeFMI()
	case "USERS":
		if c.print != nil && len(c.print.logo) > 0 {
			m.b.WriteString(`<p><img src="cid:logo" alt="" style="max-height:60px;max-width:220px"></p>`)
		}
		m.p("Estimados,")
		user, email := "", ""
		if op.Users != nil {
			user, email = op.Users.Username, op.Users.Email
		}
		if op.Type == OpTransfer {
			subject = fmt.Sprintf("%sSolicitud de modificación de usuario: %s - %s", prefix, client, rq)
			m.p(fmt.Sprintf("Favor atender la modificación de las credenciales de %s por su traspaso al cliente %s a partir del %s.", name, op.Labels.Client, fmtDate(in.Date)))
			m.table([]string{"Item", "Nombres", "Apellidos", "Usuario Actual", "Nuevo Usuario", "Correo Actual", "Nuevo Correo"},
				[][]string{{"1", first, last, orDash(user), orDash(in.NewUsername), orDash(email), orDash(in.NewEmail)}})
		} else {
			subject = fmt.Sprintf("%sSolicitud de baja de credenciales: %s - %s", prefix, client, rq)
			m.p(fmt.Sprintf("Favor atender la desactivación del usuario de red y del correo de %s a partir del %s (fecha de cese).", name, fmtDate(in.Date)))
			m.table([]string{"Item", "Nombres", "Apellidos", "Usuario", "Correo", "Fecha de Cese"},
				[][]string{{"1", first, last, orDash(user), orDash(email), fmtDate(in.Date)}})
		}
		htmlBody, text = m.closeSupport()
	case "RECOVERY":
		subject = fmt.Sprintf("%sSolicitud de recuperación de equipo: %s - %s", prefix, client, rq)
		if c.print != nil && len(c.print.logo) > 0 {
			m.b.WriteString(`<p><img src="cid:logo" alt="" style="max-height:60px;max-width:220px"></p>`)
		}
		m.p("Estimados,")
		m.p(fmt.Sprintf("Favor coordinar la recuperación del equipo asignado a %s por su cese el %s. Fecha de devolución: %s.", name, fmtDate(in.Date),
			fmtDate(in.EquipmentReturnDate)))
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
