package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"github.com/verdantflarehub/verdantflare-app-resources/internal/buildinfo"
	"github.com/verdantflarehub/verdantflare-app-resources/internal/dashboard"
	"github.com/verdantflarehub/verdantflare-app-resources/internal/resources"
	"github.com/verdantflarehub/verdantflare-app-resources/internal/strictjson"
)

type Server struct {
	credential, stationID string
	resources             resources.Service
	assets                *dashboard.Assets
}

func New(credential, stationID string, service resources.Service, assets fs.FS) (*Server, error) {
	if len(credential) < 32 || strings.ContainsAny(credential, " \t\r\n") || !resources.ValidIdentity(resources.Identity{UserID: stationID, OrganizationID: stationID, StationID: stationID}) || service.Source == nil {
		return nil, errors.New("invalid Resources configuration")
	}
	var handler *dashboard.Assets
	if assets != nil {
		var err error
		handler, err = dashboard.Load(assets, buildinfo.Version)
		if err != nil {
			return nil, errors.New("invalid Resources dashboard assets")
		}
	}
	return &Server{credential, stationID, service, handler}, nil
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func fail(w http.ResponseWriter, status int, code string) {
	reply(w, status, map[string]string{"code": code})
}

func failSourceError(w http.ResponseWriter, upstream *resources.SourceError) {
	body := map[string]any{"code": upstream.Code}
	if upstream.Message != "" {
		body["message"] = upstream.Message
	}
	if upstream.Code == "MODEL_IN_USE" || upstream.ActivePins > 0 || upstream.ReservedPins > 0 {
		body["active_pins"] = upstream.ActivePins
		body["reserved_pins"] = upstream.ReservedPins
	}
	reply(w, upstream.Status, body)
}

func validModelIdempotencyKey(value string) bool {
	return len(value) >= 8 && len(value) <= 128 && !strings.ContainsAny(value, " \t\r\n")
}

func validResidencyID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	if id == "." || id == ".." || strings.ContainsAny(id, "/\\?# \t\r\n") {
		return false
	}
	return true
}

