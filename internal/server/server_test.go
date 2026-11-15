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

type mockGPUSchedulerSource struct {
	sourceFunc
	gpuScheduler func(context.Context, resources.Identity) (resources.GPUSchedulerResponse, error)
	evict        func(context.Context, resources.Identity, resources.EvictGPUResidencyInput) (resources.GPUResidencyActionResponse, error)
	forceDrain   func(context.Context, resources.Identity, resources.ForceDrainGPUResidencyInput) (resources.GPUResidencyActionResponse, error)
}

func (m mockGPUSchedulerSource) GPUScheduler(ctx context.Context, i resources.Identity) (resources.GPUSchedulerResponse, error) {
	return m.gpuScheduler(ctx, i)
}

func (m mockGPUSchedulerSource) EvictGPUResidency(ctx context.Context, i resources.Identity, in resources.EvictGPUResidencyInput) (resources.GPUResidencyActionResponse, error) {
	return m.evict(ctx, i, in)
}

func (m mockGPUSchedulerSource) ForceDrainGPUResidency(ctx context.Context, i resources.Identity, in resources.ForceDrainGPUResidencyInput) (resources.GPUResidencyActionResponse, error) {
	return m.forceDrain(ctx, i, in)
}

func TestGPUSchedulerAndResidencyEndpoints(t *testing.T) {
	station := "00000000-0000-4000-8000-000000000003"
	token := strings.Repeat("a", 32)
	src := mockGPUSchedulerSource{
		sourceFunc: func(_ context.Context, _ resources.Identity) (resources.Snapshot, error) {
			return resources.Snapshot{SchemaVersion: 1, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), HardwareState: "unavailable", Workloads: []resources.Workload{}}, nil
		},
		gpuScheduler: func(_ context.Context, _ resources.Identity) (resources.GPUSchedulerResponse, error) {
			return resources.GPUSchedulerResponse{
				Snapshot: resources.GPUSchedulerSnapshot{
					StationID:        station,
					AppGPUQuota:      8,
					CutoverCompleted: true,
					GeneratedAt:      "2026-11-15T04:00:00Z",
					Cards: []resources.GPUSchedulerCard{
						{GPUUUID: "GPU-0001", GPUIndex: 0, TotalVRAMMiB: 32607, TelemetryFresh: false, AdmissionOpen: false, GrantableStandardMiB: 0},
					},
					ActiveLeases: []resources.GPUSchedulerLease{},
					Residencies:  []resources.GPUSchedulerResidency{},
					RecentEvents: []resources.GPUSchedulerEvent{},
				},
				ViewerCanForceDrain: false,
				AllowedActions:      []string{"evict"},
			}, nil
		},
		evict: func(_ context.Context, _ resources.Identity, in resources.EvictGPUResidencyInput) (resources.GPUResidencyActionResponse, error) {
			if in.InstanceID == "blender-s1" {
				return resources.GPUResidencyActionResponse{}, &resources.SourceError{Status: 409, Code: "GUI_EDIT_LEASE_HELD"}
			}
			return resources.GPUResidencyActionResponse{
				Result: resources.GPUResidencyActionResult{
					InstanceID:     in.InstanceID,
					AppID:          "comfyui",
					FromState:      "idle_evictable",
					ResidencyState: "zero_vram_standby",
					BootID:         "boot-keep-1",
					GPUEpoch:       3,
					ResidencySeq:   7,
				},
			}, nil
		},
		forceDrain: func(_ context.Context, i resources.Identity, in resources.ForceDrainGPUResidencyInput) (resources.GPUResidencyActionResponse, error) {
			if i.UserID == "00000000-0000-4000-8000-000000000099" {
				return resources.GPUResidencyActionResponse{}, &resources.SourceError{Status: 403, Code: "FORBIDDEN"}
			}
			if !in.ConfirmForceDrain || in.ConfirmInstanceID != in.InstanceID {
				return resources.GPUResidencyActionResponse{}, &resources.SourceError{Status: 400, Code: "CONFIRMATION_REQUIRED"}
			}
			return resources.GPUResidencyActionResponse{
				Result: resources.GPUResidencyActionResult{
					InstanceID:     in.InstanceID,
					AppID:          "blender",
					FromState:      "human_protected",
					ResidencyState: "zero_vram_standby",
					BootID:         "boot-keep-2",
					GPUEpoch:       4,
					ResidencySeq:   8,
				},
			}, nil
		},
	}
	s, err := New(token, station, resources.Service{Source: src}, nil)
	if err != nil {
		t.Fatal(err)
	}

	setHeaders := func(r *http.Request, userID string) {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-User-Id", userID)
		r.Header.Set("X-Organization-Id", "00000000-0000-4000-8000-000000000002")
		r.Header.Set("X-Station-Id", station)
	}

	// Pure-path check: query params rejected with 400 INVALID_REQUEST
	rq := httptest.NewRequest(http.MethodGet, "/api/v1/gpu-scheduler?foo=1", nil)
	setHeaders(rq, "00000000-0000-4000-8000-000000000001")
	wq := httptest.NewRecorder()
	s.ServeHTTP(wq, rq)
	if wq.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for query string, got %d %s", wq.Code, wq.Body.String())
	}

	// GET /api/v1/gpu-scheduler
	r1 := httptest.NewRequest(http.MethodGet, "/api/v1/gpu-scheduler", nil)
	setHeaders(r1, "00000000-0000-4000-8000-000000000001")
	w1 := httptest.NewRecorder()
	s.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK || !strings.Contains(w1.Body.String(), `"GPU-0001"`) {
		t.Fatalf("unexpected GET /api/v1/gpu-scheduler response: %d %s", w1.Code, w1.Body.String())
	}

	// POST /api/v1/gpu-residency/evict without expected_* target version -> 400 INVALID_ARGUMENT
	rNoVer := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-residency/evict", strings.NewReader(`{"idempotency_key":"idem-nover","instance_id":"comfyui-s2"}`))
	setHeaders(rNoVer, "00000000-0000-4000-8000-000000000001")
	wNoVer := httptest.NewRecorder()
	s.ServeHTTP(wNoVer, rNoVer)
	if wNoVer.Code != http.StatusBadRequest || !strings.Contains(wNoVer.Body.String(), `"INVALID_ARGUMENT"`) {
		t.Fatalf("expected 400 INVALID_ARGUMENT when expected_* missing on evict, got %d %s", wNoVer.Code, wNoVer.Body.String())
	}

	// POST /api/v1/gpu-residency/force-drain without expected_* target version -> 400 INVALID_ARGUMENT
	rNoVerFD := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-residency/force-drain", strings.NewReader(`{"idempotency_key":"idem-nover-fd","instance_id":"blender-s1","confirm_force_drain":true,"confirm_instance_id":"blender-s1"}`))
	setHeaders(rNoVerFD, "00000000-0000-4000-8000-000000000001")
	wNoVerFD := httptest.NewRecorder()
	s.ServeHTTP(wNoVerFD, rNoVerFD)
	if wNoVerFD.Code != http.StatusBadRequest || !strings.Contains(wNoVerFD.Body.String(), `"INVALID_ARGUMENT"`) {
		t.Fatalf("expected 400 INVALID_ARGUMENT when expected_* missing on force-drain, got %d %s", wNoVerFD.Code, wNoVerFD.Body.String())
	}

	// POST /api/v1/gpu-residency/evict -> 409 GUI_EDIT_LEASE_HELD
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-residency/evict", strings.NewReader(`{"idempotency_key":"idem-1","instance_id":"blender-s1","expected_boot_id":"boot-s1","expected_gpu_epoch":3,"expected_residency_seq":7}`))
	setHeaders(r2, "00000000-0000-4000-8000-000000000001")
	w2 := httptest.NewRecorder()
	s.ServeHTTP(w2, r2)
	if w2.Code != http.StatusConflict || !strings.Contains(w2.Body.String(), `"GUI_EDIT_LEASE_HELD"`) {
		t.Fatalf("expected 409 GUI_EDIT_LEASE_HELD, got %d %s", w2.Code, w2.Body.String())
	}

	// POST /api/v1/gpu-residency/evict -> 200 OK
	r3 := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-residency/evict", strings.NewReader(`{"idempotency_key":"idem-2","instance_id":"comfyui-s2","expected_boot_id":"boot-keep-1","expected_gpu_epoch":2,"expected_residency_seq":5}`))
	setHeaders(r3, "00000000-0000-4000-8000-000000000001")
	w3 := httptest.NewRecorder()
	s.ServeHTTP(w3, r3)
	if w3.Code != http.StatusOK || !strings.Contains(w3.Body.String(), `"boot-keep-1"`) {
		t.Fatalf("expected 200 OK evict, got %d %s", w3.Code, w3.Body.String())
	}

	// POST /api/v1/gpu-residency/force-drain -> 403 FORBIDDEN for non-admin even when confirmation missing
	r4 := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-residency/force-drain", strings.NewReader(`{"idempotency_key":"idem-3","instance_id":"blender-s1","confirm_force_drain":false,"confirm_instance_id":"","expected_boot_id":"boot-keep-2","expected_gpu_epoch":4,"expected_residency_seq":7}`))
	setHeaders(r4, "00000000-0000-4000-8000-000000000099")
	w4 := httptest.NewRecorder()
	s.ServeHTTP(w4, r4)
	if w4.Code != http.StatusForbidden {
		t.Fatalf("expected 403 FORBIDDEN, got %d %s", w4.Code, w4.Body.String())
	}

	// POST /api/v1/gpu-residency/force-drain -> 400 CONFIRMATION_REQUIRED for admin with mismatched confirm_instance_id
	r5 := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-residency/force-drain", strings.NewReader(`{"idempotency_key":"idem-4","instance_id":"blender-s1","confirm_force_drain":true,"confirm_instance_id":"wrong","expected_boot_id":"boot-keep-2","expected_gpu_epoch":4,"expected_residency_seq":7}`))
	setHeaders(r5, "00000000-0000-4000-8000-000000000001")
	w5 := httptest.NewRecorder()
	s.ServeHTTP(w5, r5)
	if w5.Code != http.StatusBadRequest || !strings.Contains(w5.Body.String(), `"CONFIRMATION_REQUIRED"`) {
		t.Fatalf("expected 400 CONFIRMATION_REQUIRED, got %d %s", w5.Code, w5.Body.String())
	}

	// POST /api/v1/gpu-residency/force-drain -> 200 OK
	r6 := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-residency/force-drain", strings.NewReader(`{"idempotency_key":"idem-5","instance_id":"blender-s1","confirm_force_drain":true,"confirm_instance_id":"blender-s1","expected_boot_id":"boot-keep-2","expected_gpu_epoch":4,"expected_residency_seq":7}`))
	setHeaders(r6, "00000000-0000-4000-8000-000000000001")
	w6 := httptest.NewRecorder()
	s.ServeHTTP(w6, r6)
	if w6.Code != http.StatusOK || !strings.Contains(w6.Body.String(), `"boot-keep-2"`) {
		t.Fatalf("expected 200 OK force-drain, got %d %s", w6.Code, w6.Body.String())
	}
}

