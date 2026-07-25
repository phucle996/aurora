pub struct ServiceSignal<'a> {
    pub enabled: bool,
    pub status: &'a str,
    pub capacity_percent: usize,
}

pub struct ZoneSignals<'a> {
    pub queue_lag: i64,
    pub pending_jobs: i64,
    pub cpu_ratio: f64,
    pub ram_ratio: f64,
    pub mail: ServiceSignal<'a>,
    pub storage: ServiceSignal<'a>,
    pub current_status: &'a str,
}

/// Pure policy for the SRE-visible active/draining lifecycle.
pub struct ZoneDrainPolicy;

impl ZoneDrainPolicy {
    pub fn evaluate(input: ZoneSignals<'_>) -> String {
        if !matches!(input.current_status, "active" | "draining") {
            return input.current_status.to_string();
        }

        // Invalid measurements fail safe by retaining the current lifecycle;
        // the transport validator should normally reject them first.
        if input.queue_lag < 0
            || input.pending_jobs < 0
            || !input.cpu_ratio.is_finite()
            || !input.ram_ratio.is_finite()
            || !(0.0..=1.0).contains(&input.cpu_ratio)
            || !(0.0..=1.0).contains(&input.ram_ratio)
            || input.mail.capacity_percent > 100
            || input.storage.capacity_percent > 100
        {
            return input.current_status.to_string();
        }

        let service_failing = |service: &ServiceSignal<'_>| {
            service.enabled
                && (matches!(service.status, "down" | "error") || service.capacity_percent < 10)
        };
        if service_failing(&input.mail) || service_failing(&input.storage) {
            return "draining".to_string();
        }

        let overloaded = input.queue_lag > 5_000
            || input.pending_jobs > 500
            || input.cpu_ratio > 0.90
            || input.ram_ratio > 0.90;
        if input.current_status == "active" && overloaded {
            return "draining".to_string();
        }

        let recovered = input.queue_lag < 4_000
            && input.pending_jobs < 400
            && input.cpu_ratio < 0.85
            && input.ram_ratio < 0.85;
        let service_healthy = |service: &ServiceSignal<'_>| {
            !service.enabled || (service.status == "healthy" && service.capacity_percent >= 50)
        };
        if input.current_status == "draining"
            && recovered
            && service_healthy(&input.mail)
            && service_healthy(&input.storage)
        {
            return "active".to_string();
        }

        input.current_status.to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::{ServiceSignal, ZoneDrainPolicy, ZoneSignals};

    fn healthy(status: &'static str) -> ZoneSignals<'static> {
        ZoneSignals {
            queue_lag: 0,
            pending_jobs: 0,
            cpu_ratio: 0.2,
            ram_ratio: 0.3,
            mail: ServiceSignal {
                enabled: true,
                status: "healthy",
                capacity_percent: 100,
            },
            storage: ServiceSignal {
                enabled: true,
                status: "healthy",
                capacity_percent: 100,
            },
            current_status: status,
        }
    }

    #[test]
    fn overload_drains_and_hysteresis_recovers() {
        let mut overloaded = healthy("active");
        overloaded.queue_lag = 5_001;
        assert_eq!(ZoneDrainPolicy::evaluate(overloaded), "draining");
        assert_eq!(ZoneDrainPolicy::evaluate(healthy("draining")), "active");
    }

    #[test]
    fn invalid_metric_cannot_change_lifecycle() {
        let mut invalid = healthy("active");
        invalid.cpu_ratio = f64::NAN;
        assert_eq!(ZoneDrainPolicy::evaluate(invalid), "active");
    }

    #[test]
    fn disabled_service_does_not_block_recovery() {
        let mut input = healthy("draining");
        input.mail.enabled = false;
        input.mail.status = "down";
        input.mail.capacity_percent = 0;
        assert_eq!(ZoneDrainPolicy::evaluate(input), "active");
    }
}
