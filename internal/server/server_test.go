package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/audemed44/skeleton/internal/store"
)

const token = "test-token"

func newServer(t *testing.T) http.Handler {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>app")}}
	return New(Options{Store: db, Token: token, Web: web}).Handler()
}

func do(h http.Handler, method, path, body string, auth bool, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuth(t *testing.T) {
	h := newServer(t)
	if rec := do(h, "GET", "/api/items", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without a token: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/items", "", true); rec.Code != http.StatusOK {
		t.Fatalf("with the token: %d", rec.Code)
	}

	rec := do(h, "POST", "/api/session", `{"token":"`+token+`"}`, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign in: %d %s", rec.Code, rec.Body)
	}
	cookie := rec.Result().Cookies()[0]
	if strings.Contains(cookie.Value, token) {
		t.Fatal("the cookie holds the token")
	}
	if rec := do(h, "GET", "/api/items", "", false, "Cookie", cookie.Name+"="+cookie.Value); rec.Code != http.StatusOK {
		t.Fatalf("with the cookie: %d", rec.Code)
	}
}

func TestCrossOriginRefused(t *testing.T) {
	h := newServer(t)
	rec := do(h, "POST", "/api/items", `{"title":"x"}`, true, "Origin", "https://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestItems(t *testing.T) {
	h := newServer(t)
	if rec := do(h, "POST", "/api/items", `{"title":"  "}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank title: %d", rec.Code)
	}
	rec := do(h, "POST", "/api/items", `{"title":"Milk","note":"oat"}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var it store.Item
	json.NewDecoder(rec.Body).Decode(&it)
	if it.ID == 0 || it.Title != "Milk" {
		t.Fatalf("created %+v", it)
	}

	if rec := do(h, "PUT", "/api/items/1", `{"title":"Milk","done":true}`, true); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", "/api/items/99", `{"title":"Nope"}`, true); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: %d", rec.Code)
	}

	rec = do(h, "GET", "/api/foyer/widget", "", true)
	var widget foyerWidget
	json.NewDecoder(rec.Body).Decode(&widget)
	if widget.Version != 1 || widget.Stats[1].Value != "1" || len(widget.Items) != 0 {
		t.Fatalf("widget %+v", widget)
	}

	if rec := do(h, "DELETE", "/api/items/1", "", true); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/items/1", "", true); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}
}

func TestSPAFallback(t *testing.T) {
	h := newServer(t)
	rec := do(h, "GET", "/some/page", "", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/healthz", "", false); rec.Code != http.StatusNoContent {
		t.Fatalf("healthz: %d", rec.Code)
	}
}