func validExpectedResidencyVersion(bootID string, epoch, seq *int64) bool {
	trimmedBoot := strings.TrimSpace(bootID)
	if len(trimmedBoot) < 1 || len(trimmedBoot) > 128 {
		return false
	}
	if epoch == nil || *epoch < 0 || seq == nil || *seq < 0 {
		return false
	}
	return true
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/healthz" && r.Method == "GET" {
		reply(w, 200, map[string]string{"status": "ok", "version": buildinfo.Version})
		return
	}
	if len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.credential)) != 1 {
		fail(w, 401, "UNAUTHENTICATED")
		return
	}
	if r.URL.Path == "/api/v1/workspaces/release" || r.URL.Path == "/api/v1/gpu-residency/evict" || r.URL.Path == "/api/v1/gpu-residency/force-drain" || r.URL.Path == "/api/v1/models/evict" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			fail(w, 405, "METHOD_NOT_ALLOWED")
			return
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || r.Header.Get("Content-Encoding") != "" {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		identity := resources.Identity{UserID: r.Header.Get("X-User-Id"), OrganizationID: r.Header.Get("X-Organization-Id"), StationID: r.Header.Get("X-Station-Id")}
		if len(r.Header.Values("X-User-Id")) != 1 || len(r.Header.Values("X-Organization-Id")) != 1 || len(r.Header.Values("X-Station-Id")) != 1 || !resources.ValidIdentity(identity) || identity.StationID != s.stationID {
			fail(w, 403, "FORBIDDEN")
			return
		}
		switch r.URL.Path {
		case "/api/v1/workspaces/release":
			var in resources.ReleaseWorkspaceInput
			if strictjson.Decode(r.Body, 4096, &in) != nil || strings.TrimSpace(in.IdempotencyKey) == "" || strings.TrimSpace(in.Namespace) == "" || strings.TrimSpace(in.PVCName) == "" {
				fail(w, 400, "INVALID_ARGUMENT")
				return
			}
			result, err := s.resources.ReleaseWorkspace(r.Context(), identity, in)
			if err != nil {
				var upstream *resources.SourceError
				if errors.As(err, &upstream) {
					failSourceError(w, upstream)
				} else {
					fail(w, 502, "TELEMETRY_INVALID")
				}
				return
			}
			reply(w, 200, result)
			return
		case "/api/v1/gpu-residency/evict":
			var in resources.EvictGPUResidencyInput
			if strictjson.Decode(r.Body, 4096, &in) != nil || len(strings.TrimSpace(in.IdempotencyKey)) == 0 || len(in.IdempotencyKey) > 128 || !validResidencyID(in.InstanceID) || !validExpectedResidencyVersion(in.ExpectedBootID, in.ExpectedGPUEpoch, in.ExpectedResidencySeq) {
				fail(w, 400, "INVALID_ARGUMENT")
				return
			}
			result, err := s.resources.EvictGPUResidency(r.Context(), identity, in)
			if err != nil {
				var upstream *resources.SourceError
				if errors.As(err, &upstream) {
					failSourceError(w, upstream)
				} else {
					fail(w, 502, "TELEMETRY_INVALID")
				}
				return
			}
			reply(w, 200, result)
			return
		case "/api/v1/gpu-residency/force-drain":
			var in resources.ForceDrainGPUResidencyInput
			if strictjson.Decode(r.Body, 4096, &in) != nil || len(strings.TrimSpace(in.IdempotencyKey)) == 0 || len(in.IdempotencyKey) > 128 || !validResidencyID(in.InstanceID) || !validExpectedResidencyVersion(in.ExpectedBootID, in.ExpectedGPUEpoch, in.ExpectedResidencySeq) {
				fail(w, 400, "INVALID_ARGUMENT")
				return
			}
			result, err := s.resources.ForceDrainGPUResidency(r.Context(), identity, in)
			if err != nil {
				var upstream *resources.SourceError
				if errors.As(err, &upstream) {
					failSourceError(w, upstream)
				} else {
					fail(w, 502, "TELEMETRY_INVALID")
				}
				return
			}
			reply(w, 200, result)
			return
		case "/api/v1/models/evict":
			var in resources.EvictModelInput
			if strictjson.Decode(r.Body, 4096, &in) != nil || !validModelIdempotencyKey(in.IdempotencyKey) || !resources.ValidModelSHA256(in.SHA256) {
				fail(w, 400, "INVALID_ARGUMENT")
				return
			}
			result, err := s.resources.EvictModel(r.Context(), identity, in)
			if err != nil {
				var upstream *resources.SourceError
				if errors.As(err, &upstream) {
					failSourceError(w, upstream)
				} else {
					fail(w, 502, "TELEMETRY_INVALID")
				}
				return
			}
			reply(w, 200, result)
			return
		}
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, 405, "METHOD_NOT_ALLOWED")
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || r.Header.Get("Content-Encoding") != "" {
		fail(w, 400, "INVALID_REQUEST")
		return
	}
	if r.URL.Path == "/internal/dashboard/declaration" || r.URL.Path == "/internal/dashboard/index" {
		if s.assets == nil {
			fail(w, 503, "DASHBOARD_UNAVAILABLE")
			return
		}
		data := s.assets.Declaration()
		if r.URL.Path == "/internal/dashboard/index" {
			data = s.assets.Index()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/dashboard/") {
		if s.assets == nil {
			fail(w, 404, "NOT_FOUND")
			return
		}
		data, mime, ok := s.assets.File(strings.TrimPrefix(r.URL.Path, "/dashboard/"))
		if !ok {
			fail(w, 404, "NOT_FOUND")
			return
		}
		w.Header().Set("Content-Type", mime)
		_, _ = w.Write(data)
		return
	}
	if r.URL.Path != "/api/v1/dashboard" && r.URL.Path != "/api/v1/workspaces" && r.URL.Path != "/api/v1/gpu-scheduler" && r.URL.Path != "/api/v1/models" {
		fail(w, 404, "NOT_FOUND")
		return
	}
	identity := resources.Identity{UserID: r.Header.Get("X-User-Id"), OrganizationID: r.Header.Get("X-Organization-Id"), StationID: r.Header.Get("X-Station-Id")}
	if len(r.Header.Values("X-User-Id")) != 1 || len(r.Header.Values("X-Organization-Id")) != 1 || len(r.Header.Values("X-Station-Id")) != 1 || !resources.ValidIdentity(identity) || identity.StationID != s.stationID {
		fail(w, 403, "FORBIDDEN")
		return
	}
	if r.URL.Path == "/api/v1/workspaces" {
		result, err := s.resources.Workspaces(r.Context(), identity)
		if err != nil {
			var upstream *resources.SourceError
			if errors.As(err, &upstream) {
				failSourceError(w, upstream)
			} else {
				fail(w, 502, "TELEMETRY_INVALID")
			}
			return
		}
		reply(w, 200, result)
		return
	}
	if r.URL.Path == "/api/v1/gpu-scheduler" {
		result, err := s.resources.GPUScheduler(r.Context(), identity)
		if err != nil {
			var upstream *resources.SourceError
			if errors.As(err, &upstream) {
				failSourceError(w, upstream)
			} else {
				fail(w, 502, "TELEMETRY_INVALID")
			}
			return
		}
		reply(w, 200, result)
		return
	}
	if r.URL.Path == "/api/v1/models" {
		result, err := s.resources.Models(r.Context(), identity)
		if err != nil {
			var upstream *resources.SourceError
			if errors.As(err, &upstream) {
				failSourceError(w, upstream)
			} else {
				fail(w, 502, "TELEMETRY_INVALID")
			}
			return
		}
		reply(w, 200, result)
		return
	}
	// The source performs live Station authorization. Header possession or the
	// preinstalled flag alone never authorizes a global resource view.
	result, err := s.resources.Dashboard(r.Context(), identity)
	if err != nil {
		var upstream *resources.SourceError
		if errors.As(err, &upstream) {
			failSourceError(w, upstream)
		} else {
			fail(w, 502, "TELEMETRY_INVALID")
		}
		return
	}
	reply(w, 200, result)
}

