// Package forms: impresión en PDF de las fichas de ingreso con el diseño de los formatos originales del FMI:
// FT-GTH-12 Formulario de movimiento, FT-GS-03 Solicitud de instalación de software y hardware y FT-GS-01 Solicitud
// de creación, modificación o desactivación de usuarios. Colores, logo y datos del formato los define el tenant.
package forms

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
)

type Renderer struct{}

const (
	pageW   = 210.0
	pageH   = 297.0
	side    = 30.0 // márgenes laterales de los formatos originales (1.18 in)
	width   = pageW - 2*side
	padX    = 1.4
	lineH   = 4.0
	signTop = 240.0 // el contenido termina antes del bloque de firma (pie de página fijo)
)

// Colores fijos de los formatos originales (no configurables).
const (
	black     = "#000000"
	ink       = "#333333"
	grayText  = "#696969"
	grayLine  = "#8C8C8C"
	softRowBg = "#F2F2F2"
	dateRowBg = "#F9F9F9"
)

type cellOpt struct {
	border  string // color del borde ("" = sin borde)
	lineW   float64
	fill    string
	color   string
	bold    bool
	size    float64
	align   string // L | C | R
	under   bool
	noWrap  bool
	prefix  string // texto en negrita antes del valor ("Área:")
	topText bool   // texto arriba en vez de centrado vertical
}

type doc struct {
	pdf *fpdf.Fpdf
	tr  func(string) string
	d   ports.FormPrint
	st  ports.FormStyle
}

func rgb(hex string) (int, int, int) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return int(v >> 16 & 0xFF), int(v >> 8 & 0xFF), int(v & 0xFF)
}

func (c *doc) font(bold bool, size float64, color string) {
	st := ""
	if bold {
		st = "B"
	}
	c.pdf.SetFont("Helvetica", st, size)
	c.pdf.SetTextColor(rgb(color))
}

// lines parte el texto en líneas que caben en w (mm) con la fuente actual.
func (c *doc) lines(text string, w float64) []string {
	if text == "" {
		return []string{""}
	}
	out := []string{}
	for _, l := range c.pdf.SplitLines([]byte(c.tr(text)), w) {
		out = append(out, string(l))
	}
	return out
}

// height: alto que necesita un texto en una celda de ancho w.
func (c *doc) height(text string, w float64, o cellOpt, min float64) float64 {
	if o.size == 0 {
		o.size = 9
	}
	c.font(o.bold, o.size, ink)
	inner := w - 2*padX
	if o.prefix != "" {
		c.font(true, o.size, ink)
		inner -= c.pdf.GetStringWidth(c.tr(o.prefix + " "))
		c.font(o.bold, o.size, ink)
	}
	h := float64(len(c.lines(text, inner)))*lineH + 2.4
	if h < min {
		return min
	}
	return h
}

// cell dibuja un recuadro con fondo, borde y texto (con salto de línea y centrado vertical).
func (c *doc) cell(x, y, w, h float64, text string, o cellOpt) {
	p := c.pdf
	if o.size == 0 {
		o.size = 9
	}
	if o.color == "" {
		o.color = ink
	}
	if o.align == "" {
		o.align = "L"
	}
	if o.fill != "" {
		p.SetFillColor(rgb(o.fill))
		p.Rect(x, y, w, h, "F")
	}
	if o.border != "" {
		lw := o.lineW
		if lw == 0 {
			lw = 0.2
		}
		p.SetLineWidth(lw)
		p.SetDrawColor(rgb(o.border))
		p.Rect(x, y, w, h, "D")
	}
	if text == "" && o.prefix == "" {
		return
	}
	tx := x + padX
	inner := w - 2*padX
	if o.prefix != "" {
		c.font(true, o.size, o.color)
		pw := p.GetStringWidth(c.tr(o.prefix + " "))
		ty := y + (h-lineH)/2
		if o.topText {
			ty = y + 1.2
		}
		p.SetXY(tx, ty)
		p.CellFormat(pw, lineH, c.tr(o.prefix+" "), "", 0, "L", false, 0, "")
		tx += pw
		inner -= pw
	}
	c.font(o.bold, o.size, o.color)
	ls := c.lines(text, inner)
	if o.noWrap {
		ls = ls[:1]
	}
	ty := y + (h-float64(len(ls))*lineH)/2
	if o.topText {
		ty = y + 1.2
	}
	for i, l := range ls {
		p.SetXY(tx, ty+float64(i)*lineH)
		align := o.align
		if o.prefix != "" {
			align = "L"
		}
		p.CellFormat(inner, lineH, l, "", 0, align, false, 0, "")
		if o.under {
			lw := p.GetStringWidth(l)
			lx := tx + (inner-lw)/2
			p.SetLineWidth(0.25)
			p.SetDrawColor(rgb(o.color))
			p.Line(lx, ty+float64(i)*lineH+lineH-0.4, lx+lw, ty+float64(i)*lineH+lineH-0.4)
		}
	}
}

