package bdt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

func TestSearchTalents(t *testing.T) {
	var got url.Values
	status, body := 200, `{"total":1,"page":1,"pageSize":20,"items":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/integration/v1/talents" || r.Header.Get("X-Api-Key") != "k" {
			t.Errorf("petición inesperada %s", r.URL)
		}
		got = r.URL.Query()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := New(srv.URL)
	ctx := context.Background()
	if _, err := c.SearchTalents(ctx, "k", url.Values{"q": {"ana"}, "page": {"2"}}); err != nil || got.Get("q") != "ana" || got.Get("page") != "2" {
		t.Fatalf("búsqueda: %v %v", err, got)
	}
	for _, tc := range []struct {
		status int
		body   string
		code   string
	}{
		{403, `{"error":{"code":"APIKEY_SCOPE"}}`, "BDT_SEARCH_DISABLED"},
		{404, `{"error":{"code":"NOT_FOUND"}}`, "BDT_SEARCH_DISABLED"},
		{403, `{"error":{"code":"APIKEY_NOT_ENABLED"}}`, "BDT_NOT_LINKED"},
		{401, `{"error":{"code":"APIKEY_INVALID"}}`, "BDT_NOT_LINKED"},
		{422, `{"error":{"code":"VALIDATION"}}`, "VALIDATION"},
		{500, `{}`, "BDT_UNAVAILABLE"},
	} {
		status, body = tc.status, tc.body
		_, err := c.SearchTalents(ctx, "k", url.Values{})
		if domain.AsError(err).Code != tc.code {
			t.Errorf("%d %s: %v, se esperaba %s", tc.status, tc.body, err, tc.code)
		}
	}
}
