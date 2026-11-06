package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestStartupDiagnostics(t *testing.T) {
	t.Parallel()
	const configuredService = "configured-service"
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	for _, tc := range []struct {
		name       string
		service    string
		attributes string
		invalidLog bool
		want       string
		wantWarn   bool
	}{
		{name: "default", want: "trip-service", wantWarn: true},
		{name: "resource name", attributes: "service.name=resource-service", want: "resource-service", wantWarn: true},
		{name: "explicit name", service: configuredService, attributes: "service.name=resource-service", want: configuredService, wantWarn: true},
		{name: "invalid config", service: configuredService, invalidLog: true, want: configuredService},
		{name: "invalid resource", service: configuredService, attributes: "private-test-credential", want: configuredService},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestStartupProcess$")
			cmd.Env = []string{
				"GOCOVERDIR=" + t.TempDir(),
				"TRIPGO_STARTUP_TEST=1", "HTTP_ADDR=127.0.0.1:0", "LOG_LEVEL=info", "SHUTDOWN_TIMEOUT=1s",
				"DATABASE_URL=postgres://user:test@127.0.0.1:invalid/database",
				"DATABASE_MAX_CONNS=1", "DATABASE_MIN_CONNS=0", "DATABASE_MAX_CONN_LIFETIME=1h",
				"DATABASE_CONNECT_TIMEOUT=1s", "DATABASE_QUERY_TIMEOUT=1s",
				"OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:1", "OTEL_TRACES_SAMPLER=invalid-sampler",
				"OTEL_SERVICE_NAME=" + tc.service, "OTEL_RESOURCE_ATTRIBUTES=" + tc.attributes,
			}
			if tc.invalidLog {
				cmd.Env = append(cmd.Env, "LOG_LEVEL=invalid")
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			var exitErr *exec.ExitError
			if err := cmd.Run(); !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("startup exit = %v, want 1", err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", stderr.String())
			}
			if strings.Contains(stdout.String(), "private-test-credential") {
				t.Fatal("startup log contains private configuration")
			}
			var warnings, failures int
			for _, line := range bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte{'\n'}) {
				var entry struct {
					Service string `json:"service"`
					Level   string `json:"level"`
					Message string `json:"msg"`
					Error   string `json:"error"`
				}
				if err := json.Unmarshal(line, &entry); err != nil {
					t.Fatalf("invalid JSON diagnostic: %v", err)
				}
				if entry.Service != tc.want {
					t.Errorf("diagnostic service = %q, want %q", entry.Service, tc.want)
				}
				switch entry.Level {
				case "WARN":
					if entry.Message == "telemetry shutdown failed" {
						continue
					}
					if entry.Message != "telemetry error" || !strings.Contains(entry.Error, "invalid-sampler") {
						t.Errorf("unexpected warning: %+v", entry)
					}
					warnings++
				case "ERROR":
					if entry.Message != "service stopped with error" || entry.Error == "" {
						t.Errorf("unexpected failure: %+v", entry)
					}
					failures++
				}
			}
			if failures != 1 || (warnings > 0) != tc.wantWarn {
				t.Fatalf("diagnostics: failures=%d, warnings=%d", failures, warnings)
			}
		})
	}
}

func TestStartupProcess(_ *testing.T) {
	if os.Getenv("TRIPGO_STARTUP_TEST") != "1" {
		return
	}
	main()
}
