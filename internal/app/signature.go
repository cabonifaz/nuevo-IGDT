package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"path"

	"github.com/google/uuid"

	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// Firma de cada usuario para las fichas de ingreso: el propio usuario la sube (PNG o JPG) y se imprime sobre la línea
// de firma de las fichas que confirma o corrige. Se guarda recortada y como PNG; el fondo blanco se vuelve transparente.

const maxSignatureBytes = 2 << 20

// MySignature devuelve la firma del usuario (PNG); nil si no tiene.
func (s *SelectionService) MySignature(ctx context.Context, p *domain.Principal) ([]byte, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	return s.signature(ctx, tid, p.UserID, "")
}

func (s *SelectionService) signature(ctx context.Context, tid, actorID uint64, userUUID string) ([]byte, error) {
	if s.Storage == nil {
		return nil, nil
	}
	key, _, err := s.Repo.UserSignature(ctx, tid, actorID, userUUID)
	if err != nil || key == "" {
		return nil, err
	}
	rc, err := s.Storage.Open(ctx, key)
	if err != nil {
		return nil, nil
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxSignatureBytes+1))
	if err != nil || len(b) > maxSignatureBytes {
		return nil, nil
	}
	return b, nil
}

func (s *SelectionService) UploadSignature(ctx context.Context, p *domain.Principal, ip, originalName string, r io.Reader) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if s.Storage == nil || s.Files == nil {
		return domain.ErrValidation
	}
	data, err := io.ReadAll(io.LimitReader(r, maxSignatureBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxSignatureBytes {
		return domain.NewError("FILE_TOO_LARGE")
	}
	clean, ok := cleanSignature(data)
	if !ok {
		return domain.NewError("FILE_TYPE_NOT_ALLOWED")
	}
	id := uuid.NewString()
	key := path.Join("tenants", p.TenantUUID, "signatures", id+".png")
	size, sum, err := s.Storage.Put(ctx, key, bytes.NewReader(clean), maxSignatureBytes)
	if err != nil {
		return err
	}
	owner := p.UserID
	f := domain.FileObject{UUID: id, TenantID: &tid, OwnerType: "USER_SIGNATURE", Category: "SIGNATURE",
		StorageKey: key, OriginalName: "firma.png", MimeType: "image/png", Size: size, SHA256: sum}
	if err := s.Files.Create(ctx, meta(p, ip), f, &owner); err != nil {
		_ = s.Storage.Delete(ctx, key)
		return err
	}
	return s.Repo.UserSignatureSet(ctx, meta(p, ip), id)
}

func (s *SelectionService) ClearSignature(ctx context.Context, p *domain.Principal, ip string) error {
	if _, err := tenantOf(p); err != nil {
		return err
	}
	return s.Repo.UserSignatureClear(ctx, meta(p, ip))
}

// cleanSignature: decodifica PNG/JPG, vuelve transparente el fondo claro, recorta los márgenes vacíos y la guarda como
// PNG (máx. 1200 px de ancho para que el PDF no pese).
func cleanSignature(b []byte) ([]byte, bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > 16_000_000 {
		return nil, false
	}
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, false
	}
	bd := src.Bounds()
	rgba := image.NewNRGBA(image.Rect(0, 0, bd.Dx(), bd.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, bd.Min, draw.Src)
	minX, minY, maxX, maxY := rgba.Bounds().Dx(), rgba.Bounds().Dy(), -1, -1
	for y := 0; y < rgba.Bounds().Dy(); y++ {
		for x := 0; x < rgba.Bounds().Dx(); x++ {
			c := rgba.NRGBAAt(x, y)
			if c.A < 40 || (c.R > 225 && c.G > 225 && c.B > 225) {
				rgba.SetNRGBA(x, y, color.NRGBA{})
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX < 0 { // imagen vacía
		return nil, false
	}
	pad := 4
	crop := image.Rect(max(minX-pad, 0), max(minY-pad, 0), min(maxX+pad+1, rgba.Bounds().Dx()), min(maxY+pad+1, rgba.Bounds().Dy()))
	var img image.Image = rgba.SubImage(crop)
	if crop.Dx() > 1200 { // reducción simple (vecino más cercano)
		w, h := 1200, crop.Dy()*1200/crop.Dx()
		if h < 1 {
			h = 1
		}
		dst := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(x, y, img.At(crop.Min.X+x*crop.Dx()/w, crop.Min.Y+y*crop.Dy()/h))
			}
		}
		img = dst
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}
