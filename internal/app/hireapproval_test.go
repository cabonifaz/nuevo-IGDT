package app

import (
	"strings"
	"testing"
	"time"
)

func TestDueStateAndPending(t *testing.T) {
	today := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	for in, want := range map[string]string{"2026-11-24": "OVERDUE", "2026-11-25": "DUE_SOON", "2026-11-28": "DUE_SOON", "2026-11-29": "OK", "": "OK"} {
		if got := dueState(in, today); got != want {
			t.Errorf("%q: %s, se esperaba %s", in, got, want)
		}
	}
	if got := pendingForms([]string{"USERS"}, true, true); strings.Join(got, ",") != "FMI,EQUIPMENT" {
		t.Errorf("pendientes: %v", got)
	}
	if got := pendingForms([]string{"FMI", "USERS"}, false, true); len(got) != 0 {
		t.Errorf("sin equipo: %v", got)
	}
}

func mailCtx() *formContext {
	return &formContext{
		req: map[string]any{
			"requirement": map[string]any{"code": "RQ-000208"}, "client": map[string]any{"label": "BANBIF"},
			"talent":     map[string]any{"firstName": "Ana Victoria", "lastName": "Fernandez", "secondLastName": "Bueno", "email": "ana@x.pe", "phone": "+51 935157063", "docNumber": "44178142"},
			"conditions": map[string]any{"emo": true, "sctr": true, "laptop": false, "duration": "3 mes(es) (inicialmente)"},
			"requesters": []any{map[string]any{"name": "Fiorella Pedraza", "email": "fiorella@empresa.pe"}, map[string]any{"name": "Gina Quintana", "email": "gina@empresa.pe"}},
		},
		forms:    &HireForms{Hire: HireForm{Position: "Asistente de Compras", StartDate: "2026-11-23", Pay: &Pay{Fee: 2400}}},
		settings: &HireFormSettings{SupportEmails: []string{"soporte@empresa.pe"}, TalentEmails: []string{"talento@empresa.pe"}, CCEmails: []string{"seleccion@empresa.pe", "fiorella@empresa.pe"}, CCManager: true},
		print:    &printStyle{company: "Fractal - Banco de talentos"},
	}
}

func TestRecipients(t *testing.T) {
	s := &SelectionService{}
	c := mailCtx()
	to, cc, greet := s.recipientsFor("FMI", c, "gestor@empresa.pe")
	if strings.Join(to, ",") != "fiorella@empresa.pe,gina@empresa.pe" || greet != "Fiorella" ||
		strings.Join(cc, ",") != "talento@empresa.pe,seleccion@empresa.pe,gestor@empresa.pe" {
		t.Fatalf("FMI: to=%v cc=%v greet=%q", to, cc, greet)
	}
	to, cc, _ = s.recipientsFor("USERS", c, "")
	if strings.Join(to, ",") != "soporte@empresa.pe" || strings.Join(cc, ",") != "fiorella@empresa.pe,gina@empresa.pe,seleccion@empresa.pe" {
		t.Fatalf("USERS: to=%v cc=%v", to, cc)
	}
	c.req["requesters"] = []any{}
	c.settings.SupportEmails = nil
	if to, _, _ := s.recipientsFor("EQUIPMENT", c, ""); len(to) != 0 {
		t.Fatalf("sin soporte ni solicitantes: %v", to)
	}
}

func TestFormMail(t *testing.T) {
	c := mailCtx()
	subj, html, text := buildFormMail("USERS", "CONFIRMED", c, "", "")
	if subj != "Solicitud de creación de usuario: BANBIF - RQ-000208" || !strings.Contains(html, "Favor atender la solicitud de creación de usuario.") ||
		!strings.Contains(html, "bandeja DESATENDIDA") || !strings.Contains(html, "desde FRACTAL.") || !strings.Contains(text, "Slds") {
		t.Fatalf("USERS: %s\n%s", subj, html)
	}
	subj, html, _ = buildFormMail("EQUIPMENT", "UPDATED", c, "", "")
	if subj != "Actualización - Solicitud de equipo: BANBIF - RQ-000208" || !strings.Contains(html, "reemplaza") {
		t.Fatalf("EQUIPMENT: %s", subj)
	}
	subj, html, _ = buildFormMail("FMI", "CONFIRMED", c, "Fiorella", "")
	if subj != "Ingreso de nuevo talento | RQ-000208 | BANBIF" || !strings.Contains(html, "Estimado Fiorella,") || !strings.Contains(html, "Requisitos de ingreso") ||
		!strings.Contains(html, "3 mes(es) (inicialmente)") || !strings.Contains(html, "2,400.00") || !strings.Contains(html, "&#9745;") || !strings.Contains(html, "&#9744;") {
		t.Fatalf("FMI: %s\n%s", subj, html)
	}
	if n := attachName("USERS", "FT-GS-01", "Ana Victoria Fernandez Bueno"); n != "FT-GS-01 Solicitud de Creación de Usuarios - Ana Victoria Fernandez Bueno.pdf" {
		t.Fatalf("adjunto: %s", n)
	}
}