type mockModelCacheSource struct {
	sourceFunc
	models func(context.Context, resources.Identity) (resources.ModelsResponse, error)
	evict  func(context.Context, resources.Identity, resources.EvictModelInput) (resources.EvictModelResponse, error)
}

func (m mockModelCacheSource) Models(ctx context.Context, i resources.Identity) (resources.ModelsResponse, error) {
	return m.models(ctx, i)
}

func (m mockModelCacheSource) EvictModel(ctx context.Context, i resources.Identity, in resources.EvictModelInput) (resources.EvictModelResponse, error) {
	return m.evict(ctx, i, in)
}

func TestModelsAndEvictEndpoints(t *testing.T) {
	station := "00000000-0000-4000-8000-000000000003"
	token := strings.Repeat("a", 32)
	shaReady := strings.Repeat("c", 64)
	shaPinned := strings.Repeat("d", 64)

	src := mockModelCacheSource{
		sourceFunc: func(_ context.Context, _ resources.Identity) (resources.Snapshot, error) {
			return resources.Snapshot{SchemaVersion: 1, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), HardwareState: "unavailable", Workloads: []resources.Workload{}}, nil
		},
		models: func(_ context.Context, _ resources.Identity) (resources.ModelsResponse, error) {
			return resources.ModelsResponse{
				SchemaVersion: 1,
				MountPath:     "/models",
				Watermark: resources.StorageWatermark{
					ReservedHeadroomBytes: 214748364800,
					MaxAutoPullModelBytes: 26843545600,
				},
				Models: []resources.ModelCacheItem{
					{
						CatalogCode:      "MDL-CKPT-SDXL-001",
						ModelID:          "sdxl_base_1.0",
						DisplayName:      "SDXL Base 1.0",
						Category:         "CKPT",
						CategoryDir:      "checkpoints",
						Filename:         "sd_xl_base_1.0.safetensors",
						RelativeViewPath: "checkpoints/" + shaReady[:12] + "__sd_xl_base_1.0.safetensors",
						SHA256:           shaReady,
						SizeBytes:        6938078334,
						Status:           "ready",
						OriginKind:       "public",
						PinScopes:        []string{},
					},
				},
				TotalCount: 1,
			}, nil
		},
		evict: func(_ context.Context, _ resources.Identity, in resources.EvictModelInput) (resources.EvictModelResponse, error) {
			if in.SHA256 == shaPinned {
				return resources.EvictModelResponse{}, &resources.SourceError{
					Status:       409,
					Code:         "MODEL_IN_USE",
					Message:      "Model cache blob is currently pinned by an active task or published workflow API",
					ActivePins:   2,
					ReservedPins: 1,
				}
			}
			return resources.EvictModelResponse{Status: "evicted", SHA256: in.SHA256}, nil
		},
	}

	s, err := New(token, station, resources.Service{Source: src}, nil)
	if err != nil {
		t.Fatal(err)
	}
	setHeaders := func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-User-Id", "00000000-0000-4000-8000-000000000001")
		r.Header.Set("X-Organization-Id", "00000000-0000-4000-8000-000000000002")
		r.Header.Set("X-Station-Id", station)
	}

	// Pure-path check on GET /api/v1/models
	rq := httptest.NewRequest(http.MethodGet, "/api/v1/models?category=CKPT", nil)
	setHeaders(rq)
	wq := httptest.NewRecorder()
	s.ServeHTTP(wq, rq)
	if wq.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 INVALID_REQUEST on query string, got %d %s", wq.Code, wq.Body.String())
	}

	// GET /api/v1/models -> 200 OK
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	setHeaders(rList)
	wList := httptest.NewRecorder()
	s.ServeHTTP(wList, rList)
	if wList.Code != http.StatusOK || !strings.Contains(wList.Body.String(), `"MDL-CKPT-SDXL-001"`) {
		t.Fatalf("expected 200 OK models list, got %d %s", wList.Code, wList.Body.String())
	}

	// POST /api/v1/models/evict invalid sha256 -> 400 INVALID_ARGUMENT
	rBad := httptest.NewRequest(http.MethodPost, "/api/v1/models/evict", strings.NewReader(`{"idempotency_key":"evict-bad-01","sha256":"not-a-sha"}`))
	setHeaders(rBad)
	wBad := httptest.NewRecorder()
	s.ServeHTTP(wBad, rBad)
	if wBad.Code != http.StatusBadRequest || !strings.Contains(wBad.Body.String(), `"INVALID_ARGUMENT"`) {
		t.Fatalf("expected 400 INVALID_ARGUMENT on invalid sha256, got %d %s", wBad.Code, wBad.Body.String())
	}

	// POST /api/v1/models/evict pinned -> 409 MODEL_IN_USE with active_pins & reserved_pins
	r409 := httptest.NewRequest(http.MethodPost, "/api/v1/models/evict", strings.NewReader(`{"idempotency_key":"evict-pin-01","sha256":"`+shaPinned+`"}`))
	setHeaders(r409)
	w409 := httptest.NewRecorder()
	s.ServeHTTP(w409, r409)
	if w409.Code != http.StatusConflict || !strings.Contains(w409.Body.String(), `"MODEL_IN_USE"`) || !strings.Contains(w409.Body.String(), `"active_pins":2`) || !strings.Contains(w409.Body.String(), `"reserved_pins":1`) {
		t.Fatalf("expected 409 MODEL_IN_USE with pin counts, got %d %s", w409.Code, w409.Body.String())
	}

	// POST /api/v1/models/evict ready -> 200 OK
	r200 := httptest.NewRequest(http.MethodPost, "/api/v1/models/evict", strings.NewReader(`{"idempotency_key":"evict-ok-001","sha256":"`+shaReady+`"}`))
	setHeaders(r200)
	w200 := httptest.NewRecorder()
	s.ServeHTTP(w200, r200)
	if w200.Code != http.StatusOK || !strings.Contains(w200.Body.String(), `"evicted"`) {
		t.Fatalf("expected 200 OK model evict, got %d %s", w200.Code, w200.Body.String())
	}
}

