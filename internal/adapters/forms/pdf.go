// Package forms: impresión en PDF de las fichas de ingreso (FT-GTH-12, FT-GS-03, FT-GS-01) con un formato sobrio:
// cabecera con el logo y el código del formato, secciones con filas etiqueta/valor y tablas.
package forms

import (
	"bytes"

	"github.com/go-pdf/fpdf"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
)

type Renderer struct{}

const (
	margin   = 15.0
	labelW   = 58.0
	rowH     = 6.0
	pageW    = 210.0
	contentW = pageW - 2*margin
)

func (Renderer) Render(d ports.FormDoc) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(true, 18)
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetTitle(tr(d.Title), false)
	pdf.SetCreator("IGDT", false)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Helvetica", "", 7.5)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(contentW/2, 5, tr(d.Footer), "", 0, "L", false, 0, "")
		pdf.CellFormat(contentW/2, 5, tr(d.Code+"  ·  Pág. "+itoa(pdf.PageNo())), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()

	// cabecera: logo | empresa y título | código del formato
	top := pdf.GetY()
	x := margin
	if len(d.Logo) > 0 && (d.LogoType == "PNG" || d.LogoType == "JPG") {
		opt := fpdf.ImageOptions{ImageType: d.LogoType, ReadDpi: true}
		pdf.RegisterImageOptionsReader("logo", opt, bytes.NewReader(d.Logo))
		if pdf.Ok() {
			pdf.ImageOptions("logo", margin, top, 0, 14, false, opt, 0, "")
			x = margin + 42
		} else {
			pdf.ClearError()
		}
	}
	pdf.SetXY(x, top)
	pdf.SetTextColor(30, 30, 30)
	pdf.SetFont("Helvetica", "B", 13)
	pdf.MultiCell(pageW-margin-x-38, 6, tr(d.Title), "", "L", false)
	pdf.SetX(x)
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(90, 90, 90)
	pdf.MultiCell(pageW-margin-x-38, 5, tr(d.Company), "", "L", false)
	if d.Subtitle != "" {
		pdf.SetX(x)
		pdf.MultiCell(pageW-margin-x-38, 5, tr(d.Subtitle), "", "L", false)
	}
	bottom := pdf.GetY()
	pdf.SetXY(pageW-margin-34, top)
	pdf.SetDrawColor(60, 60, 60)
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetTextColor(30, 30, 30)
	pdf.CellFormat(34, 8, tr(d.Code), "1", 2, "C", false, 0, "")
	if bottom < top+16 {
		bottom = top + 16
	}
	pdf.SetY(bottom + 4)

	for _, s := range d.Sections {
		section(pdf, tr, s)
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func section(pdf *fpdf.Fpdf, tr func(string) string, s ports.FormSection) {
	if pdf.GetY() > 255 {
		pdf.AddPage()
	}
	pdf.SetFillColor(235, 238, 242)
	pdf.SetTextColor(30, 30, 30)
	pdf.SetFont("Helvetica", "B", 9.5)
	pdf.CellFormat(contentW, 7, " "+tr(s.Title), "", 1, "L", true, 0, "")
	pdf.Ln(1)
	pdf.SetDrawColor(215, 218, 222)
	for _, f := range s.Fields {
		v := f[1]
		if v == "" {
			v = "—"
		}
		pdf.SetFont("Helvetica", "", 8.5)
		lines := pdf.SplitLines([]byte(tr(v)), contentW-labelW-2)
		h := float64(len(lines)) * 5
		if h < rowH {
			h = rowH
		}
		if pdf.GetY()+h > 279 {
			pdf.AddPage()
		}
		y := pdf.GetY()
		pdf.SetFont("Helvetica", "B", 8.5)
		pdf.SetTextColor(80, 80, 80)
		pdf.CellFormat(labelW, h, tr(f[0]), "B", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 8.5)
		pdf.SetTextColor(20, 20, 20)
		pdf.SetXY(margin+labelW, y)
		if len(lines) <= 1 {
			pdf.CellFormat(contentW-labelW, h, tr(v), "B", 1, "L", false, 0, "")
		} else {
			pdf.MultiCell(contentW-labelW, 5, tr(v), "B", "L", false)
		}
	}
	if len(s.Header) > 0 {
		w := contentW / float64(len(s.Header))
		pdf.SetFont("Helvetica", "B", 8.5)
		pdf.SetFillColor(246, 247, 249)
		for _, h := range s.Header {
			pdf.CellFormat(w, rowH, tr(h), "1", 0, "L", true, 0, "")
		}
		pdf.Ln(-1)
		pdf.SetFont("Helvetica", "", 8.5)
		if len(s.Rows) == 0 {
			pdf.CellFormat(contentW, rowH, "—", "1", 1, "L", false, 0, "")
		}
		for _, r := range s.Rows {
			for i := range s.Header {
				v := ""
				if i < len(r) {
					v = r[i]
				}
				pdf.CellFormat(w, rowH, tr(v), "1", 0, "L", false, 0, "")
			}
			pdf.Ln(-1)
		}
	}
	pdf.Ln(4)
}
