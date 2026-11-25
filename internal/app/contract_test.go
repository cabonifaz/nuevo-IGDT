package app

import (
	"bytes"
	"fmt"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cabonifaz/nuevo-IGDT/internal/adapters/forms"
	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

func TestNormalizeAlertDays(t *testing.T) {
	if d, err := normalizeAlertDays(nil); err != nil || len(d) != 3 || d[0] != 30 || d[2] != 7 {
		t.Fatalf("por defecto: %v %v", d, err)
	}
	if d, err := normalizeAlertDays([]int{7, 45, 15, 7}); err != nil || len(d) != 3 || d[0] != 45 || d[1] != 15 || d[2] != 7 {
		t.Fatalf("orden y repetidos: %v %v", d, err)
	}
	for _, bad := range [][]int{{0}, {121}, {1, 2, 3, 4, 5, 6}, {-3}} {
		if _, err := normalizeAlertDays(bad); err == nil {
			t.Errorf("%v debería rechazarse", bad)
		}
	}
	if d, err := normalizeAlertDays([]int{120, 1, 2, 3, 4}); err != nil || len(d) != 5 {
		t.Fatalf("5 valores en rango: %v %v", d, err)
	}
}

func TestContractLevel(t *testing.T) {
	today := time.Date(2026, 11, 25, 9, 30, 0, 0, limaTZ())
	days := []int{30, 15, 7}
	cases := map[string]struct {
		level      string
		left, thre int
	}{
		"2026-12-26": {LevelOK, 31, 0},
		"2026-12-25": {LevelNotice, 30, 30},
		"2026-12-11": {LevelNotice, 16, 30},
		"2026-12-10": {LevelReminder, 15, 15},
		"2026-12-03": {LevelReminder, 8, 15},
		"2026-12-02": {LevelCritical, 7, 7},
		"2026-11-25": {LevelCritical, 0, 7},
		"2026-11-24": {LevelExpired, -1, 0},
	}
	for end, want := range cases {
		lv, left, th := contractLevel(end, today, days)
		if lv != want.level || left != want.left || th != want.thre {
			t.Errorf("%s: %s %d %d, se esperaba %+v", end, lv, left, th, want)
		}
	}
	if lv, _, _ := contractLevel("2026-11-30", today, []int{10}); lv != LevelNotice {
		t.Errorf("un solo umbral: %s", lv)
	}
	if lv, _, _ := contractLevel("2026-11-30", today, []int{15, 7}); lv != LevelCritical {
		t.Errorf("dos umbrales, 5 días: %s", lv)
	}
	if lv, _, _ := contractLevel("", today, days); lv != LevelOK {
		t.Errorf("sin fecha: %s", lv)
	}
}

func code(err error) string {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func TestContractOpValidate(t *testing.T) {
	const start, end = "2026-06-01", "2026-12-31"
	cases := []struct {
		name string
		in   ContractOpInput
		want string
	}{
		{"renovar sin fecha", ContractOpInput{Type: "renew", Reason: "x"}, "CONTRACT_DATE_INVALID"},
		{"renovar antes del fin", ContractOpInput{Type: OpRenew, EndDate: "2026-12-31", Reason: "x"}, "CONTRACT_DATE_INVALID"},
		{"renovar sin motivo", ContractOpInput{Type: OpRenew, EndDate: "2027-06-30"}, "REASON_REQUIRED"},
		{"renovar ok", ContractOpInput{Type: OpRenew, EndDate: "2027-06-30", Reason: " Extensión del servicio ", Position: "x"}, ""},
		{"baja antes del inicio", ContractOpInput{Type: OpTerminate, Date: "2026-05-01", TerminationReasonID: u1}, "CONTRACT_DATE_INVALID"},
		{"baja sin motivo", ContractOpInput{Type: OpTerminate, Date: end}, "VALIDATION"},
		{"baja recuperar sin fecha", ContractOpInput{Type: OpTerminate, Date: end, TerminationReasonID: u1, RecoverEquipment: true}, "CONTRACT_DATE_INVALID"},
		{"baja ok", ContractOpInput{Type: OpTerminate, Date: end, TerminationReasonID: u1, RecoverEquipment: true, EquipmentReturnDate: "2027-01-05"}, ""},
		{"traspaso sin cargo", ContractOpInput{Type: OpTransfer, Date: end, AreaID: u1, Schedule: "L-V"}, "VALIDATION"},
		{"traspaso fin antes del movimiento", ContractOpInput{Type: OpTransfer, Date: end, AreaID: u1, Position: "QA", Schedule: "L-V", EndDate: end}, "CONTRACT_DATE_INVALID"},
		{"traspaso indeterminado", ContractOpInput{Type: OpTransfer, Date: end, AreaID: u1, Position: "QA", Schedule: "L-V", Indefinite: true, EndDate: "2020-01-01"}, ""},
		{"traspaso credenciales sin datos", ContractOpInput{Type: OpTransfer, Date: end, AreaID: u1, Position: "QA", Schedule: "L-V", Indefinite: true, ModifyCredentials: true}, "VALIDATION"},
		{"traspaso correo inválido", ContractOpInput{Type: OpTransfer, Date: end, AreaID: u1, Position: "QA", Schedule: "L-V", Indefinite: true, ModifyCredentials: true, NewEmail: "no-es-correo"}, "VALIDATION"},
		{"traspaso a la misma área", ContractOpInput{Type: OpTransfer, Date: end, AreaID: uArea, Position: "QA", Schedule: "L-V", Indefinite: true}, "TRANSFER_SAME_AREA"},
		{"traspaso remuneración en cero", ContractOpInput{Type: OpTransfer, Date: end, AreaID: u1, Position: "QA", Schedule: "L-V", Indefinite: true, Pay: &Pay{}}, "PAY_REQUIRED"},
		{"traspaso sede inválida", ContractOpInput{Type: OpTransfer, Date: end, AreaID: u1, Position: "QA", Schedule: "L-V", Indefinite: true, SiteID: "x"}, "VALIDATION"},
		{"traspaso con remuneración", ContractOpInput{Type: OpTransfer, Date: end, AreaID: u1, Position: "QA", Schedule: "L-V", Indefinite: true, Pay: &Pay{Base: 4200.5}, CurrencyID: u1}, ""},
		{"cambio de RQ sin RQ", ContractOpInput{Type: OpRQChange, Date: end}, "VALIDATION"},
		{"cambio de RQ con 11 RQ", ContractOpInput{Type: OpRQChange, Date: end, RequirementIDs: elevenIDs()}, "VALIDATION"},
		{"cambio de RQ ok", ContractOpInput{Type: OpRQChange, Date: end, RequirementID: u1}, ""},
		{"tipo desconocido", ContractOpInput{Type: "CLOSE"}, "VALIDATION"},
	}
	for _, c := range cases {
		in := c.in
		if got := code(in.validate(start, end, uArea)); got != c.want {
			t.Errorf("%s: %q, se esperaba %q", c.name, got, c.want)
		}
	}
	in := ContractOpInput{Type: "renew", EndDate: "2027-06-30", Reason: " Extensión ", Position: "x", AreaID: u1}
	if err := in.validate(start, end, uArea); err != nil || in.Type != OpRenew || in.Reason != "Extensión" || in.Position != "" || in.AreaID != "" {
		t.Fatalf("renovar deja solo lo suyo: %+v %v", in, err)
	}
	rqm := ContractOpInput{Type: OpRQChange, Date: end, RequirementID: u1, RequirementIDs: []string{uArea, u1}}
	if err := rqm.validate(start, end, uArea); err != nil || strings.Join(rqm.RequirementIDs, ",") != u1+","+uArea || rqm.RequirementID != "" {
		t.Fatalf("cambio de RQ múltiple: %+v %v", rqm, err)
	}
	tr := ContractOpInput{Type: OpTransfer, Date: end, AreaID: u1, Position: "QA", Schedule: "L-V", Indefinite: true, EndDate: "2027-01-01", NewUsername: "x"}
	if err := tr.validate(start, end, uArea); err != nil || tr.EndDate != "" || tr.NewUsername != "" {
		t.Fatalf("traspaso indeterminado sin credenciales: %+v %v", tr, err)
	}
}

const uArea = "22222222-2222-4222-8222-222222222222"

func elevenIDs() []string {
	out := []string{}
	for i := 0; i < 11; i++ {
		out = append(out, fmt.Sprintf("33333333-3333-4333-8333-%012d", i))
	}
	return out
}

func terminateOp() *contractOpData {
	return &contractOpData{Type: OpTerminate,
		Input:     ContractOpInput{Type: OpTerminate, Date: "2026-12-31", TerminationReasonID: u1, RecoverEquipment: true, EquipmentReturnDate: "2027-01-05"},
		Before:    contractSnapshot{Client: "BANBIF", Area: "TI", AreaID: u1, Position: "Asistente de Compras", Schedule: "L-V 9-6", StartDate: "2026-06-01", EndDate: "2026-12-31", RQ: "RQ-000208"},
		Labels:    opLabels{TerminationReason: "Término de contrato"},
		Equipment: []equipItem{{Label: "Laptop", Detail: "Lenovo · i7 · 16 GB", DeliveredAt: "2026-06-01"}, {Label: "Celular"}},
		Users:     &UserRequest{Username: "ana.fernandez", Email: "ana.fernandez@empresa.pe"}}
}

func TestContractRecipients(t *testing.T) {
	c := mailCtx()
	to, cc, greet := contractRecipients("FMI", c, "gestor@empresa.pe")
	if strings.Join(to, ",") != "fiorella@empresa.pe,gina@empresa.pe" || greet != "Fiorella" || strings.Join(cc, ",") != "talento@empresa.pe,seleccion@empresa.pe,gestor@empresa.pe" {
		t.Fatalf("FMI de baja: to=%v cc=%v", to, cc)
	}
	for _, f := range []string{"USERS", "RECOVERY"} {
		to, cc, _ := contractRecipients(f, c, "gestor@empresa.pe")
		if strings.Join(to, ",") != "soporte@empresa.pe" || strings.Join(cc, ",") != "fiorella@empresa.pe,gina@empresa.pe,seleccion@empresa.pe,gestor@empresa.pe" {
			t.Fatalf("%s: to=%v cc=%v", f, to, cc)
		}
	}
	to, cc, _ = contractRecipients("NOTICE", c, "")
	if strings.Join(to, ",") != "fiorella@empresa.pe,gina@empresa.pe" || strings.Join(cc, ",") != "seleccion@empresa.pe" {
		t.Fatalf("NOTICE: to=%v cc=%v", to, cc)
	}
	if got := opForms(terminateOp()); strings.Join(got, ",") != "FMI,USERS,RECOVERY" {
		t.Fatalf("baja con equipo: %v", got)
	}
	op := terminateOp()
	op.Input.RecoverEquipment = false
	if got := opForms(op); strings.Join(got, ",") != "FMI,USERS" {
		t.Fatalf("baja sin equipo: %v", got)
	}
	if got := opForms(&contractOpData{Type: OpRQChange}); strings.Join(got, ",") != "FMI,NOTICE" {
		t.Fatalf("cambio de RQ: %v", got)
	}
	if got := opForms(&contractOpData{Type: OpTransfer, Input: ContractOpInput{ModifyCredentials: true}}); strings.Join(got, ",") != "FMI,USERS" {
		t.Fatalf("traspaso con credenciales: %v", got)
	}
}

func TestTerminationMails(t *testing.T) {
	c := mailCtx()
	op := terminateOp()
	subj, html, text := buildContractMail("FMI", "TERMINATED", op, c, "Fiorella")
	if subj != "Baja de colaborador | RQ-000208 | BANBIF" || !strings.Contains(html, "Estimado Fiorella,") || !strings.Contains(html, "fecha de cese 31/12/2026") ||
		!strings.Contains(html, "Recuperar laptop/equipo: Sí: Laptop (Lenovo · i7 · 16 GB), Celular. Devolución: 05/01/2027.") ||
		!strings.Contains(html, "Término de contrato") || !strings.Contains(text, "Recuperar laptop/equipo") {
		t.Fatalf("FMI de baja: %s\n%s", subj, html)
	}
	subj, html, _ = buildContractMail("USERS", "TERMINATED", op, c, "")
	if subj != "Solicitud de baja de credenciales: BANBIF - RQ-000208" || !strings.Contains(html, "desactivación del usuario de red y del correo") ||
		!strings.Contains(html, "ana.fernandez@empresa.pe") || !strings.Contains(html, "bandeja DESATENDIDA") {
		t.Fatalf("baja de credenciales: %s\n%s", subj, html)
	}
	subj, html, _ = buildContractMail("RECOVERY", "RESENT", op, c, "")
	if subj != "Reenvío - Solicitud de recuperación de equipo: BANBIF - RQ-000208" || !strings.Contains(html, "Lenovo · i7 · 16 GB") ||
		!strings.Contains(html, "Fecha de devolución: 05/01/2027") || !strings.Contains(html, ">Celular<") {
		t.Fatalf("recuperación: %s\n%s", subj, html)
	}
	op.Input.RecoverEquipment = false
	if _, html, _ := buildContractMail("FMI", "TERMINATED", op, c, ""); strings.Contains(html, "Recuperar laptop") || !strings.Contains(html, "No se requiere recuperar equipo.") {
		t.Fatalf("sin equipo: %s", html)
	}
	rq := &contractOpData{Type: OpRQChange, Input: ContractOpInput{Date: "2026-12-31"}, Before: op.Before, Labels: opLabels{RQCode: "RQ-000300", RQTitle: "QA", RQClient: "Interbank",
		RQs: []rqLabel{{Code: "RQ-000300", Title: "QA", Client: "Interbank"}, {Code: "RQ-000301", Title: "Dev", Client: "BCP"}}}}
	subj, html, _ = buildContractMail("NOTICE", "RQ_CHANGE", rq, c, "Fiorella")
	if subj != "Gestionar ingreso en BDT: Ana Victoria Fernandez Bueno → RQ-000300, RQ-000301" || !strings.Contains(html, "gestionar en BDT el ingreso") ||
		!strings.Contains(html, ">RQ-000301<") || !strings.Contains(html, ">BCP<") {
		t.Fatalf("aviso de cambio de RQ: %s\n%s", subj, html)
	}
	tr := &contractOpData{Type: OpTransfer, Input: ContractOpInput{Date: "2027-01-01", Position: "Analista", Schedule: "L-V", ModifyCredentials: true, NewEmail: "ana@interbank.pe"},
		Before: op.Before, Labels: opLabels{Area: "Operaciones"}, Users: op.Users}
	if subj, _, _ := buildContractMail("FMI", "TRANSFERRED", tr, c, ""); subj != "Traspaso de área | RQ-000208 | BANBIF" {
		t.Fatalf("traspaso: %s", subj)
	}
	if subj, html, _ := buildContractMail("USERS", "TRANSFERRED", tr, c, ""); subj != "Solicitud de modificación de usuario: BANBIF - RQ-000208" || !strings.Contains(html, "ana@interbank.pe") {
		t.Fatalf("modificación de usuario: %s", subj)
	}
}

func TestTerminationPDF(t *testing.T) {
	f := baseForms()
	f.Users = &UserRequest{Username: "jperez", Email: "jperez@empresa.pe"}
	c := &formContext{req: map[string]any{"talent": map[string]any{"firstName": "José", "lastName": "Núñez"},
		"client": map[string]any{"label": "Cliente Ñandú"}, "requirement": map[string]any{"code": "RQ-1"}},
		forms: &f, labels: map[string]ports.CatalogLabel{u1: {Code: "PEN", Label: "Planilla"}},
		settings: &HireFormSettings{}, manager: "Gestora", issued: fixedNow(), withPay: true,
		print: &printStyle{style: HireFormStyle{}.withDefaults(), company: "Geeky"}}
	op := terminateOp()
	c.applyOp(op)
	fmi := contractPrint("FMI", c, op)
	if fmi.FMI == nil || fmi.FMI.TermReason != "Término de contrato" || fmi.FMI.TermDate != "31/12/2026" || fmi.FMI.ReturnDate != "05/01/2027" ||
		!strings.Contains(fmi.FMI.Recover, "Laptop (Lenovo · i7 · 16 GB)") || fmi.FMI.Client != "BANBIF" || fmi.FMI.Position != "Asistente de Compras" {
		t.Fatalf("FMI de baja: %+v", fmi.FMI)
	}
	us := contractPrint("USERS", c, op)
	if us.Users == nil || us.Users.DeactUser != "ana.fernandez" || us.Users.DeactEmail != "ana.fernandez@empresa.pe" || us.Users.Username != "" ||
		!strings.Contains(us.Users.DeactReason, "31/12/2026") {
		t.Fatalf("baja de credenciales: %+v", us.Users)
	}
	tr := &contractOpData{Type: OpTransfer, Input: ContractOpInput{Date: "2027-01-01", Position: "Analista", Schedule: "L-V 8-5", EndDate: "2027-06-30"},
		Before: op.Before, Labels: opLabels{Area: "Operaciones", Site: "Sede Miraflores", Currency: "PEN"},
		PayEnc: "x", newPay: &Pay{Base: 4200}}
	mv := contractPrint("FMI", c, tr)
	if mv.FMI.MoveSite != "Sede Miraflores" || mv.FMI.MoveBase != "S/.4,200.00" || !mv.FMI.MoveHasBase || mv.FMI.MoveClient != "" || mv.FMI.MovePosition != "Analista" || mv.FMI.MoveArea != "Operaciones" || mv.FMI.MoveDate != "01/01/2027" ||
		!strings.Contains(mv.FMI.Notes, "30/06/2027") {
		t.Fatalf("FMI de movimiento: %+v", mv.FMI)
	}
	for name, pr := range map[string]ports.FormPrint{"FMI_baja": fmi, "USERS_baja": us, "FMI_traspaso": mv} {
		b, err := forms.Renderer{}.Render(pr)
		if dir := os.Getenv("PDF_OUT"); dir != "" && err == nil {
			_ = os.WriteFile(dir+"/"+name+".pdf", b, 0o600)
		}
		if err != nil || !bytes.HasPrefix(b, []byte("%PDF")) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	renew := &contractOpData{Type: OpRenew, Input: ContractOpInput{EndDate: "2027-06-30", Reason: "Extensión"}, Before: op.Before}
	c.applyOp(renew)
	if c.forms.Hire.EndDate != "2027-06-30" {
		t.Fatalf("renovación: fin %s", c.forms.Hire.EndDate)
	}
	if got := renew.changes(); len(got) != 1 || got[0].Field != "endDate" || got[0].From != "2026-12-31" || got[0].To != "2027-06-30" {
		t.Fatalf("historial de renovación: %+v", got)
	}
}

func TestReminderMailContracts(t *testing.T) {
	u := &reminderUser{Name: "Ana Gestora", Email: "ana@empresa.pe", contracts: []ContractAlertItem{
		{RequestID: "r1", Talent: "José Núñez", Client: "BANBIF", Position: "QA", EndDate: "2026-11-24", DaysLeft: -1, Level: LevelExpired},
		{RequestID: "r2", Talent: "Eva Paz", Client: "Interbank", Position: "Dev", EndDate: "2026-12-01", DaysLeft: 6, Level: LevelCritical},
	}}
	subj, html, text := buildReminderMail(ports.ReminderTenant{Slug: "fractal", Company: "Fractal - BDT"}, u, "https://igdt.example/")
	if subj != "Contratos por terminar: 2 (1 críticos, 1 vencidos)" || !strings.Contains(html, "Contratos por terminar") ||
		!strings.Contains(html, "https://igdt.example/fractal/solicitudes/r1?contrato=1") || !strings.Contains(html, "vencido hace 1 días") ||
		!strings.Contains(text, "[Crítico] Eva Paz") || strings.Contains(html, "solicitudes de ingreso por completar") {
		t.Fatalf("recordatorio: %s\n%s", subj, html)
	}
	u.items = []HireAlertItem{{ID: "h1", Talent: "Luis", Client: "BCP", RQ: "RQ-9", Due: "OK", Pending: []string{"FMI"}}}
	if subj, _, _ := buildReminderMail(ports.ReminderTenant{Slug: "fractal", Company: "Fractal"}, u, ""); !strings.HasPrefix(subj, "Solicitudes de ingreso pendientes: 1") ||
		!strings.Contains(subj, " · Contratos por terminar: 2") {
		t.Fatalf("asunto combinado: %s", subj)
	}
}

func TestScheduledTerminationMails(t *testing.T) {
	c := mailCtx()
	op := terminateOp()
	op.Scheduled = true
	_, html, _ := buildContractMail("FMI", "TERMINATED", op, c, "")
	if !strings.Contains(html, "Baja programada: se hace efectiva el 31/12/2026.") {
		t.Fatalf("baja programada: %s", html)
	}
	if _, html, _ := buildContractMail("USERS", "TERMINATED", op, c, ""); !strings.Contains(html, "Baja programada") {
		t.Fatalf("credenciales programadas: %s", html)
	}
	op.change = &opChange{From: "2026-12-31", To: "2027-01-15", Reason: "El cliente extendió una quincena"}
	op.Input.Date = "2027-01-15"
	subj, html, _ := buildContractMail("RECOVERY", "RESCHEDULED", op, c, "")
	if subj != "Cambio de fecha - Solicitud de recuperación de equipo: BANBIF - RQ-000208" ||
		!strings.Contains(html, "Se cambió la fecha de cese del 31/12/2026 al 15/01/2027. Motivo: El cliente extendió una quincena.") {
		t.Fatalf("cambio de fecha: %s\n%s", subj, html)
	}
	op.change = &opChange{From: "2027-01-15", Reason: "Se renovó el servicio"}
	subj, html, _ = buildContractMail("FMI", "OP_CANCELLED", op, c, "Fiorella")
	if subj != "Anulado - Baja de colaborador | RQ-000208 | BANBIF" || !strings.Contains(html, "Se anuló la baja programada") ||
		!strings.Contains(html, "15/01/2027") || !strings.Contains(html, "Se renovó el servicio") {
		t.Fatalf("anulación: %s\n%s", subj, html)
	}
	if got := pendingForms([]string{"FMI"}, false, false); len(got) != 0 {
		t.Fatalf("con credenciales no se pide la ficha de usuarios: %v", got)
	}
	if got := pendingForms(nil, true, false); strings.Join(got, ",") != "FMI,EQUIPMENT" {
		t.Fatalf("reingreso con equipo: %v", got)
	}
}
