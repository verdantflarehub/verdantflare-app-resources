package resources

import (
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
	endpoint, credential string
	client               *http.Client
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
	return &StationSource{endpoint: strings.TrimSuffix(base, "/") + "/internal/v1/resources/snapshot", credential: credential, client: &http.Client{Timeout: 8 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
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