// check: casilla de 3.5 mm con una X si está marcada, seguida de su texto.
func (c *doc) check(x, y float64, label string, on bool, bold bool, size float64) float64 {
	p := c.pdf
	const s = 3.5
	p.SetLineWidth(0.25)
	p.SetDrawColor(0, 0, 0)
	p.Rect(x, y, s, s, "D")
	if on {
		c.font(true, 8, black)
		p.SetXY(x, y)
		p.CellFormat(s, s, "X", "", 0, "C", false, 0, "")
	}
	c.font(bold, size, ink)
	if bold {
		c.font(bold, size, black)
	}
	p.SetXY(x+s+1, y-0.25)
	lw := p.GetStringWidth(c.tr(label))
	p.CellFormat(lw+1, 4, c.tr(label), "", 0, "L", false, 0, "")
	return x + s + 1 + lw + 1
}

// header: cuadro de la cabecera con logo, título y código/versión/fecha del formato.
func (c *doc) header(y float64, titleColor string) float64 {
	p := c.pdf
	const rowH = 7.0
	h := 3 * rowH
	logoW, titleW, labW := width*0.25, width*0.45, width*0.12
	valW := width - logoW - titleW - labW
	x := side
	bo := cellOpt{border: c.st.Border, lineW: 0.3}
	c.cell(x, y, logoW, h, "", bo)
	c.logo(x, y, logoW, h)
	to := bo
	to.bold, to.size, to.align, to.color = true, 10, "C", titleColor
	c.cell(x+logoW, y, titleW, h, strings.ToUpper(c.d.Header.Title), to)
	rows := [][2]string{{"Código:", c.d.Header.Code}, {"Versión:", c.d.Header.Version}, {"Fecha:", c.d.Header.Date}}
	for i, r := range rows {
		mo := bo
		mo.size, mo.color = 8, black
		c.cell(x+logoW+titleW, y+float64(i)*rowH, labW, rowH, r[0], mo)
		c.cell(x+logoW+titleW+labW, y+float64(i)*rowH, valW, rowH, r[1], mo)
	}
	p.SetLineWidth(0.2)
	return y + h
}

// logo dentro de su celda (máx. 14 mm de alto); sin imagen, el nombre de la empresa.
func (c *doc) logo(x, y, w, h float64) {
	p := c.pdf
	if len(c.d.Logo) > 0 && (c.d.LogoType == "PNG" || c.d.LogoType == "JPG") {
		opt := fpdf.ImageOptions{ImageType: c.d.LogoType}
		info := p.RegisterImageOptionsReader("logo", opt, bytes.NewReader(c.d.Logo))
		if p.Ok() && info != nil && info.Width() > 0 && info.Height() > 0 {
			maxW, maxH := w-3, h-4
			if maxH > 14 {
				maxH = 14
			}
			iw, ih := maxW, maxW*info.Height()/info.Width()
			if ih > maxH {
				ih, iw = maxH, maxH*info.Width()/info.Height()
			}
			p.ImageOptions("logo", x+(w-iw)/2, y+(h-ih)/2, iw, ih, false, opt, 0, "")
			return
		}
		p.ClearError()
	}
	c.cell(x, y, w, h, c.d.Company, cellOpt{bold: true, size: 10, align: "C", color: c.st.Primary})
}

