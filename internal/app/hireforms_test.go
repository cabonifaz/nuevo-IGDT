package app

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/cabonifaz/nuevo-IGDT/internal/adapters/forms"
	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
)

const u1 = "11111111-1111-4111-8111-111111111111"

func baseForms() HireForms {
	return HireForms{Hire: HireForm{Position: "Analista", ContractID: u1, ReasonID: u1, AreaID: u1, CurrencyID: u1,
		StartDate: "2026-12-01", Schedule: "L-V 9-18", Pay: &Pay{Base: 3500}}}
}

func TestHireFormsValidate(t *testing.T) {
	f := baseForms()
	if err := f.validate(true); err != nil {
		t.Fatalf("válido: %v", err)
	}
	draft := HireForms{Hire: HireForm{Position: "x"}}
	if err := draft.validate(false); err != nil {
		t.Fatalf("borrador incompleto debe aceptarse: %v", err)
	}
	if err := draft.validate(true); err == nil {
		t.Fatal("borrador incompleto no se puede confirmar")
	}
	bad := baseForms()
	bad.Hire.EndDate = "2026-11-01"
	if bad.validate(true) == nil {
		t.Fatal("fecha de término antes del inicio")
	}
	eq := baseForms()
	eq.Equipment = &EquipmentRequest{RequestDate: "2026-11-20", DeliveryDate: "2026-11-30"}
	if eq.validate(true) == nil {
		t.Fatal("solicitud de equipo sin tipos")
	}
	eq.Equipment.Types = []string{u1}
	eq.Equipment.Annex = "OTRO"
	if eq.validate(true) == nil {
		t.Fatal("anexo inválido")
	}
	us := baseForms()
	us.Users = &UserRequest{Username: "jperez", Email: "no es correo"}
	if us.validate(true) == nil {
		t.Fatal("correo inválido")
	}
	us.Users.Email = "jperez@empresa.pe"
	if err := us.validate(true); err != nil {
		t.Fatalf("usuario válido: %v", err)
	}
}

func TestFormPDF(t *testing.T) {
	f := baseForms()
	f.Equipment = &EquipmentRequest{Types: []string{u1}, Software: []SoftwareItem{{"Office", "365"}}}
	f.Users = &UserRequest{Username: "jperez", Email: "jperez@empresa.pe"}
	c := &formContext{req: map[string]any{"talent": map[string]any{"firstName": "José", "lastName": "Núñez"},
		"client": map[string]any{"label": "Cliente Ñandú"}, "requirement": map[string]any{"code": "RQ-1", "title": "Diseño"}},
		forms: &f, labels: map[string]ports.CatalogLabel{u1: {Code: "PEN", Label: "Planilla"}},
		settings: &HireFormSettings{}, manager: "Gestora", issued: fixedNow(), withPay: true,
		print: &printStyle{style: HireFormStyle{}.withDefaults(), company: "Geeky"}}
	for _, form := range FormCodes {
		b, err := forms.Renderer{}.Render(c.build(form))
		if dir := os.Getenv("PDF_OUT"); dir != "" && err == nil {
			_ = os.WriteFile(dir+"/"+form+".pdf", b, 0o600)
		}
		if err != nil || !bytes.HasPrefix(b, []byte("%PDF")) {
			t.Fatalf("%s: %v", form, err)
		}
	}
	if got := formatAmount(1234567.5); got != "1,234,567.50" {
		t.Fatalf("monto: %s", got)
	}
}

func fixedNow() (t time.Time) { return time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC) }

func TestFormStyle(t *testing.T) {
	st := HireFormStyle{Primary: "#112233", Forms: map[string]FormHeaderStyle{"USERS": {Code: "X-1"}}}
	if err := st.validate(); err != nil {
		t.Fatal(err)
	}
	w := st.withDefaults()
	if w.Primary != "#112233" || w.Border != "#1083C6" || w.Forms["USERS"].Code != "X-1" || w.Forms["USERS"].Version != "01" || w.Forms["FMI"].Code != "FT-GTH-12" {
		t.Fatalf("defaults: %+v", w)
	}
	if (HireFormStyle{Primary: "azul"}).validate() == nil || (HireFormStyle{LogoSource: "X"}).validate() == nil ||
		(HireFormStyle{Forms: map[string]FormHeaderStyle{"OTRO": {}}}).validate() == nil {
		t.Fatal("formato inválido aceptado")
	}
	// vista previa con datos de ejemplo y logo (PDF_OUT=dir para revisarlos)
	raw, _ := os.ReadFile(os.Getenv("PDF_LOGO"))
	logo, _ := normalizeLogo(raw)
	ps := &printStyle{style: HireFormStyle{}.withDefaults(), company: "Fractal", logo: logo, logoType: "PNG"}
	if len(logo) == 0 {
		ps.logoType = ""
	}
	for _, form := range FormCodes {
		b, err := forms.Renderer{}.Render(sampleContext(ps, "Cris Alan Bonifaz Menacho").build(form))
		if dir := os.Getenv("PDF_OUT"); dir != "" && err == nil {
			_ = os.WriteFile(dir+"/sample_"+form+".pdf", b, 0o600)
		}
		if err != nil || !bytes.HasPrefix(b, []byte("%PDF")) {
			t.Fatalf("%s: %v", form, err)
		}
	}
	if got := longDateES(fixedNow()); got != "10 de noviembre de 2026" {
		t.Fatal(got)
	}
}
