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

type mockWorkspaceSource struct {
	sourceFunc
	workspaces func(context.Context, resources.Identity) (resources.WorkspacesResponse, error)
	release    func(context.Context, resources.Identity, resources.ReleaseWorkspaceInput) (resources.ReleaseWorkspaceResponse, error)
}

func (m mockWorkspaceSource) Workspaces(ctx context.Context, i resources.Identity) (resources.WorkspacesResponse, error) {
	return m.workspaces(ctx, i)
}

func (m mockWorkspaceSource) ReleaseWorkspace(ctx context.Context, i resources.Identity, in resources.ReleaseWorkspaceInput) (resources.ReleaseWorkspaceResponse, error) {
	return m.release(ctx, i, in)
}

func TestWorkspacesAndReleaseEndpoints(t *testing.T) {
	station := "00000000-0000-4000-8000-000000000003"
	token := strings.Repeat("a", 32)
	src := mockWorkspaceSource{
		sourceFunc: func(_ context.Context, _ resources.Identity) (resources.Snapshot, error) {
			return resources.Snapshot{SchemaVersion: 1, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), HardwareState: "unavailable", Workloads: []resources.Workload{}}, nil
		},
		workspaces: func(_ context.Context, _ resources.Identity) (resources.WorkspacesResponse, error) {
			return resources.WorkspacesResponse{
				Workspaces: []resources.WorkspaceVolume{
					{
						WorkspaceID:    "verdantflare-station/ws-comfyui-001",
						PVCName:        "ws-comfyui-001",
						PVName:         "pv-ws-comfyui-001",
						Namespace:      "verdantflare-station",
						PoolName:       "comfyui-workspaces",
						AppID:          "comfyui",
						AppDisplayName: "ComfyUI",
						StorageBytes:   53687091200,
						State:          "retained",
						AllowedActions: []string{"release"},
					},
				},
			}, nil
		},
		release: func(_ context.Context, _ resources.Identity, in resources.ReleaseWorkspaceInput) (resources.ReleaseWorkspaceResponse, error) {
			return resources.ReleaseWorkspaceResponse{
				Status: "released",
				Workspace: resources.WorkspaceVolume{
					WorkspaceID: in.Namespace + "/" + in.PVCName,
					PVCName:     in.PVCName,
					PVName:      "pv-" + in.PVCName,
					Namespace:   in.Namespace,
					State:       "available",
				},
			}, nil
		},
	}
	s, err := New(token, station, resources.Service{Source: src}, nil)
	if err != nil {
		t.Fatal(err)
	}

	r1 := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	r1.Header.Set("Authorization", "Bearer "+token)
	r1.Header.Set("X-User-Id", "00000000-0000-4000-8000-000000000001")
	r1.Header.Set("X-Organization-Id", "00000000-0000-4000-8000-000000000002")
	r1.Header.Set("X-Station-Id", station)
	w1 := httptest.NewRecorder()
	s.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK || !strings.Contains(w1.Body.String(), "ws-comfyui-001") {
		t.Fatalf("unexpected GET /api/v1/workspaces response: %d %s", w1.Code, w1.Body.String())
	}

	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/release", strings.NewReader(`{"idempotency_key":"00000000-0000-4000-8000-000000000010","namespace":"verdantflare-station","pvc_name":"ws-comfyui-001"}`))
	r2.Header.Set("Authorization", "Bearer "+token)
	r2.Header.Set("X-User-Id", "00000000-0000-4000-8000-000000000001")
	r2.Header.Set("X-Organization-Id", "00000000-0000-4000-8000-000000000002")
	r2.Header.Set("X-Station-Id", station)
	w2 := httptest.NewRecorder()
	s.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK || !strings.Contains(w2.Body.String(), `"status":"released"`) {
		t.Fatalf("unexpected POST /api/v1/workspaces/release response: %d %s", w2.Code, w2.Body.String())
	}
}

