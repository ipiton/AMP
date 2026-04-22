package url

import (
	"net/url"
	"strings"
	"testing"
)

func TestBuildSilenceURL_EmptyExternalURL(t *testing.T) {
	result := BuildSilenceURL("", map[string]string{"alertname": "Test"})
	if result != "" {
		t.Errorf("expected empty string for empty externalURL, got %q", result)
	}
}

func TestBuildSilenceURL_NoLabels(t *testing.T) {
	result := BuildSilenceURL("http://amp.example.com", map[string]string{})
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.HasPrefix(result, "http://amp.example.com/#/silences?filter=") {
		t.Errorf("unexpected prefix: %q", result)
	}
}

func TestBuildSilenceURL_WithLabels(t *testing.T) {
	result := BuildSilenceURL("http://amp.example.com", map[string]string{
		"alertname": "HighCPU",
		"namespace": "prod",
	})

	if !strings.HasPrefix(result, "http://amp.example.com/#/silences?filter=") {
		t.Errorf("unexpected result: %q", result)
	}

	// Decode and verify filter contains both matchers
	filterEncoded := strings.TrimPrefix(result, "http://amp.example.com/#/silences?filter=")
	filter, err := url.QueryUnescape(filterEncoded)
	if err != nil {
		t.Fatalf("failed to decode filter: %v", err)
	}

	if !strings.Contains(filter, `alertname="HighCPU"`) {
		t.Errorf("filter missing alertname matcher: %q", filter)
	}
	if !strings.Contains(filter, `namespace="prod"`) {
		t.Errorf("filter missing namespace matcher: %q", filter)
	}
}

func TestBuildSilenceURL_TrailingSlash(t *testing.T) {
	withSlash := BuildSilenceURL("http://amp.example.com/", map[string]string{"alertname": "X"})
	withoutSlash := BuildSilenceURL("http://amp.example.com", map[string]string{"alertname": "X"})
	if withSlash != withoutSlash {
		t.Errorf("trailing slash should be normalized: %q vs %q", withSlash, withoutSlash)
	}
}

func TestBuildSilenceURL_SortedMatchers(t *testing.T) {
	result := BuildSilenceURL("http://amp.example.com", map[string]string{
		"zzz":       "last",
		"aaa":       "first",
		"alertname": "Middle",
	})

	filterEncoded := strings.TrimPrefix(result, "http://amp.example.com/#/silences?filter=")
	filter, _ := url.QueryUnescape(filterEncoded)

	// Verify sorted order: aaa < alertname < zzz
	posAaa := strings.Index(filter, "aaa")
	posAlert := strings.Index(filter, "alertname")
	posZzz := strings.Index(filter, "zzz")
	if posAaa > posAlert || posAlert > posZzz {
		t.Errorf("matchers not sorted alphabetically: %q", filter)
	}
}
