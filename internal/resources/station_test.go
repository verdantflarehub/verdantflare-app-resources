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

func TestStationGPUSchedulerAndResidencyDelegation(t *testing.T) {
	i := Identity{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003"}
	token := strings.Repeat("s", 32)
	s, err := NewStationSource("https://station.example", token, roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-User-Id") != i.UserID || r.Header.Get("X-Organization-Id") != i.OrganizationID || r.Header.Get("X-Station-Id") != i.StationID || r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatal("wrong authority envelope")
		}
		switch r.URL.Path {
		case "/internal/v1/resources/gpu-scheduler":
			return &http.Response{
				StatusCode: 200,
				Body: io.NopCloser(strings.NewReader(`{
					"snapshot":{
						"station_id":"00000000-0000-4000-8000-000000000003",
						"app_gpu_quota":8,
						"cutover_completed":true,
						"generated_at":"2026-11-15T04:00:00Z",
						"cards":[{"node_uid":"node-1","node_name":"verdentflare-5090","gpu_uuid":"GPU-0001","gpu_index":0,"model_name":"NVIDIA GeForce RTX 5090","total_vram_mib":32607,"driver_overhead_mib":700,"headroom_mib":2048,"pinned_mib":6144,"leased_mib":0,"evictable_mib":11800,"quarantined_mib":0,"ledger_uncommitted_mib":23715,"measured_free_vram_mib":14000,"measured_used_vram_mib":18607,"grantable_standard_mib":11952,"telemetry_fresh":true,"admission_open":true,"telemetry_sample_id":42,"last_event_seq":9}],
						"active_leases":[],
						"residencies":[{"instance_id":"comfyui-main","app_id":"comfyui","node_uid":"node-1","pod_uid":"pod-1","boot_id":"boot-abc","gpu_epoch":2,"residency_seq":5,"gpu_uuid":"GPU-0001","residency_state":"idle_evictable","evictable":true,"proven_vram_mib":11800}],
						"recent_events":[]
					},
					"viewer_can_force_drain":true,
					"allowed_actions":["evict","force_drain"],
					"request_id":"req-gpu-1"
				}`)),
			}, nil
		case "/internal/v1/resources/gpu-residency/evict":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), `"blender-viewport"`) {
				return &http.Response{
					StatusCode: 409,
					Body:       io.NopCloser(strings.NewReader(`{"code":"GUI_EDIT_LEASE_HELD","message":"protected"}`)),
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"result":{"instance_id":"comfyui-main","app_id":"comfyui","from_state":"idle_evictable","residency_state":"zero_vram_standby","boot_id":"boot-abc","gpu_epoch":3,"residency_seq":6,"reclaimed_vram_mib":11800,"ledger_event_seq":10},"request_id":"req-evict-1"}`)),
			}, nil
		case "/internal/v1/resources/gpu-residency/force-drain":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), `"confirm_force_drain":false`) {
				return &http.Response{
					StatusCode: 400,
					Body:       io.NopCloser(strings.NewReader(`{"code":"CONFIRMATION_REQUIRED","message":"confirm required"}`)),
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"result":{"instance_id":"blender-viewport","app_id":"blender","from_state":"human_protected","residency_state":"zero_vram_standby","boot_id":"boot-bld","gpu_epoch":4,"residency_seq":8,"reclaimed_vram_mib":6144,"ledger_event_seq":11},"request_id":"req-drain-1"}`)),
			}, nil
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
			return nil, nil
		}
	}))
	if err != nil {
		t.Fatal(err)
	}

	snap, err := s.GPUScheduler(context.Background(), i)
	if err != nil || !snap.ViewerCanForceDrain || len(snap.Snapshot.Cards) != 1 || snap.Snapshot.Cards[0].GrantableStandardMiB != 11952 {
		t.Fatalf("unexpected GPU scheduler response: %+v, err=%v", snap, err)
	}

	ev, err := s.EvictGPUResidency(context.Background(), i, EvictGPUResidencyInput{IdempotencyKey: "idem-1", InstanceID: "comfyui-main"})
	if err != nil || ev.Result.BootID != "boot-abc" || ev.Result.ResidencyState != "zero_vram_standby" {
		t.Fatalf("unexpected evict response: %+v, err=%v", ev, err)
	}

	_, err = s.EvictGPUResidency(context.Background(), i, EvictGPUResidencyInput{IdempotencyKey: "idem-2", InstanceID: "blender-viewport"})
	if se, ok := err.(*SourceError); !ok || se.Status != 409 || se.Code != "GUI_EDIT_LEASE_HELD" {
		t.Fatalf("expected 409 GUI_EDIT_LEASE_HELD, got %v", err)
	}

	_, err = s.ForceDrainGPUResidency(context.Background(), i, ForceDrainGPUResidencyInput{
		IdempotencyKey: "idem-3", InstanceID: "blender-viewport", ConfirmForceDrain: false, ConfirmInstanceID: "blender-viewport",
	})
	if se, ok := err.(*SourceError); !ok || se.Status != 400 || se.Code != "CONFIRMATION_REQUIRED" {
		t.Fatalf("expected 400 CONFIRMATION_REQUIRED, got %v", err)
	}

	dr, err := s.ForceDrainGPUResidency(context.Background(), i, ForceDrainGPUResidencyInput{
		IdempotencyKey: "idem-4", InstanceID: "blender-viewport", ConfirmForceDrain: true, ConfirmInstanceID: "blender-viewport",
	})
	if err != nil || dr.Result.ResidencyState != "zero_vram_standby" || dr.Result.BootID != "boot-bld" {
		t.Fatalf("unexpected force-drain response: %+v, err=%v", dr, err)
	}
}

