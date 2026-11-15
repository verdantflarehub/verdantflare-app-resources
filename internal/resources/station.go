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

var (
	identifier       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	modelSHA256Regex = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func ValidIdentity(i Identity) bool {
	return identifier.MatchString(i.UserID) && identifier.MatchString(i.OrganizationID) && identifier.MatchString(i.StationID)
}

func ValidModelSHA256(value string) bool {
	return modelSHA256Regex.MatchString(value)
}

type SourceError struct {
	Status       int
	Code         string
	Message      string
	ActivePins   int
	ReservedPins int
}

func sourceErr(status int, code string) *SourceError {
	return &SourceError{Status: status, Code: code}
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
		return Snapshot{}, sourceErr(403, "FORBIDDEN")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return Snapshot{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	req.Header.Set("Authorization", "Bearer "+s.credential)
	req.Header.Set("X-User-Id", identity.UserID)
	req.Header.Set("X-Organization-Id", identity.OrganizationID)
	req.Header.Set("X-Station-Id", identity.StationID)
	req.Header.Set("Accept", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return Snapshot{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return Snapshot{}, sourceErr(403, "FORBIDDEN")
	}
	if response.StatusCode != http.StatusOK {
		return Snapshot{}, sourceErr(502, "TELEMETRY_UNAVAILABLE")
	}
	const max = 4 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || len(body) > max {
		return Snapshot{}, sourceErr(502, "TELEMETRY_INVALID")
	}
	var raw Snapshot
	if json.Unmarshal(body, &raw) != nil {
		return Snapshot{}, sourceErr(502, "TELEMETRY_INVALID")
	}
	return raw, nil
}

func (s *StationSource) Workspaces(ctx context.Context, identity Identity) (WorkspacesResponse, error) {
	if !ValidIdentity(identity) {
		return WorkspacesResponse{}, sourceErr(403, "FORBIDDEN")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/internal/v1/resources/workspaces", nil)
	if err != nil {
		return WorkspacesResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	req.Header.Set("Authorization", "Bearer "+s.credential)
	req.Header.Set("X-User-Id", identity.UserID)
	req.Header.Set("X-Organization-Id", identity.OrganizationID)
	req.Header.Set("X-Station-Id", identity.StationID)
	req.Header.Set("Accept", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return WorkspacesResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return WorkspacesResponse{}, sourceErr(403, "FORBIDDEN")
	}
	if response.StatusCode != http.StatusOK {
		return WorkspacesResponse{}, sourceErr(502, "TELEMETRY_UNAVAILABLE")
	}
	const max = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || len(body) > max {
		return WorkspacesResponse{}, sourceErr(502, "TELEMETRY_INVALID")
	}
	var out WorkspacesResponse
	if json.Unmarshal(body, &out) != nil || out.Workspaces == nil {
		return WorkspacesResponse{}, sourceErr(502, "TELEMETRY_INVALID")
	}
	return out, nil
}

func (s *StationSource) ReleaseWorkspace(ctx context.Context, identity Identity, input ReleaseWorkspaceInput) (ReleaseWorkspaceResponse, error) {
	if !ValidIdentity(identity) {
		return ReleaseWorkspaceResponse{}, sourceErr(403, "FORBIDDEN")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return ReleaseWorkspaceResponse{}, sourceErr(400, "INVALID_ARGUMENT")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/internal/v1/resources/workspaces/release", bytes.NewReader(payload))
	if err != nil {
		return ReleaseWorkspaceResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	req.Header.Set("Authorization", "Bearer "+s.credential)
	req.Header.Set("X-User-Id", identity.UserID)
	req.Header.Set("X-Organization-Id", identity.OrganizationID)
	req.Header.Set("X-Station-Id", identity.StationID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return ReleaseWorkspaceResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	defer response.Body.Close()
	const max = 64 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || len(body) > max {
		return ReleaseWorkspaceResponse{}, sourceErr(502, "TELEMETRY_INVALID")
	}
	if response.StatusCode == http.StatusOK {
		var out ReleaseWorkspaceResponse
		if json.Unmarshal(body, &out) != nil || out.Status == "" {
			return ReleaseWorkspaceResponse{}, sourceErr(502, "TELEMETRY_INVALID")
		}
		return out, nil
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return ReleaseWorkspaceResponse{}, sourceErr(403, "FORBIDDEN")
	}
	var errPayload struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &errPayload)
	if errPayload.Code == "" {
		errPayload.Code = "SERVICE_UNAVAILABLE"
	}
	if response.StatusCode == 400 || response.StatusCode == 404 || response.StatusCode == 409 {
		return ReleaseWorkspaceResponse{}, sourceErr(response.StatusCode, errPayload.Code)
	}
	return ReleaseWorkspaceResponse{}, sourceErr(502, "TELEMETRY_UNAVAILABLE")
}

func (s *StationSource) GPUScheduler(ctx context.Context, identity Identity) (GPUSchedulerResponse, error) {
	if !ValidIdentity(identity) {
		return GPUSchedulerResponse{}, sourceErr(403, "FORBIDDEN")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/internal/v1/resources/gpu-scheduler", nil)
	if err != nil {
		return GPUSchedulerResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	req.Header.Set("Authorization", "Bearer "+s.credential)
	req.Header.Set("X-User-Id", identity.UserID)
	req.Header.Set("X-Organization-Id", identity.OrganizationID)
	req.Header.Set("X-Station-Id", identity.StationID)
	req.Header.Set("Accept", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return GPUSchedulerResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return GPUSchedulerResponse{}, sourceErr(403, "FORBIDDEN")
	}
	if response.StatusCode == 503 {
		return GPUSchedulerResponse{}, sourceErr(503, "SERVICE_UNAVAILABLE")
	}
	if response.StatusCode != http.StatusOK {
		return GPUSchedulerResponse{}, sourceErr(502, "TELEMETRY_UNAVAILABLE")
	}
	const max = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || len(body) > max {
		return GPUSchedulerResponse{}, sourceErr(502, "TELEMETRY_INVALID")
	}
	var out GPUSchedulerResponse
	if json.Unmarshal(body, &out) != nil || out.Snapshot.GeneratedAt == "" || out.AllowedActions == nil {
		return GPUSchedulerResponse{}, sourceErr(502, "TELEMETRY_INVALID")
	}
	if out.Snapshot.Cards == nil {
		out.Snapshot.Cards = []GPUSchedulerCard{}
	}
	if out.Snapshot.ActiveLeases == nil {
		out.Snapshot.ActiveLeases = []GPUSchedulerLease{}
	}
	if out.Snapshot.Residencies == nil {
		out.Snapshot.Residencies = []GPUSchedulerResidency{}
	}
	if out.Snapshot.RecentEvents == nil {
		out.Snapshot.RecentEvents = []GPUSchedulerEvent{}
	}
	return out, nil
}

func (s *StationSource) EvictGPUResidency(ctx context.Context, identity Identity, input EvictGPUResidencyInput) (GPUResidencyActionResponse, error) {
	return s.postGPUResidencyAction(ctx, identity, "/internal/v1/resources/gpu-residency/evict", input)
}

func (s *StationSource) ForceDrainGPUResidency(ctx context.Context, identity Identity, input ForceDrainGPUResidencyInput) (GPUResidencyActionResponse, error) {
	return s.postGPUResidencyAction(ctx, identity, "/internal/v1/resources/gpu-residency/force-drain", input)
}

func (s *StationSource) postGPUResidencyAction(ctx context.Context, identity Identity, path string, input any) (GPUResidencyActionResponse, error) {
	if !ValidIdentity(identity) {
		return GPUResidencyActionResponse{}, sourceErr(403, "FORBIDDEN")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return GPUResidencyActionResponse{}, sourceErr(400, "INVALID_ARGUMENT")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return GPUResidencyActionResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	req.Header.Set("Authorization", "Bearer "+s.credential)
	req.Header.Set("X-User-Id", identity.UserID)
	req.Header.Set("X-Organization-Id", identity.OrganizationID)
	req.Header.Set("X-Station-Id", identity.StationID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return GPUResidencyActionResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	defer response.Body.Close()
	const max = 64 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || len(body) > max {
		return GPUResidencyActionResponse{}, sourceErr(502, "TELEMETRY_INVALID")
	}
	if response.StatusCode == http.StatusOK {
		var out GPUResidencyActionResponse
		if json.Unmarshal(body, &out) != nil || out.Result.InstanceID == "" {
			return GPUResidencyActionResponse{}, sourceErr(502, "TELEMETRY_INVALID")
		}
		return out, nil
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return GPUResidencyActionResponse{}, sourceErr(403, "FORBIDDEN")
	}
	var errPayload struct {
		Code      string `json:"code"`
		ErrorCode string `json:"error_code"`
	}
	_ = json.Unmarshal(body, &errPayload)
	code := errPayload.Code
	if code == "" {
		code = errPayload.ErrorCode
	}
	if code == "" {
		code = "SERVICE_UNAVAILABLE"
	}
	if response.StatusCode == 400 || response.StatusCode == 404 || response.StatusCode == 409 || response.StatusCode == 503 {
		return GPUResidencyActionResponse{}, sourceErr(response.StatusCode, code)
	}
	return GPUResidencyActionResponse{}, sourceErr(502, "TELEMETRY_UNAVAILABLE")
}

func (s *StationSource) Models(ctx context.Context, identity Identity) (ModelsResponse, error) {
	if !ValidIdentity(identity) {
		return ModelsResponse{}, sourceErr(403, "FORBIDDEN")
	}
	const maxPages = 20
	const maxPageBytes = 2 << 20
	var out ModelsResponse
	cursor := ""
	for pageIdx := 0; pageIdx < maxPages; pageIdx++ {
		query := "limit=100"
		if cursor != "" {
			query += "&cursor=" + url.QueryEscape(cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/internal/v1/resources/models?"+query, nil)
		if err != nil {
			return ModelsResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
		}
		req.Header.Set("Authorization", "Bearer "+s.credential)
		req.Header.Set("X-User-Id", identity.UserID)
		req.Header.Set("X-Organization-Id", identity.OrganizationID)
		req.Header.Set("X-Station-Id", identity.StationID)
		req.Header.Set("Accept", "application/json")
		response, err := s.client.Do(req)
		if err != nil {
			return ModelsResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxPageBytes+1))
		response.Body.Close()
		if response.StatusCode == 401 || response.StatusCode == 403 {
			return ModelsResponse{}, sourceErr(403, "FORBIDDEN")
		}
		if response.StatusCode == 503 {
			return ModelsResponse{}, sourceErr(503, "SERVICE_UNAVAILABLE")
		}
		if response.StatusCode != http.StatusOK {
			return ModelsResponse{}, sourceErr(502, "TELEMETRY_UNAVAILABLE")
		}
		if readErr != nil || len(body) > maxPageBytes || strings.Contains(string(body), "/data/models") {
			return ModelsResponse{}, sourceErr(502, "TELEMETRY_INVALID")
		}
		var page struct {
			SchemaVersion int              `json:"schema_version"`
			MountPath     string           `json:"mount_path"`
			Watermark     StorageWatermark `json:"watermark"`
			Models        []ModelCacheItem `json:"models"`
			NextCursor    string           `json:"next_cursor"`
			TotalCount    int              `json:"total_count"`
			RequestID     string           `json:"request_id"`
		}
		if json.Unmarshal(body, &page) != nil || page.SchemaVersion != 1 || page.MountPath != "/models" || page.Models == nil {
			return ModelsResponse{}, sourceErr(502, "TELEMETRY_INVALID")
		}
		for i := range page.Models {
			if page.Models[i].PinScopes == nil {
				page.Models[i].PinScopes = []string{}
			}
			if !ValidModelSHA256(page.Models[i].SHA256) || strings.Contains(page.Models[i].RelativeViewPath, "..") || strings.HasPrefix(page.Models[i].RelativeViewPath, "/") {
				return ModelsResponse{}, sourceErr(502, "TELEMETRY_INVALID")
			}
		}
		if pageIdx == 0 {
			out = ModelsResponse{
				SchemaVersion: page.SchemaVersion,
				MountPath:     page.MountPath,
				Watermark:     page.Watermark,
				Models:        make([]ModelCacheItem, 0, len(page.Models)),
				TotalCount:    page.TotalCount,
				RequestID:     page.RequestID,
			}
		} else {
			out.Watermark = page.Watermark
			out.TotalCount = page.TotalCount
		}
		out.Models = append(out.Models, page.Models...)
		if page.NextCursor == "" {
			return out, nil
		}
		if len(page.Models) == 0 || (cursor != "" && page.NextCursor <= cursor) {
			return ModelsResponse{}, sourceErr(502, "TELEMETRY_INVALID")
		}
		cursor = page.NextCursor
	}
	out.Truncated = true
	return out, nil
}

func (s *StationSource) EvictModel(ctx context.Context, identity Identity, input EvictModelInput) (EvictModelResponse, error) {
	if !ValidIdentity(identity) {
		return EvictModelResponse{}, sourceErr(403, "FORBIDDEN")
	}
	if !ValidModelSHA256(input.SHA256) || len(input.IdempotencyKey) < 8 || len(input.IdempotencyKey) > 128 || strings.ContainsAny(input.IdempotencyKey, " \t\r\n") {
		return EvictModelResponse{}, sourceErr(400, "INVALID_ARGUMENT")
	}
	payload, err := json.Marshal(struct {
		IdempotencyKey string `json:"idempotency_key"`
	}{IdempotencyKey: input.IdempotencyKey})
	if err != nil {
		return EvictModelResponse{}, sourceErr(400, "INVALID_ARGUMENT")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/internal/v1/resources/models/"+input.SHA256+"/evict", bytes.NewReader(payload))
	if err != nil {
		return EvictModelResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	req.Header.Set("Authorization", "Bearer "+s.credential)
	req.Header.Set("X-User-Id", identity.UserID)
	req.Header.Set("X-Organization-Id", identity.OrganizationID)
	req.Header.Set("X-Station-Id", identity.StationID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return EvictModelResponse{}, sourceErr(503, "STATION_UNAVAILABLE")
	}
	defer response.Body.Close()
	const max = 64 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || len(body) > max || strings.Contains(string(body), "/data/models") {
		return EvictModelResponse{}, sourceErr(502, "TELEMETRY_INVALID")
	}
	if response.StatusCode == http.StatusOK {
		var out EvictModelResponse
		if json.Unmarshal(body, &out) != nil || out.Status == "" {
			return EvictModelResponse{}, sourceErr(502, "TELEMETRY_INVALID")
		}
		if out.SHA256 == "" {
			out.SHA256 = input.SHA256
		}
		if out.Model != nil && out.Model.PinScopes == nil {
			out.Model.PinScopes = []string{}
		}
		return out, nil
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return EvictModelResponse{}, sourceErr(403, "FORBIDDEN")
	}
	var errPayload struct {
		Code         string `json:"code"`
		ErrorCode    string `json:"error_code"`
		Message      string `json:"message"`
		ActivePins   int    `json:"active_pins"`
		ReservedPins int    `json:"reserved_pins"`
	}
	_ = json.Unmarshal(body, &errPayload)
	code := errPayload.Code
	if code == "" {
		code = errPayload.ErrorCode
	}
	if code == "" {
		code = "SERVICE_UNAVAILABLE"
	}
	if response.StatusCode == 400 || response.StatusCode == 404 || response.StatusCode == 409 || response.StatusCode == 503 {
		return EvictModelResponse{}, &SourceError{
			Status:       response.StatusCode,
			Code:         code,
			Message:      strings.TrimSpace(errPayload.Message),
			ActivePins:   errPayload.ActivePins,
			ReservedPins: errPayload.ReservedPins,
		}
	}
	return EvictModelResponse{}, sourceErr(502, "TELEMETRY_UNAVAILABLE")
}

