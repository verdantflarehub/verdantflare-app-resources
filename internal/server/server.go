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
	if r.URL.Path != "/api/v1/dashboard" || r.URL.RawQuery != "" {
		fail(w, 404, "NOT_FOUND")
		return
	}
	identity := resources.Identity{UserID: r.Header.Get("X-User-Id"), OrganizationID: r.Header.Get("X-Organization-Id"), StationID: r.Header.Get("X-Station-Id")}
	if len(r.Header.Values("X-User-Id")) != 1 || len(r.Header.Values("X-Organization-Id")) != 1 || len(r.Header.Values("X-Station-Id")) != 1 || !resources.ValidIdentity(identity) || identity.StationID != s.stationID {
		fail(w, 403, "FORBIDDEN")
		return
	}
	// The source performs live Station authorization. Header possession or the
	// preinstalled flag alone never authorizes a global resource view.
	result, err := s.resources.Dashboard(r.Context(), identity)
	if err != nil {
		var upstream *resources.SourceError
		if errors.As(err, &upstream) {
			fail(w, upstream.Status, upstream.Code)
		} else {
			fail(w, 502, "TELEMETRY_INVALID")
		}
		return
	}
	reply(w, 200, result)
}