func TestStationModelsPaginationHostPathGuardAndEvictDelegation(t *testing.T) {
	i := Identity{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003"}
	token := strings.Repeat("s", 32)
	shaReady := strings.Repeat("a", 64)
	shaPinned := strings.Repeat("b", 64)
	leakHostPath := false

	s, err := NewStationSource("https://station.example", token, roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-User-Id") != i.UserID || r.Header.Get("X-Organization-Id") != i.OrganizationID || r.Header.Get("X-Station-Id") != i.StationID || r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatal("wrong authority envelope")
		}
		switch r.URL.Path {
		case "/internal/v1/resources/models":
			if leakHostPath {
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(`{"schema_version":1,"mount_path":"/models","watermark":{"reserved_headroom_bytes":214748364800},"models":[{"catalog_code":"MDL-CKPT-SDXL-001","sha256":"` + shaReady + `","relative_view_path":"/data/models/blob"}],"total_count":1}`)),
				}, nil
			}
			if r.URL.Query().Get("cursor") == "" {
				return &http.Response{
					StatusCode: 200,
					Body: io.NopCloser(strings.NewReader(`{
						"schema_version":1,
						"mount_path":"/models",
						"watermark":{"filesystem_total_bytes":1099511627776,"filesystem_free_bytes":536870912000,"cached_ready_bytes":12884901888,"unmaterialized_reserved_bytes":0,"reserved_headroom_bytes":214748364800,"max_auto_pull_model_bytes":26843545600,"headroom_compliant":true,"sampled_at":"2026-11-15T06:00:00Z"},
						"models":[{"catalog_code":"MDL-CKPT-SDXL-001","model_id":"sdxl_base_1.0","display_name":"SDXL Base 1.0","category":"CKPT","category_dir":"checkpoints","filename":"sd_xl_base_1.0.safetensors","relative_view_path":"checkpoints/` + shaReady[:12] + `__sd_xl_base_1.0.safetensors","sha256":"` + shaReady + `","size_bytes":6938078334,"vram_footprint_mib":7168,"license":"openrail++","license_tier":"open","paid_access":false,"origin_kind":"public","approval_state":"approved","status":"ready","download_fence":3,"active_pins":0,"reserved_pins":0,"pin_scopes":[]}],
						"next_cursor":"MDL-CKPT-SDXL-001",
						"total_count":2,
						"request_id":"req-m-1"
					}`)),
				}, nil
			}
			if r.URL.Query().Get("cursor") == "MDL-CKPT-SDXL-001" {
				return &http.Response{
					StatusCode: 200,
					Body: io.NopCloser(strings.NewReader(`{
						"schema_version":1,
						"mount_path":"/models",
						"watermark":{"filesystem_total_bytes":1099511627776,"filesystem_free_bytes":536870912000,"cached_ready_bytes":12884901888,"unmaterialized_reserved_bytes":0,"reserved_headroom_bytes":214748364800,"max_auto_pull_model_bytes":26843545600,"headroom_compliant":true,"sampled_at":"2026-11-15T06:00:00Z"},
						"models":[{"catalog_code":"MDL-LORA-SDXL-002","model_id":"xie_xiaoe_lora","display_name":"Xie Xiaoe Style LoRA","category":"LORA","category_dir":"loras","filename":"xie_xiaoe_v1.safetensors","relative_view_path":"loras/` + shaPinned[:12] + `__xie_xiaoe_v1.safetensors","sha256":"` + shaPinned + `","size_bytes":228589568,"vram_footprint_mib":512,"license":"apache-2.0","license_tier":"open","paid_access":false,"origin_kind":"local","approval_state":"approved","status":"ready","download_fence":5,"active_pins":1,"reserved_pins":1,"pin_scopes":["published_api","workflow_run"]}],
						"total_count":2,
						"request_id":"req-m-2"
					}`)),
				}, nil
			}
			t.Fatalf("unexpected cursor: %s", r.URL.RawQuery)
			return nil, nil
		case "/internal/v1/resources/models/" + shaReady + "/evict":
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"status":"evicted","sha256":"` + shaReady + `","request_id":"req-ev-ok"}`)),
			}, nil
		case "/internal/v1/resources/models/" + shaPinned + "/evict":
			return &http.Response{
				StatusCode: 409,
				Body:       io.NopCloser(strings.NewReader(`{"code":"MODEL_IN_USE","message":"Model cache blob is currently pinned by an active task or published workflow API","active_pins":1,"reserved_pins":1,"request_id":"req-ev-409"}`)),
			}, nil
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
			return nil, nil
		}
	}))
	if err != nil {
		t.Fatal(err)
	}

	modelsResp, err := s.Models(context.Background(), i)
	if err != nil || len(modelsResp.Models) != 2 || modelsResp.TotalCount != 2 || modelsResp.Models[1].OriginKind != "local" {
		t.Fatalf("unexpected paginated models response: %+v, err=%v", modelsResp, err)
	}

	evOK, err := s.EvictModel(context.Background(), i, EvictModelInput{IdempotencyKey: "evict-idem-0001", SHA256: shaReady})
	if err != nil || evOK.Status != "evicted" || evOK.SHA256 != shaReady {
		t.Fatalf("unexpected model evict response: %+v, err=%v", evOK, err)
	}

	_, err = s.EvictModel(context.Background(), i, EvictModelInput{IdempotencyKey: "evict-idem-0002", SHA256: shaPinned})
	if se, ok := err.(*SourceError); !ok || se.Status != 409 || se.Code != "MODEL_IN_USE" || se.ActivePins != 1 || se.ReservedPins != 1 {
		t.Fatalf("expected 409 MODEL_IN_USE with pin counts, got %+v", err)
	}

	leakHostPath = true
	if _, err = s.Models(context.Background(), i); err == nil {
		t.Fatal("expected fail-closed error on host path leak /data/models")
	}
}

