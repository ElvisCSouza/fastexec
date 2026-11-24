package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSetClientReadsAndCapsVersion(t *testing.T) {
	s := &Server{}
	s.setClient(json.RawMessage(`{"clientInfo":{"name":"cursor","version":"1.2.3"}}`))
	if v := s.clientVersionSafe(); v != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", v)
	}

	s.setClient(json.RawMessage(`{"clientInfo":{"name":"cursor","version":"` + strings.Repeat("v", 40) + `"}}`))
	if v := s.clientVersionSafe(); len(v) != maxClientVersion {
		t.Fatalf("long version kept %d chars, want %d", len(v), maxClientVersion)
	}

	s.setClient(json.RawMessage(`{"clientInfo":{"name":"cursor","version":"a b/c"}}`))
	if v := s.clientVersionSafe(); v != "a_b_c" {
		t.Fatalf("unsafe chars not replaced: %q", v)
	}

	s.setClient(json.RawMessage(`{"clientInfo":{"name":"cursor"}}`))
	if v := s.clientVersionSafe(); v != "" {
		t.Fatalf("missing version must stay empty, got %q", v)
	}
}

func TestWithClientVersionRoundTrip(t *testing.T) {
	out := withClientVersion(json.RawMessage(`{"path":"a"}`), "1.0")
	if v := clientVersionOf(out); v != "1.0" {
		t.Fatalf("clientVersionOf = %q, want 1.0", v)
	}
	if !strings.Contains(string(out), `"path":"a"`) {
		t.Fatalf("original argument lost: %s", out)
	}

	// No version from the client: the arguments pass through untouched.
	raw := json.RawMessage(`{"path":"a"}`)
	if string(withClientVersion(raw, "")) != string(raw) {
		t.Fatal("empty version must not change the arguments")
	}

	// An existing hidden value is kept.
	kept := withClientVersion(json.RawMessage(`{"_client_version":"2.0"}`), "1.0")
	if v := clientVersionOf(kept); v != "2.0" {
		t.Fatalf("existing _client_version replaced: %q", v)
	}

	// Non-object arguments are left alone.
	if string(withClientVersion(json.RawMessage(`[1]`), "1.0")) != `[1]` {
		t.Fatal("non-object arguments must pass untouched")
	}

	if v := clientVersionOf(json.RawMessage(`{"path":"a"}`)); v != "" {
		t.Fatalf("absent _client_version read as %q", v)
	}
}
