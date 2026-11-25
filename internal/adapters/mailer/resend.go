package mailer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
)

// Resend: correo saliente por la API HTTP de Resend (https://resend.com/docs/api-reference/emails/send-email).
// Se usa en lugar de SMTP porque Railway puede bloquear los puertos SMTP. La llave viene de RESEND_API_KEY; el
// remitente (MAIL_FROM) debe ser de un dominio verificado en Resend.
type Resend struct {
	Key, From, FromName string
	BaseURL             string // por defecto https://api.resend.com (las pruebas lo cambian)
	HTTP                *http.Client
}

func (r Resend) Enabled() bool { return r.Key != "" && r.From != "" }

func (r Resend) SendInvitation(ctx context.Context, to, tenantName, link, locale string) error {
	subject, body := "Invitación a IGDT", fmt.Sprintf("Te invitaron a %s en IGDT.\n\nActiva tu cuenta aquí: %s\n", tenantName, link)
	if strings.HasPrefix(locale, "en") {
		subject, body = "IGDT invitation", fmt.Sprintf("You were invited to %s on IGDT.\n\nActivate your account: %s\n", tenantName, link)
	}
	return r.Send(ctx, ports.Mail{To: []string{to}, Subject: subject, Text: body})
}

type resendAttachment struct {
	Filename    string `json:"filename"`
	Content     string `json:"content"`
	ContentType string `json:"content_type,omitempty"`
	ContentID   string `json:"content_id,omitempty"`
}

type resendEmail struct {
	From        string             `json:"from"`
	To          []string           `json:"to"`
	Cc          []string           `json:"cc,omitempty"`
	Subject     string             `json:"subject"`
	Text        string             `json:"text"`
	HTML        string             `json:"html,omitempty"`
	Attachments []resendAttachment `json:"attachments,omitempty"`
}

func (r Resend) Send(ctx context.Context, m ports.Mail) error {
	if !r.Enabled() {
		return ports.ErrNoMailer
	}
	to, err := cleanAddrs(m.To)
	if err != nil {
		return err
	}
	cc, err := cleanAddrs(m.Cc)
	if err != nil {
		return err
	}
	to, cc = deliverable(to), deliverable(cc)
	if len(to) == 0 && len(cc) > 0 {
		to, cc = cc[:1], cc[1:]
	}
	if len(to) == 0 {
		return fmt.Errorf("sin destinatarios válidos (los dominios de prueba no reciben correo)")
	}
	from := r.From
	if r.FromName != "" {
		from = fmt.Sprintf("%s <%s>", strings.NewReplacer("<", "", ">", "", "\"", "").Replace(r.FromName), r.From)
	}
	body := resendEmail{From: from, To: to, Cc: cc, Subject: m.Subject, Text: m.Text, HTML: m.HTML}
	for _, a := range m.Attachments {
		body.Attachments = append(body.Attachments, resendAttachment{Filename: a.Name, Content: base64.StdEncoding.EncodeToString(a.Data), ContentType: a.ContentType, ContentID: a.ContentID})
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	base := r.BaseURL
	if base == "" {
		base = "https://api.resend.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/emails", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.Key)
	req.Header.Set("Content-Type", "application/json")
	client := r.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		// el mensaje de Resend explica el motivo (dominio no verificado, destinatario no permitido…); nunca incluye la llave
		var e struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		_ = json.Unmarshal(b, &e)
		return fmt.Errorf("resend %d: %s %s", res.StatusCode, e.Name, e.Message)
	}
	return nil
}

// reservedTLD: dominios de prueba que no reciben correo (rebotarían y dañan la reputación del remitente).
var reservedTLD = map[string]bool{"test": true, "example": true, "invalid": true, "localhost": true, "local": true, "demo": true}

func deliverable(list []string) []string {
	out := []string{}
	for _, a := range list {
		at := strings.LastIndex(a, "@")
		dom := strings.ToLower(a[at+1:])
		if i := strings.LastIndex(dom, "."); i >= 0 {
			dom = dom[i+1:]
		}
		if !reservedTLD[dom] {
			out = append(out, a)
		}
	}
	return out
}
