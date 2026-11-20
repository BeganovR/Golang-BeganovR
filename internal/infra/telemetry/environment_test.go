package telemetry

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestInvalidEnvironmentDoesNotLeak(t *testing.T) {
	t.Parallel()
	const (
		secret  = "private-test-credential"
		headers = "HEADERS"
	)
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	for _, protocol := range []string{"http/protobuf", "grpc"} {
		for _, prefix := range []string{"OTEL_EXPORTER_OTLP", "OTEL_EXPORTER_OTLP_TRACES", "OTEL_EXPORTER_OTLP_METRICS"} {
			for _, tc := range []struct {
				name  string
				value string
			}{
				{name: "ENDPOINT", value: "http://user:" + secret + "@localhost:invalid"},
				{name: headers, value: "authorization=" + secret + "%invalid"},
				{name: headers, value: secret},
				{name: headers, value: secret + " invalid=value"},
			} {
				t.Run(protocol+"/"+prefix+"_"+tc.name+"/"+tc.value, func(t *testing.T) {
					t.Parallel()
					name := prefix + "_" + tc.name
					cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestEnvironmentProcess$")
					for _, value := range os.Environ() {
						if !strings.HasPrefix(value, "OTEL_") && !strings.HasPrefix(value, "TRIPGO_TEST_OTLP_ENV=") {
							cmd.Env = append(cmd.Env, value)
						}
					}
					cmd.Env = append(cmd.Env, "TRIPGO_TEST_OTLP_ENV=1", "OTEL_EXPORTER_OTLP_PROTOCOL="+protocol, name+"="+tc.value)
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("run environment check: %v", err)
					}
					if strings.Contains(string(output), secret) {
						t.Fatal("telemetry diagnostics contain credential")
					}
					if !strings.Contains(string(output), "invalid "+name) {
						t.Fatalf("missing safe configuration error: %s", output)
					}
				})
			}
		}
	}
}

func TestEnvironmentProcess(t *testing.T) {
	if os.Getenv("TRIPGO_TEST_OTLP_ENV") != "1" {
		return
	}
	providers, err := New(context.Background(), "test")
	if providers != nil || err == nil {
		t.Fatal("invalid environment was accepted")
	}
	fmt.Println(err)
}

func TestValidEnvironment(t *testing.T) {
	for _, endpoint := range []string{"", "http://localhost:4318", "https://collector.example/otlp", "unix:///tmp/otel.sock"} {
		for _, headers := range []string{"", "Authorization=Bearer%20test", "a=1,b=two%2Cthree", " x-api-key =value=with=equals", "!#$%&'*+-.^_`|~=value", "empty="} {
			t.Run(endpoint+"/"+headers, func(t *testing.T) {
				for _, prefix := range []string{"OTEL_EXPORTER_OTLP", "OTEL_EXPORTER_OTLP_TRACES", "OTEL_EXPORTER_OTLP_METRICS"} {
					t.Setenv(prefix+"_ENDPOINT", endpoint)
					t.Setenv(prefix+"_HEADERS", headers)
				}
				if err := validateEnvironment(); err != nil {
					t.Fatalf("valid environment rejected: %v", err)
				}
			})
		}
	}
}
