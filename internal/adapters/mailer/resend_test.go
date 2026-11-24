package mailer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
)

func TestResendSend(t *testing.T) {
	var got resendEmail
	var auth string
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"name":"validation_error","message":"domain not verified"}`))
	}))
	defer srv.Close()
	r := Resend{Key: "re_x", From: "fichas@empresa.pe", FromName: "IGDT Fractal", BaseURL: srv.URL}
	err := r.Send(context.Background(), ports.Mail{To: []string{"A <a@x.pe>", "a@x.pe"}, Cc: []string{"b@x.pe"}, Subject: "S", Text: "T",
		Attachments: []ports.MailAttachment{{Name: "f.pdf", ContentType: "application/pdf", Data: []byte("%PDF")}}})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer re_x" || got.From != "IGDT Fractal <fichas@empresa.pe>" || len(got.To) != 1 || got.Cc[0] != "b@x.pe" ||
		got.Attachments[0].Content != "JVBERg==" || got.Attachments[0].Filename != "f.pdf" {
		t.Fatalf("petición: %s %+v", auth, got)
	}
	status = 422
	if err := r.Send(context.Background(), ports.Mail{To: []string{"a@x.pe"}, Subject: "S", Text: "T"}); err == nil || !strings.Contains(err.Error(), "domain not verified") || strings.Contains(err.Error(), "re_x") {
		t.Fatalf("error: %v", err)
	}
	if err := (Resend{}).Send(context.Background(), ports.Mail{To: []string{"a@x.pe"}}); err != ports.ErrNoMailer {
		t.Fatalf("sin llave: %v", err)
	}
}
