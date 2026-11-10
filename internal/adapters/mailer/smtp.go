package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
)

// SMTP: correo saliente con STARTTLS (puerto 587) o TLS directo (465). Configurado por variables de entorno.
type SMTP struct {
	Host, Port, User, Password, From, FromName string
}

func (s SMTP) Enabled() bool { return s.Host != "" && s.From != "" }

func (s SMTP) SendInvitation(ctx context.Context, to, tenantName, link, locale string) error {
	subject, body := "Invitación a IGDT", fmt.Sprintf("Te invitaron a %s en IGDT.\n\nActiva tu cuenta aquí: %s\n", tenantName, link)
	if strings.HasPrefix(locale, "en") {
		subject, body = "IGDT invitation", fmt.Sprintf("You were invited to %s on IGDT.\n\nActivate your account: %s\n", tenantName, link)
	}
	return s.Send(ctx, ports.Mail{To: []string{to}, Subject: subject, Text: body})
}

func cleanAddrs(list []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, a := range list {
		addr, err := mail.ParseAddress(strings.TrimSpace(a))
		if err != nil {
			return nil, fmt.Errorf("dirección inválida")
		}
		k := strings.ToLower(addr.Address)
		if !seen[k] {
			seen[k] = true
			out = append(out, addr.Address)
		}
	}
	return out, nil
}

func (s SMTP) Send(ctx context.Context, m ports.Mail) error {
	if !s.Enabled() {
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
	if len(to) == 0 {
		return fmt.Errorf("sin destinatarios")
	}
	msg := s.build(to, cc, m)
	addr := net.JoinHostPort(s.Host, s.Port)
	d := net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	if s.Port == "465" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if s.Port != "465" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("el servidor SMTP no ofrece STARTTLS")
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	for _, r := range append(append([]string{}, to...), cc...) {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func boundary() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "igdt-" + hex.EncodeToString(b)
}

func wrap64(b []byte) string {
	s := base64.StdEncoding.EncodeToString(b)
	var out strings.Builder
	for len(s) > 76 {
		out.WriteString(s[:76] + "\r\n")
		s = s[76:]
	}
	out.WriteString(s + "\r\n")
	return out.String()
}

func (s SMTP) build(to, cc []string, m ports.Mail) []byte {
	var b bytes.Buffer
	from := (&mail.Address{Name: s.FromName, Address: s.From}).String()
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\n", from, strings.Join(to, ", "))
	if len(cc) > 0 {
		fmt.Fprintf(&b, "Cc: %s\r\n", strings.Join(cc, ", "))
	}
	fmt.Fprintf(&b, "Subject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n", mime.QEncoding.Encode("utf-8", m.Subject), time.Now().Format(time.RFC1123Z))
	bd := boundary()
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", bd)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n%s", bd, wrap64([]byte(m.Text)))
	for _, a := range m.Attachments {
		name := mime.QEncoding.Encode("utf-8", a.Name)
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; name=\"%s\"\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=\"%s\"\r\n\r\n%s",
			bd, a.ContentType, name, name, wrap64(a.Data))
	}
	fmt.Fprintf(&b, "--%s--\r\n", bd)
	return b.Bytes()
}

// Send del mailer de desarrollo: sin proveedor, el envío se registra como pendiente (sin direcciones completas).
func (m LogMailer) Send(_ context.Context, msg ports.Mail) error {
	domains := []string{}
	for _, t := range msg.To {
		domains = append(domains, domainOf(t))
	}
	m.Log.Info("correo no enviado: sin proveedor SMTP", "subject", msg.Subject, "to_domains", domains, "attachments", len(msg.Attachments))
	return ports.ErrNoMailer
}
