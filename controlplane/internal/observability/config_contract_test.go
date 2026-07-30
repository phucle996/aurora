package observability

import (
	"bytes"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTelemetryBackendCorrelationConfiguration(t *testing.T) {
	collector, err := os.ReadFile("../../dev/otel/otel-collector.yml")
	if err != nil {
		t.Fatalf("read collector config: %v", err)
	}
	var collectorDocument any
	if err := yaml.Unmarshal(collector, &collectorDocument); err != nil {
		t.Fatalf("parse collector config: %v", err)
	}
	for _, required := range [][]byte{
		[]byte(`resource.attributes["service.name"]`),
		[]byte(`resource.attributes["service.instance.id"]`),
		[]byte(`resource.attributes["aurora.component"]`),
	} {
		if !bytes.Contains(collector, required) {
			t.Fatalf("collector config is missing %s", required)
		}
	}

	datasources, err := os.ReadFile("../../dev/grafana/provisioning/datasources/datasources.yml")
	if err != nil {
		t.Fatalf("read Grafana datasource config: %v", err)
	}
	var datasourceDocument any
	if err := yaml.Unmarshal(datasources, &datasourceDocument); err != nil {
		t.Fatalf("parse Grafana datasource config: %v", err)
	}
	if bytes.Count(datasources, []byte("exemplarTraceIdDestinations:")) != 2 {
		t.Fatalf("expected exemplar mappings for both metric datasources")
	}
	if !bytes.Contains(datasources, []byte("datasourceUid: jaeger")) ||
		!bytes.Contains(datasources, []byte("derivedFields:")) {
		t.Fatalf("Grafana log/metric trace drill-down is incomplete")
	}

	environment, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatalf("read environment example: %v", err)
	}
	if !bytes.Contains(environment, []byte("OTEL_METRICS_EXEMPLAR_FILTER=trace_based")) {
		t.Fatalf("trace-based metric exemplars must stay enabled by default")
	}
}
