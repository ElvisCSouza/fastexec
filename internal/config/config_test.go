package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FASTEXEC_CONFIG", p)
	return dir
}

func clearEnv(t *testing.T) {
	for _, k := range []string{"FASTEXEC_CONFIG", "FASTEXEC_DEFAULT_HOST", "FASTEXEC_DATA_DIR", "FASTEXEC_TELEMETRY"} {
		t.Setenv(k, "")
	}
	t.Setenv("FASTEXEC_HOME", t.TempDir())
}

func TestNoFile(t *testing.T) {
	clearEnv(t)
	c := Load()
	if c.Err() != nil || c.Path() != "" {
		t.Fatalf("err=%v path=%q", c.Err(), c.Path())
	}
	if c.Default() != "local" || !c.TelemetryOn() || c.Data() != Home() {
		t.Fatalf("default=%s telemetry=%v data=%s", c.Default(), c.TelemetryOn(), c.Data())
	}
	if got := c.HostChoices(true); got != "'local' (default)" {
		t.Fatalf("HostChoices = %q", got)
	}
}

func TestSampleJSONMatchesSample(t *testing.T) {
	clearEnv(t)
	writeConfig(t, SampleJSON)
	c := Load()
	if c.Err() != nil {
		t.Fatal(c.Err())
	}
	s := Sample()
	if !reflect.DeepEqual(c.Hosts, s.Hosts) || c.DefaultHost != s.DefaultHost || c.TelemetryOn() != s.TelemetryOn() {
		t.Fatalf("SampleJSON and Sample() differ:\n%+v\n%+v", c, s)
	}
	want := "'server' (default; cwd ~/workspace), 'local', 'router'"
	if got := c.HostChoices(false); got != want {
		t.Fatalf("HostChoices = %q, want %q", got, want)
	}
	want = "'server' (default; cwd ~/workspace, never prefix 'cd ~/workspace &&'), 'local', 'router'"
	if got := c.HostChoices(true); got != want {
		t.Fatalf("HostChoices(cd) = %q, want %q", got, want)
	}
}

func TestRelativeDirsAndEnv(t *testing.T) {
	clearEnv(t)
	dir := writeConfig(t, `{"data_dir": "data", "handoff_dir": "~/ho", "telemetry": false}`)
	c := Load()
	if c.Data() != filepath.Join(dir, "data") {
		t.Fatalf("data = %s", c.Data())
	}
	home, _ := os.UserHomeDir()
	if c.HandoffDir != filepath.Join(home, "ho") {
		t.Fatalf("handoff = %s", c.HandoffDir)
	}
	if c.TelemetryOn() {
		t.Fatal("telemetry should be off")
	}
	t.Setenv("FASTEXEC_TELEMETRY", "1")
	t.Setenv("FASTEXEC_DATA_DIR", filepath.Join(dir, "other"))
	c = Load()
	if !c.TelemetryOn() || c.Data() != filepath.Join(dir, "other") {
		t.Fatalf("env override: telemetry=%v data=%s", c.TelemetryOn(), c.Data())
	}
}

func TestInvalidFallsBackToLocal(t *testing.T) {
	cases := map[string]string{
		"unknown field":   `{"hostz": {}}`,
		"bad json":        `{`,
		"addr and ssh":    `{"hosts": {"a": {"addr": "1.2.3.4", "ssh": "a"}}}`,
		"neither":         `{"hosts": {"a": {}}}`,
		"duplicate alias": `{"hosts": {"a": {"addr": "1.2.3.4", "aliases": ["b"]}, "b": {"addr": "1.2.3.5"}}}`,
		"bad default":     `{"default_host": "nope"}`,
		"local with addr": `{"hosts": {"local": {"addr": "1.2.3.4"}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			writeConfig(t, body)
			c := Load()
			if c.Err() == nil {
				t.Fatal("expected an error")
			}
			if c.Default() != "local" || len(c.Remote(false)) != 0 {
				t.Fatalf("invalid config must fall back to local only: default=%s remote=%v", c.Default(), c.Remote(false))
			}
		})
	}
}

func TestLookupAliasesAndLocal(t *testing.T) {
	clearEnv(t)
	writeConfig(t, `{"hosts": {"local": {"aliases": ["my-pc"]}, "box": {"addr": "192.0.2.5", "aliases": ["Box2"]}}}`)
	c := Load()
	for in, want := range map[string]string{"my-pc": "local", "LOCALHOST": "local", "box2": "box", " BOX ": "box"} {
		if got, _, ok := c.Lookup(in); !ok || got != want {
			t.Errorf("Lookup(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	if got := strings.Join(c.Remote(false), ","); got != "box" {
		t.Errorf("Remote = %s", got)
	}
}

func TestSampleJSONIsValidJSON(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal([]byte(SampleJSON), &v); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayCwd(t *testing.T) {
	for in, want := range map[string]string{"/home/u/workspace": "~/workspace", "/home/u": "~", "/root/x": "~/x", "/srv/app": "/srv/app"} {
		if got := DisplayCwd(in); got != want {
			t.Errorf("DisplayCwd(%q) = %q want %q", in, got, want)
		}
	}
}
