package observability

import (
	"bytes"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTelemetryBackendCorrelationConfiguration(t *testing.T) {
	collector, err := os.ReadFile("../../../dev/central/otel/otel-collector.yml")
	if err != nil {
		t.Fatalf("read collector config: %v", err)
	}
	var collectorDocument any
	if err := yaml.Unmarshal(collector, &collectorDocument); err != nil {
		t.Fatalf("parse collector config: %v", err)
	}
	for _, required := range [][]byte{
		[]byte(`"VL-Stream-Fields": "service_name"`),
		[]byte(`"VL-Ignore-Fields": "scope.name,scope.version"`),
	} {
		if !bytes.Contains(collector, required) {
			t.Fatalf("collector config is missing %s", required)
		}
	}
	if bytes.Contains(collector, []byte(`VL-Stream-Fields": "container_name`)) ||
		bytes.Contains(collector, []byte(`Substring(attributes["container_id"]`)) {
		t.Fatal("container identity must not be used as a VictoriaLogs stream dimension or fallback")
	}

	zoneCollector, err := os.ReadFile("../../../dev/zone/otel/otel-collector.yml")
	if err != nil {
		t.Fatalf("read zone collector config: %v", err)
	}
	var zoneCollectorDocument any
	if err := yaml.Unmarshal(zoneCollector, &zoneCollectorDocument); err != nil {
		t.Fatalf("parse zone collector config: %v", err)
	}
	if !bytes.Contains(zoneCollector, []byte(`"VL-Stream-Fields": "service_name"`)) {
		t.Fatal("zone collector must keep the same bounded service_name stream contract")
	}
	if !bytes.Contains(zoneCollector, []byte(`"VL-Ignore-Fields": "scope.name,scope.version"`)) {
		t.Fatal("zone collector must drop default OTLP scope noise before storage")
	}
	if bytes.Contains(zoneCollector, []byte(`VL-Stream-Fields": "service_name,zone_id`)) {
		t.Fatal("zone or customer identity must not be a VictoriaLogs stream dimension")
	}

	datasources, err := os.ReadFile("../../../dev/central/grafana/provisioning/datasources/datasources.yml")
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
