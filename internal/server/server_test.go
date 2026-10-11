package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-app-resources/internal/resources"
)

type sourceFunc func(context.Context, resources.Identity) (resources.Snapshot, error)

func (f sourceFunc) Read(ctx context.Context, i resources.Identity) (resources.Snapshot, error) {
	return f(ctx, i)
}

func TestAuthorizationAndStationIsolation(t *testing.T) {
	station := "00000000-0000-4000-8000-000000000003"
	token := strings.Repeat("a", 32)
	for _, kind := range []string{"no_token", "fake_headers", "wrong_station", "revoked", "allowed"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			s, err := New(token, station, resources.Service{Source: sourceFunc(func(_ context.Context, i resources.Identity) (resources.Snapshot, error) {
				calls++
				if kind == "revoked" {
					return resources.Snapshot{}, &resources.SourceError{Status: 403, Code: "FORBIDDEN"}
				}
				return resources.Snapshot{SchemaVersion: 1, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), HardwareState: "unavailable", Workloads: []resources.Workload{}}, nil
			})}, nil)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
			r.Header.Set("X-User-Id", "00000000-0000-4000-8000-000000000001")
			r.Header.Set("X-Organization-Id", "00000000-0000-4000-8000-000000000002")
			r.Header.Set("X-Station-Id", station)
			if kind != "no_token" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			if kind == "fake_headers" {
				r.Header.Set("X-User-Id", "admin")
			}
			if kind == "wrong_station" {
				r.Header.Set("X-Station-Id", "00000000-0000-4000-8000-000000000099")
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			want := 403
			if kind == "no_token" {
				want = 401
			}
			if kind == "allowed" {
				want = 200
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if (kind == "no_token" || kind == "fake_headers" || kind == "wrong_station") && calls != 0 {
				t.Fatal("unauthorized request reached source")
			}
		})
	}
}
