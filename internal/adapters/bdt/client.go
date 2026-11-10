// Package bdt: cliente de la API de integración de BDT (solo lectura de candidatos) con la llave que BDT entrega a IGDT
// al conectar la integración. La URL base viene de BDT_API_URL; nunca de lo que envíe BDT.
package bdt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(base string) *Client {
	return &Client{BaseURL: base, HTTP: &http.Client{Timeout: 90 * time.Second}}
}

func (c *Client) Enabled() bool { return c != nil && c.BaseURL != "" }

const maxDoc = 4 << 20

func (c *Client) get(ctx context.Context, key, path string) (*http.Response, error) {
	if !c.Enabled() {
		return nil, domain.NewError("BDT_NOT_LINKED")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", key)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, domain.NewError("BDT_UNAVAILABLE")
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		res.Body.Close()
		return nil, domain.NewError("TALENT_NOT_FOUND")
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		res.Body.Close()
		return nil, domain.NewError("BDT_NOT_LINKED")
	case res.StatusCode >= 300:
		res.Body.Close()
		return nil, domain.NewError("BDT_UNAVAILABLE")
	}
	return res, nil
}

// Talent: perfil del candidato con sus CV disponibles y postulaciones.
func (c *Client) Talent(ctx context.Context, key, talentID string) (json.RawMessage, error) {
	res, err := c.get(ctx, key, "/integration/v1/talents/"+url.PathEscape(talentID))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxDoc+1))
	if err != nil || len(b) > maxDoc || !json.Valid(b) {
		return nil, fmt.Errorf("respuesta de BDT inválida")
	}
	return b, nil
}

// TalentFile: CV original o con marca (ES/EN) del candidato. El llamador cierra el cuerpo.
func (c *Client) TalentFile(ctx context.Context, key, talentID, fileID string) (string, io.ReadCloser, error) {
	res, err := c.get(ctx, key, "/integration/v1/talents/"+url.PathEscape(talentID)+"/files/"+url.PathEscape(fileID))
	if err != nil {
		return "", nil, err
	}
	return res.Header.Get("Content-Type"), res.Body, nil
}

const maxCVSource = 20 << 20 // incluye cabecera, pie, logo y foto en data URL

// CVSource: insumos para generar el CV con la marca del tenant como lo hace BDT (talento, plantilla y, en inglés,
// los textos traducidos por BDT).
func (c *Client) CVSource(ctx context.Context, key, talentID, lang string) (json.RawMessage, error) {
	res, err := c.get(ctx, key, "/integration/v1/talents/"+url.PathEscape(talentID)+"/cv-source?lang="+url.QueryEscape(lang))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxCVSource+1))
	if err != nil || len(b) > maxCVSource || !json.Valid(b) {
		return nil, fmt.Errorf("respuesta de BDT inválida")
	}
	return b, nil
}
