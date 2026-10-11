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
