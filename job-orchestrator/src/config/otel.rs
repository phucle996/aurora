use super::environment::Environment;
use super::tls::TlsClientConfig;

#[derive(Clone)]
pub struct OtlpExporterConfig {
    pub endpoint: String,
    pub tls: Option<TlsClientConfig>,
    pub tls_server_name: Option<String>,
}

/// OTel is diagnostic-only. Its enablement and endpoint are explicit, while
/// sampling, queue and export timing remain bounded tuning defaults.
#[derive(Clone)]
pub struct OtelConfig {
    pub exporter: Option<OtlpExporterConfig>,
    pub trace_sample_ratio: f64,
    pub metric_export_interval_secs: u64,
    pub export_timeout_secs: u64,
    pub batch_max_queue_size: usize,
    pub batch_max_export_size: usize,
    pub batch_schedule_delay_ms: u64,
    pub batch_export_timeout_ms: u64,
}

impl OtelConfig {
    pub(crate) fn load(environment: &Environment) -> Result<Self, String> {
        let enabled = environment.required_bool("OTEL_ENABLED")?;
        let exporter = if enabled {
            let endpoint = environment.required("OTEL_EXPORTER_OTLP_ENDPOINT")?;
            let uses_tls = if endpoint.starts_with("https://") {
                true
            } else if endpoint.starts_with("http://") {
                false
            } else {
                return Err("OTEL_EXPORTER_OTLP_ENDPOINT must use http:// or https://".to_owned());
            };
            let tls_server_name = environment.optional("OTEL_TLS_SERVER_NAME");
            let tls = if uses_tls {
                Some(TlsClientConfig::load(
                    environment,
                    "OTEL_TLS",
                    "OTEL_EXPORTER_OTLP_CERTIFICATE",
                    "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE",
                    "OTEL_EXPORTER_OTLP_CLIENT_KEY",
                )?)
            } else {
                TlsClientConfig::ensure_absent(
                    environment,
                    "OTEL_TLS",
                    "OTEL_EXPORTER_OTLP_CERTIFICATE",
                    "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE",
                    "OTEL_EXPORTER_OTLP_CLIENT_KEY",
                    "OTEL_EXPORTER_OTLP_ENDPOINT uses http://",
                )?;
                if tls_server_name.is_some() {
                    return Err(
                        "OTEL_TLS_SERVER_NAME requires an https:// OTLP endpoint".to_owned()
                    );
                }
                None
            };
            Some(OtlpExporterConfig {
                endpoint,
                tls,
                tls_server_name,
            })
        } else {
            environment.reject_present(
                &["OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_TLS_SERVER_NAME"],
                "OTEL_ENABLED=false",
            )?;
            TlsClientConfig::ensure_absent(
                environment,
                "OTEL_TLS",
                "OTEL_EXPORTER_OTLP_CERTIFICATE",
                "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE",
                "OTEL_EXPORTER_OTLP_CLIENT_KEY",
                "OTEL_ENABLED=false",
            )?;
            None
        };

        let metric_export_interval_secs =
            environment.bounded("OTEL_METRIC_EXPORT_INTERVAL_SECS", 15_u64, 5, 300)?;
        let export_timeout_secs = environment.bounded("OTEL_EXPORT_TIMEOUT_SECS", 10_u64, 1, 30)?;
        if export_timeout_secs > metric_export_interval_secs {
            return Err(
                "OTEL_EXPORT_TIMEOUT_SECS cannot exceed OTEL_METRIC_EXPORT_INTERVAL_SECS"
                    .to_owned(),
            );
        }
        let batch_max_queue_size =
            environment.bounded("OTEL_BSP_MAX_QUEUE_SIZE", 8_192_usize, 128, 1_048_576)?;
        let batch_max_export_size =
            environment.bounded("OTEL_BSP_MAX_EXPORT_BATCH_SIZE", 512_usize, 1, 65_536)?;
        if batch_max_export_size > batch_max_queue_size {
            return Err(
                "OTEL_BSP_MAX_EXPORT_BATCH_SIZE cannot exceed OTEL_BSP_MAX_QUEUE_SIZE".to_owned(),
            );
        }
        let batch_export_timeout_ms =
            environment.bounded("OTEL_BSP_EXPORT_TIMEOUT", 5_000_u64, 100, 30_000)?;
        if batch_export_timeout_ms > export_timeout_secs.saturating_mul(1_000) {
            return Err(
                "OTEL_BSP_EXPORT_TIMEOUT cannot exceed OTEL_EXPORT_TIMEOUT_SECS".to_owned(),
            );
        }

        Ok(Self {
            exporter,
            trace_sample_ratio: environment.bounded_f64(
                "OTEL_TRACE_SAMPLE_RATIO",
                1.0,
                0.0,
                1.0,
            )?,
            metric_export_interval_secs,
            export_timeout_secs,
            batch_max_queue_size,
            batch_max_export_size,
            batch_schedule_delay_ms: environment.bounded(
                "OTEL_BSP_SCHEDULE_DELAY",
                1_000_u64,
                10,
                60_000,
            )?,
            batch_export_timeout_ms,
        })
    }
}
