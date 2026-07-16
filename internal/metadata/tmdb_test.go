package metadata

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGetURLRetriesWithIPv4Client(t *testing.T) {
	c := Client{
		HTTP: &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) { return nil, errors.New("network reset") })},
		IPv4HTTP: &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
		})},
	}
	res, err := c.getURL("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", res.StatusCode)
	}
}
