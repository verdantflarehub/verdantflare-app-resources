package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var identifier = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func ValidIdentity(i Identity) bool {
	return identifier.MatchString(i.UserID) && identifier.MatchString(i.OrganizationID) && identifier.MatchString(i.StationID)
}

type SourceError struct {
	Status int
	Code   string
}

func (e *SourceError) Error() string { return e.Code }

// StationSource sends no browser credential and accepts no user-selected URL.
// The trusted Station endpoint revalidates current membership, admin role and
// app:read scope for every request; supplied identity headers are selectors.
type StationSource struct {
	baseURL, endpoint, credential string
	client                        *http.Client
}

func NewStationSource(base, credential string, transport http.RoundTripper) (*StationSource, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || len(credential) < 32 || strings.ContainsAny(credential, " \t\r\n") {
		return nil, errors.New("invalid Station resource configuration")
	}
	if transport == nil {
		controlled := http.DefaultTransport.(*http.Transport).Clone()
		controlled.Proxy = nil
		transport = controlled
	}
	trimmed := strings.TrimSuffix(base, "/")
	return &StationSource{baseURL: trimmed, endpoint: trimmed + "/internal/v1/resources/snapshot", credential: credential, client: &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (s *StationSource) Read(ctx context.Context, identity Identity) (Snapshot, error) {
	if !ValidIdentity(identity) {
		return Snapshot{}, &SourceError{403, "FORBIDDEN"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return Snapshot{}, &SourceError{503, "STATION_UNAVAILABLE"}
	}
	req.Header.Set("Authorization", "Bearer "+s.credential)
	req.Header.Set("X-User-Id", identity.UserID)
	req.Header.Set("X-Organization-Id", identity.OrganizationID)
	req.Header.Set("X-Station-Id", identity.StationID)
	req.Header.Set("Accept", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return Snapshot{}, &SourceError{503, "STATION_UNAVAILABLE"}
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return Snapshot{}, &SourceError{403, "FORBIDDEN"}
	}
	if response.StatusCode != http.StatusOK {
		return Snapshot{}, &SourceError{502, "TELEMETRY_UNAVAILABLE"}
	}
	const max = 4 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || len(body) > max {
		return Snapshot{}, &SourceError{502, "TELEMETRY_INVALID"}
	}
	var raw Snapshot
	if json.Unmarshal(body, &raw) != nil {
		return Snapshot{}, &SourceError{502, "TELEMETRY_INVALID"}
	}
	return raw, nil
}

func (s *StationSource) Workspaces(ctx context.Context, identity Identity) (WorkspacesResponse, error) {
	if !ValidIdentity(identity) {
		return WorkspacesResponse{}, &SourceError{403, "FORBIDDEN"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/internal/v1/resources/workspaces", nil)
	if err != nil {
		return WorkspacesResponse{}, &SourceError{503, "STATION_UNAVAILABLE"}
	}
	req.Header.Set("Authorization", "Bearer "+s.credential)
	req.Header.Set("X-User-Id", identity.UserID)
	req.Header.Set("X-Organization-Id", identity.OrganizationID)
	req.Header.Set("X-Station-Id", identity.StationID)
	req.Header.Set("Accept", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return WorkspacesResponse{}, &SourceError{503, "STATION_UNAVAILABLE"}
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return WorkspacesResponse{}, &SourceError{403, "FORBIDDEN"}
	}
	if response.StatusCode != http.StatusOK {
		return WorkspacesResponse{}, &SourceError{502, "TELEMETRY_UNAVAILABLE"}
	}
	const max = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || len(body) > max {
		return WorkspacesResponse{}, &SourceError{502, "TELEMETRY_INVALID"}
	}
	var out WorkspacesResponse
	if json.Unmarshal(body, &out) != nil || out.Workspaces == nil {
		return WorkspacesResponse{}, &SourceError{502, "TELEMETRY_INVALID"}
	}
	return out, nil
}

func (s *StationSource) ReleaseWorkspace(ctx context.Context, identity Identity, input ReleaseWorkspaceInput) (ReleaseWorkspaceResponse, error) {
	if !ValidIdentity(identity) {
		return ReleaseWorkspaceResponse{}, &SourceError{403, "FORBIDDEN"}
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return ReleaseWorkspaceResponse{}, &SourceError{400, "INVALID_ARGUMENT"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/internal/v1/resources/workspaces/release", bytes.NewReader(payload))
	if err != nil {
		return ReleaseWorkspaceResponse{}, &SourceError{503, "STATION_UNAVAILABLE"}
	}
	req.Header.Set("Authorization", "Bearer "+s.credential)
	req.Header.Set("X-User-Id", identity.UserID)
	req.Header.Set("X-Organization-Id", identity.OrganizationID)
	req.Header.Set("X-Station-Id", identity.StationID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return ReleaseWorkspaceResponse{}, &SourceError{503, "STATION_UNAVAILABLE"}
	}
	defer response.Body.Close()
	const max = 64 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || len(body) > max {
		return ReleaseWorkspaceResponse{}, &SourceError{502, "TELEMETRY_INVALID"}
	}
	if response.StatusCode == http.StatusOK {
		var out ReleaseWorkspaceResponse
		if json.Unmarshal(body, &out) != nil || out.Status == "" {
			return ReleaseWorkspaceResponse{}, &SourceError{502, "TELEMETRY_INVALID"}
		}
		return out, nil
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return ReleaseWorkspaceResponse{}, &SourceError{403, "FORBIDDEN"}
	}
	var errPayload struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &errPayload)
	if errPayload.Code == "" {
		errPayload.Code = "SERVICE_UNAVAILABLE"
	}
	if response.StatusCode == 400 || response.StatusCode == 404 || response.StatusCode == 409 {
		return ReleaseWorkspaceResponse{}, &SourceError{response.StatusCode, errPayload.Code}
	}
	return ReleaseWorkspaceResponse{}, &SourceError{502, "TELEMETRY_UNAVAILABLE"}
}

