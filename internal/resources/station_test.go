package resources

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStationDelegationNeverUsesBrowserAuthority(t *testing.T) {
	i := Identity{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003"}
	for _, status := range []int{200, 302, 401, 403, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			s, err := NewStationSource("https://station.example", strings.Repeat("s", 32), roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/internal/v1/resources/snapshot" || r.Header.Get("X-User-Id") != i.UserID || r.Header.Get("X-Station-Id") != i.StationID || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) {
					t.Fatal("wrong authority envelope")
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://other.example"}}, Body: io.NopCloser(strings.NewReader(`{"schema_version":1}`))}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Read(context.Background(), i)
			if status == 200 && err != nil || status != 200 && err == nil {
				t.Fatal("unexpected result", err)
			}
			if calls != 1 {
				t.Fatal("redirect followed")
			}
		})
	}
}

func TestStationWorkspacesAndReleaseDelegation(t *testing.T) {
	i := Identity{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003"}
	token := strings.Repeat("s", 32)
	s, err := NewStationSource("https://station.example", token, roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-User-Id") != i.UserID || r.Header.Get("X-Station-Id") != i.StationID || r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatal("wrong authority envelope")
		}
		switch r.URL.Path {
		case "/internal/v1/resources/workspaces":
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"workspaces":[{"workspace_id":"verdantflare-station/ws-comfyui-001","pvc_name":"ws-comfyui-001","pv_name":"pv-ws-comfyui-001","namespace":"verdantflare-station","pool_name":"comfyui-workspaces","app_id":"comfyui","app_display_name":"ComfyUI","storage_bytes":53687091200,"storage_class":"local-nvme","access_mode":"ReadWriteOnce","node_name":"verdentflare-5090","state":"retained","allowed_actions":["release"]}],"request_id":"req-1"}`)),
			}, nil
		case "/internal/v1/resources/workspaces/release":
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"status":"released","workspace":{"workspace_id":"verdantflare-station/ws-comfyui-001","pvc_name":"ws-comfyui-001","pv_name":"pv-ws-comfyui-001","namespace":"verdantflare-station","pool_name":"comfyui-workspaces","app_id":"comfyui","app_display_name":"ComfyUI","storage_bytes":53687091200,"storage_class":"local-nvme","access_mode":"ReadWriteOnce","node_name":"verdentflare-5090","state":"available","allowed_actions":[]},"request_id":"req-2"}`)),
			}, nil
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
			return nil, nil
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	wsResp, err := s.Workspaces(context.Background(), i)
	if err != nil || len(wsResp.Workspaces) != 1 || wsResp.Workspaces[0].PVCName != "ws-comfyui-001" {
		t.Fatalf("unexpected workspaces result: %+v, err=%v", wsResp, err)
	}
	relResp, err := s.ReleaseWorkspace(context.Background(), i, ReleaseWorkspaceInput{
		IdempotencyKey: "00000000-0000-4000-8000-000000000010",
		Namespace:      "verdantflare-station",
		PVCName:        "ws-comfyui-001",
	})
	if err != nil || relResp.Status != "released" || relResp.Workspace.PVCName != "ws-comfyui-001" {
		t.Fatalf("unexpected release result: %+v, err=%v", relResp, err)
	}
}

