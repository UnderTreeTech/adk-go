package jaeger

// Config holds the connection parameters needed to export traces to a Jaeger
// instance via OTLP.
//
// Jaeger v1.35+ natively supports OTLP ingestion, so no Jaeger-specific
// client library is needed — standard OpenTelemetry OTLP exporters are used
// under the hood.
//
// Host applications should embed or reference this struct directly (e.g. as a
// YAML field) rather than defining their own mirror type.
type Config struct {
	// Endpoint is the Jaeger OTLP collector endpoint.
	//
	// For gRPC (default): "localhost:4317"
	// For HTTP:           "http://localhost:4318/v1/traces"
	//
	// When empty it defaults to "localhost:4317" (gRPC).
	Endpoint string `yaml:"endpoint" json:"endpoint"`

	// Protocol selects the OTLP transport protocol. Supported values:
	//   - "grpc" (default): uses OTLP/gRPC exporter
	//   - "http": uses OTLP/HTTP exporter
	// When empty it defaults to "grpc".
	Protocol string `yaml:"protocol,omitempty" json:"protocol,omitempty"`

	// ServiceName is the OpenTelemetry service.name resource attribute.
	// Defaults to "adk-agent" when empty.
	ServiceName string `yaml:"serviceName,omitempty" json:"serviceName,omitempty"`

	// Environment is an optional deployment environment tag forwarded as a
	// resource attribute (e.g. "production", "staging").
	Environment string `yaml:"environment,omitempty" json:"environment,omitempty"`

	// Insecure disables TLS for the OTLP exporter. Set to true when
	// connecting to a local or non-TLS Jaeger instance. Defaults to true
	// for local development convenience.
	Insecure bool `yaml:"insecure,omitempty" json:"insecure,omitempty"`

	// Headers are optional additional HTTP/gRPC headers to send with each
	// export request (e.g. for authentication).
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
}

// IsEnabled reports whether the minimum required configuration (Endpoint) is
// present. Callers should gate Jaeger setup behind this check so that the
// plugin is silently disabled when no endpoint is provided.
func (c *Config) IsEnabled() bool {
	return c != nil && c.Endpoint != ""
}