// bar: barra de sección (fondo del color principal, texto centrado en mayúsculas).
func (c *doc) bar(x, y, w float64, text string, size float64, border string) float64 {
	const h = 6.2
	c.cell(x, y, w, h, strings.ToUpper(text), cellOpt{fill: c.st.Primary, border: border, color: c.st.HeaderText, bold: true, size: size, align: "C"})
	return y + h
}

// signature: firma del gestor al pie (nombre, línea, nombre y cargo) y el texto del pie.
func (c *doc) signature(note string, signerBold bool) {
	p := c.pdf
	p.SetAutoPageBreak(false, 0)
	// el bloque termina a 10 mm del borde inferior, como en los formatos originales
	y := pageH - 10 - 5 - 25
	if note != "" {
		c.font(false, 8, grayText)
		y -= float64(len(c.lines(note, width)))*3.6 + 1.5
	}
	c.font(true, 11, ink)
	p.SetXY(side, y+8)
	p.CellFormat(width, 5, c.tr(c.d.Signer), "", 0, "C", false, 0, "")
	lw := 92.6
	p.SetLineWidth(0.3)
	p.SetDrawColor(rgb(c.st.Border))
	p.Line((pageW-lw)/2, y+13.6, (pageW+lw)/2, y+13.6)
	c.font(true, 9, ink)
	p.SetXY(side, y+14.6)
	p.CellFormat(width, 4, c.tr(c.d.Signer), "", 0, "C", false, 0, "")
	c.font(signerBold, 9, ink)
	p.SetXY(side, y+18.6)
	p.CellFormat(width, 4, c.tr(c.d.SignerPos), "", 0, "C", false, 0, "")
	y += 25
	if note != "" {
		c.font(false, 8, grayText)
		p.SetXY(side, y)
		p.MultiCell(width, 3.6, c.tr(note), "", "J", false)
		y = p.GetY() + 1.5
	}
	c.font(false, 7, grayText)
	p.SetXY(side, y+1)
	p.CellFormat(width, 4, c.tr(c.d.Footer), "", 0, "C", false, 0, "")
}

// room: si el bloque de alto h no cabe antes de la firma, pasa a una página nueva.
func (c *doc) room(y, h float64) float64 {
	if y+h > signTop-4 {
		c.pdf.AddPage()
		return 12
	}
	return y
}

