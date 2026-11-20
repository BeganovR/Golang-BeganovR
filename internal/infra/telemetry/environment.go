package telemetry

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

func validateEnvironment() error {
	for _, prefix := range []string{"OTEL_EXPORTER_OTLP", "OTEL_EXPORTER_OTLP_TRACES", "OTEL_EXPORTER_OTLP_METRICS"} {
		name := prefix + "_ENDPOINT"
		if _, err := url.Parse(strings.TrimSpace(os.Getenv(name))); err != nil {
			return fmt.Errorf("invalid %s", name)
		}
		name = prefix + "_HEADERS"
		if value := strings.TrimSpace(os.Getenv(name)); value != "" && !validHeaders(value) {
			return fmt.Errorf("invalid %s", name)
		}
	}
	return nil
}

func validHeaders(value string) bool {
	for _, pair := range strings.Split(value, ",") {
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			return false
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.IndexFunc(name, func(c rune) bool {
			return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!#$%&'*+-.^_`|~", c)
		}) >= 0 {
			return false
		}
		if _, err := url.PathUnescape(value); err != nil {
			return false
		}
	}
	return true
}
