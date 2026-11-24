package corpus

import (
	"strings"
	"testing"
)

// Canary for the device pattern: the third positional literal of Device,
// BulbDevice and OutletDevice is masked. The value is a fake local key.
const deviceCanary = "CANARY_LOCALKEY_9f3a"

func TestDeviceLiteralKeyIsMasked(t *testing.T) {
	cases := map[string]string{
		`d = Device("bf0000000000000000abcd", "192.0.2.10", "` + deviceCanary + `")`:      `d = Device("bf0000000000000000abcd", "192.0.2.10", "[REDACTED]")`,
		`b = BulbDevice("bf00000000", "192.0.2.11", '` + deviceCanary + `', version=3.3)`: `b = BulbDevice("bf00000000", "192.0.2.11", '[REDACTED]', version=3.3)`,
		`o = OutletDevice("id1", "192.0.2.12", "` + deviceCanary + `")`:                   `o = OutletDevice("id1", "192.0.2.12", "[REDACTED]")`,
	}
	for in, want := range cases {
		got := Redact(in, DefaultRedactors())
		if strings.Contains(got, deviceCanary) {
			t.Errorf("canary survived: %q", got)
		}
		if got != want {
			t.Errorf("Redact(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

// What the device pattern deliberately does not cover: it is narrow on purpose.
func TestDeviceLiteralScopeIsNarrow(t *testing.T) {
	unchanged := []string{
		// the key is a variable, not a literal: nothing to mask
		`d = Device(dev_id, addr, local_key)`,
		// a literal under 8 characters is left alone to avoid masking ordinary words
		`d = Device("id", "192.0.2.10", "short")`,
		// other constructors are not part of the pattern
		`x = Connect("bf0000000000000000abcd", "192.0.2.10", "` + deviceCanary + `")`,
	}
	for _, in := range unchanged {
		if got := Redact(in, DefaultRedactors()); got != in {
			t.Errorf("changed %q to %q", in, got)
		}
	}
	// A named argument is not matched by the device pattern; it is masked only by
	// the generic key-name rule, because the name ends in "_key".
	named := `d = Device(dev_id="bf0000", address="192.0.2.10", local_key="` + deviceCanary + `")`
	if got := Redact(named, DefaultRedactors()); strings.Contains(got, deviceCanary) {
		t.Errorf("named local_key leaked: %q", got)
	}
}