func (Renderer) Render(d ports.FormPrint) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(side, 8, side)
	pdf.SetAutoPageBreak(false, 0)
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetTitle(tr(d.Header.Code+" "+d.Header.Title), false)
	pdf.SetCreator("IGDT", false)
	pdf.AddPage()
	c := &doc{pdf: pdf, tr: tr, d: d, st: d.Style}
	switch {
	case d.FMI != nil:
		c.fmi(*d.FMI)
	case d.Equipment != nil:
		c.equipment(*d.Equipment)
	case d.Users != nil:
		c.users(*d.Users)
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ------------------------------------------------------------ FT-GTH-12

const sunatNote = `(*) DECLARACION EN SUNAT - En esta sección deberá informar al colaborador que se encuentra en Planilla y será ` +
	`declarado en SUNAT. Responda la pregunta [1], en caso su respuesta sea afirmativa, por favor responda a la pregunta ` +
	`[2] tener en cuenta que si la respuesta es "Oficina del Cliente" se debe contar con un contrato entre empresas.`

func (c *doc) fmi(f ports.FMIPrint) {
	y := 8.0
	if f.Locator {
		c.cell(side, y, width, 8, strings.ToUpper(c.d.Header.Title), cellOpt{bold: true, size: 10, align: "C", color: c.st.Title, under: true})
		y += 10
	} else {
		y = c.header(y, c.st.Title) + 3
	}
	labW := width * 0.30
	colW := (width - labW) / 4
	bo := cellOpt{border: c.st.Border, lineW: 0.15}
	lab := bo
	lab.fill = c.st.LabelBg
	val := bo
	val.color = black
	section := func(title string) {
		y = c.room(y, 16)
		c.cell(side, y, width, 6.5, strings.ToUpper(title), cellOpt{bold: true, size: 9, color: ink})
		y += 6.5
	}
	row := func(label, value string) {
		h := c.height(value, width-labW, val, 6.5)
		y = c.room(y, h)
		c.cell(side, y, labW, h, label, lab)
		c.cell(side+labW, y, width-labW, h, value, val)
		y += h
	}
	pay := func(label string, base, mob, bonus string, hasBase, hasMob, monthly, quarterly bool) {
		const r = 5.6
		y = c.room(y, 3*r)
		c.cell(side, y, labW, 3*r, label, lab)
		x := side + labW
		for i, t := range []struct {
			l  string
			on bool
		}{{"Base", hasBase}, {"Movilidad", hasMob}} {
			cx := x + float64(i)*colW
			c.cell(cx, y, colW, 2*r, "", bo)
			c.font(false, 9, ink)
			w := 3.5 + 1 + c.pdf.GetStringWidth(c.tr(t.l)) + 1
			c.check(cx+(colW-w)/2, y+r-1.75, t.l, t.on, false, 9)
		}
		c.cell(x+2*colW, y, colW, 2*r, "Bono", cellOpt{border: c.st.Border, lineW: 0.15, align: "C"})
		c.cell(x+3*colW, y, colW, r, "", bo)
		c.check(x+3*colW+padX, y+(r-3.5)/2, "Mensual", monthly, false, 9)
		c.cell(x+3*colW, y+r, colW, r, "", bo)
		c.check(x+3*colW+padX, y+r+(r-3.5)/2, "Trimestral", quarterly, false, 9)
		am := bo
		am.align, am.color, am.noWrap = "C", black, true
		c.cell(x, y+2*r, colW, r, base, am)
		c.cell(x+colW, y+2*r, colW, r, mob, am)
		c.cell(x+2*colW, y+2*r, 2*colW, r, bonus, am)
		y += 3 * r
	}

	section("Datos del colaborador")
	row("Nombres y Apellidos", f.Name)
	row("Cliente", f.Client)
	y += 2.5
	section("Ingreso")
	row("Modalidad", f.Modality)
	row("Motivo de Ingreso", f.Reason)
	row("Cargo", f.Position)
	row("Horario de Trabajo", f.Schedule)
	pay("Estructura Salarial", f.Base, f.Mobility, f.Bonus, f.HasBase, f.HasMobility, f.Monthly, f.Quarterly)
	row("F. Inicio contrato", f.Start)
	row("F. Término contrato", f.End)
	row("Proyecto / servicio", f.Project)
	row("Objeto del contrato", f.Object)
	if f.Notes != "" {
		row("Observaciones", f.Notes)
	}
	y += 2.5
	section("Declaración en SUNAT (*)")
	row("[1] ¿Declarado en SUNAT?", f.Sunat)
	row("[2] Sede a declarar", f.Site)
	y += 2.5
	section("Movimiento")
	pay("Cambio de estructura salarial", "", "", "", false, false, false, false)
	row("Cambio de puesto", "")
	row("Cambio de área", "")
	row("Cambio de jornada", "")
	row("F. Inicio de movimiento", "")
	y += 2.5
	section("Cese")
	row("Motivo de Cese", "")
	row("Fecha de Cese", "")
	row("Fecha de devolución Equipo", "")
	c.signature(sunatNote, false)
}

// ------------------------------------------------------------ FT-GS-03

func (c *doc) equipment(e ports.EquipmentPrint) {
	y := c.header(12.7, black) + 3.5
	bk := cellOpt{border: black, lineW: 0.18}
	lab := bk
	lab.bold, lab.color = true, black
	val := lab
	// datos del colaborador
	y = c.bar(side, y, width, "Datos del colaborador", 9, black)
	c1, c2, c3 := width*0.25, width*0.29, width*0.14
	c4 := width - c1 - c2 - c3
	full := func(label, value string) {
		h := c.height(value, width-c1, val, 6.5)
		c.cell(side, y, c1, h, label, lab)
		c.cell(side+c1, y, width-c1, h, value, val)
		y += h
	}
	pair := func(l1, v1, l2, v2 string) {
		h := c.height(v1, c2, val, 6.5)
		if h2 := c.height(v2, c4, val, 6.5); h2 > h {
			h = h2
		}
		c.cell(side, y, c1, h, l1, lab)
		c.cell(side+c1, y, c2, h, v1, val)
		c.cell(side+c1+c2, y, c3, h, l2, lab)
		c.cell(side+c1+c2+c3, y, c4, h, v2, val)
		y += h
	}
	full("Nombres y Apellidos:", e.Name)
	full("Cliente:", e.Client)
	pair("Área:", e.Area, "Cargo:", e.Position)
	pair("DNI:", e.DocNumber, "Teléfono:", e.Phone)
	y += 3
	// fechas
	c.font(true, 9, black)
	c.pdf.SetXY(side, y)
	c.pdf.CellFormat(width, 6, c.tr("Fecha de Solicitud: "+e.RequestDate+"      Fecha de Entrega: "+e.DeliveryDate), "", 0, "L", false, 0, "")
	y += 7
	c.pdf.SetLineWidth(0.3)
	c.pdf.SetDrawColor(rgb(c.st.Border))
	c.pdf.Line(side, y, side+width, y)
	y += 6
	// hardware
	y = c.bar(side, y, width, "Datos de requerimiento de hardware", 9, black)
	ws := []float64{width * 0.30, width * 0.22, width * 0.22, width * 0.13, width * 0.13}
	heads := []string{"TIPO", "PROCESADOR", "RAM", "HD", "MARCA\n(*)"}
	colHead := func(x, w float64, t string) {
		c.cell(x, y, w, 7, "", cellOpt{fill: c.st.Primary, border: black, lineW: 0.18})
		parts := strings.Split(t, "\n")
		c.font(true, 8, c.st.HeaderText)
		for i, part := range parts {
			c.pdf.SetXY(x, y+(7-float64(len(parts))*3.2)/2+float64(i)*3.2)
			c.pdf.CellFormat(w, 3.2, c.tr(part), "", 0, "C", false, 0, "")
		}
	}
	x := side
	for i, w := range ws {
		colHead(x, w, heads[i])
		x += w
	}
	y += 7
	// casillas de tipo seguidas (PC, Laptop y otros tipos pedidos); saltan de línea si no caben
	type pos struct{ x, y float64 }
	place := make([]pos, len(e.Types))
	cx, line := side+padX, 0
	for i, t := range e.Types {
		c.font(true, 9, black)
		w := 3.5 + 1 + c.pdf.GetStringWidth(c.tr(t.Label)) + 1 + 2.5
		if cx+w > side+ws[0] && cx > side+padX {
			cx, line = side+padX, line+1
		}
		place[i] = pos{cx, float64(line)}
		cx += w
	}
	rh := 4.0 + float64(line+1)*4.8
	c.cell(side, y, ws[0], rh, "", bk)
	for i, t := range e.Types {
		c.check(place[i].x, y+2+place[i].y*4.8, t.Label, t.On, true, 9)
	}
	x = side + ws[0]
	for i, v := range []string{e.Processor, e.RAM, e.Disk, e.Brand} {
		c.cell(x, y, ws[i+1], rh, v, val)
		x += ws[i+1]
	}
	y += rh + 3.5
	// anexo / celular / internet móvil
	w3 := []float64{width * 0.33, width * 0.34, width * 0.33}
	x = side
	for i, t := range []string{"ANEXO", "CELULAR", "INTERNET MÓVIL"} {
		colHead(x, w3[i], t)
		x += w3[i]
	}
	y += 7
	checks := [][2]ports.FormCheck{
		{{Label: "Fijo", On: e.AnnexFixed}, {Label: "Softphone", On: e.AnnexSoftphone}},
		{{Label: "Sí", On: e.Mobile}, {Label: "No", On: !e.Mobile}},
		{{Label: "Sí", On: e.MobileInternet}, {Label: "No", On: !e.MobileInternet}},
	}
	x = side
	for i, pair := range checks {
		c.cell(x, y, w3[i], 7, "", bk)
		nx := c.check(x+padX, y+1.75, pair[0].Label, pair[0].On, true, 9)
		c.check(nx+2.5, y+1.75, pair[1].Label, pair[1].On, true, 9)
		x += w3[i]
	}
	y += 10
	// accesorios
	acc := cellOpt{bold: true, size: 9, color: black, prefix: "Accesorios:"}
	ah := c.height(e.Accessories, width, acc, 6)
	c.cell(side-padX, y, width+padX, ah, e.Accessories, acc)
	y += ah
	if e.Notes != "" {
		no := cellOpt{bold: true, size: 9, color: black, prefix: "Observaciones:"}
		nh := c.height(e.Notes, width, no, 6)
		c.cell(side-padX, y, width+padX, nh, e.Notes, no)
		y += nh
	}
	y += 3
	// software
	y = c.room(y, 20)
	y = c.bar(side, y, width, "Datos de la instalacion de software", 9, black)
	wsw := []float64{width * 0.10, width * 0.70, width * 0.20}
	x = side
	for i, t := range []string{"ITEM", "PRODUCTO", "VERSION"} {
		colHead(x, wsw[i], t)
		x += wsw[i]
	}
	y += 7
	rows := e.Software
	if len(rows) == 0 {
		rows = [][2]string{{"", ""}}
	}
	for i, r := range rows {
		h := c.height(r[0], wsw[1], val, 6.5)
		if y+h > signTop-10 {
			c.pdf.AddPage()
			y = 12
		}
		item := strconv.Itoa(i + 1)
		if r[0] == "" && r[1] == "" && len(e.Software) == 0 {
			item = ""
		}
		ic := val
		ic.align = "C"
		c.cell(side, y, wsw[0], h, item, ic)
		c.cell(side+wsw[0], y, wsw[1], h, r[0], val)
		c.cell(side+wsw[0]+wsw[1], y, wsw[2], h, r[1], val)
		y += h
	}
	c.font(true, 8, black)
	c.pdf.SetXY(side, y+1.5)
	c.pdf.CellFormat(width, 4, "(*) Opcional", "", 0, "L", false, 0, "")
	c.signature("", true)
}

// ------------------------------------------------------------ FT-GS-01

func (c *doc) users(u ports.UsersPrint) {
	y := c.header(7, black) + 3
	g := cellOpt{border: grayLine, lineW: 0.1}
	lab := g
	lab.bold = true
	gap := 5.0
	// datos del solicitante
	y = c.bar(side, y, width, "Datos del solicitante", 9, grayLine)
	aW := width * 0.15
	areaW := width * 0.35
	const r = 6.5
	pre := func(prefix string) cellOpt { o := g; o.prefix = prefix; return o }
	c.cell(side, y, width-aW, r, u.RequesterName, pre("Nombres y Apellidos:"))
	an := g
	an.topText = true
	c.cell(side+width-aW, y, aW, 2*r, "", g)
	c.font(true, 9, ink)
	c.pdf.SetXY(side+width-aW+padX, y+2.2)
	c.pdf.CellFormat(aW-2*padX, lineH, c.tr("Anexo:"), "", 0, "L", false, 0, "")
	c.font(false, 9, ink)
	c.pdf.SetXY(side+width-aW+padX, y+2.2+lineH)
	c.pdf.CellFormat(aW-2*padX, lineH, c.tr(u.RequesterAnnex), "", 0, "L", false, 0, "")
	y += r
	c.cell(side, y, areaW, r, u.RequesterArea, pre("Área:"))
	c.cell(side+areaW, y, width-aW-areaW, r, u.RequesterPosition, pre("Cargo:"))
	y += r
	fd := pre("Fecha:")
	fd.fill = dateRowBg
	c.cell(side, y, width, r, u.Date, fd)
	y += r + gap
	// creación de usuarios
	y = c.bar(side, y, width, "Creación de usuarios", 9, grayLine)
	w1, w2, w3 := width*0.20, width*0.30, width*0.24
	w4 := width - w1 - w2 - w3
	nameH := c.height(u.Name, w2+w3, g, 8.5)
	userH := c.height(u.Username, w2+w3, g, 8.5)
	mailH := c.height(u.Email, w2, g, 8.5)
	c.cell(side, y, w1, nameH, "Nombres y Apellidos:", lab)
	c.cell(side+w1, y, w2+w3, nameH, u.Name, g)
	c.cell(side+w1+w2+w3, y, w4, nameH+userH+mailH, "", g)
	y += nameH
	c.cell(side, y, w1, userH, "Nombre de Usuario:", lab)
	c.cell(side+w1, y, w2+w3, userH, u.Username, g)
	y += userH
	c.cell(side, y, w1, mailH, "Correo:", lab)
	c.cell(side+w1, y, w2, mailH, u.Email, g)
	ar := g
	ar.prefix = "Área:"
	c.cell(side+w1+w2, y, w3, mailH, u.Area, ar)
	y += mailH
	soft := g
	soft.fill = softRowBg
	const ch = 10.0
	c.cell(side, y, w1, ch, "", soft)
	c.cell(side+w1, y, w2, ch, "", soft)
	c.cell(side+w1+w2, y, w3+w4, ch, "", soft)
	c.check(side+padX, y+1, "Entrada", false, false, 9)
	c.check(side+padX, y+5.4, "Salida", false, false, 9)
	c.check(side+w1+padX, y+1, "Interno", false, false, 9)
	c.check(side+w1+padX, y+5.4, "Externo", false, false, 9)
	y += ch
	if u.Notes != "" {
		nh := c.height(u.Notes, width-w1, g, 6.5)
		c.cell(side, y, w1, nh, "Observaciones:", lab)
		c.cell(side+w1, y, width-w1, nh, u.Notes, g)
		y += nh
	}
	y += gap
	// modificación (en blanco)
	y = c.bar(side, y, width, "Modificación de usuarios", 9, grayLine)
	m1, m2 := width*0.33, width*0.50
	m3 := width - m1 - m2
	mods := []struct {
		l   string
		box bool
	}{{"Modificar Usuario de Red", true}, {"Usuario:", false}, {"Modificar por:", false},
		{"Modificar correo electrónico:", true}, {"Correo:", false}, {"Modificar por:", false}}
	c.cell(side+m1+m2, y, m3, float64(len(mods))*r, "", g)
	for _, m := range mods {
		c.cell(side, y, m1, r, m.l, lab)
		c.cell(side+m1, y, m2, r, "", g)
		if m.box {
			c.check(side+m1+padX, y+(r-3.5)/2, "", false, false, 9)
		}
		y += r
	}
	y += gap
	// desactivación (en blanco)
	y = c.bar(side, y, width, "Desactivación de usuarios", 9, grayLine)
	for _, l := range []string{"Nombre y Apellidos:", "Usuario:", "Correo:", "Motivo:"} {
		h := r
		if l == "Nombre y Apellidos:" {
			h = 8.5
		}
		c.cell(side, y, w1, h, l, lab)
		c.cell(side+w1, y, width-w1, h, "", g)
		y += h
	}
	c.signature("", false)
}
