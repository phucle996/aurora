use std::sync::atomic::{AtomicUsize, Ordering};

use crate::observability::logger::Logger;
use crate::observability::metrics::NodeRuntimeSampler;

const PREFETCH_PER_READY_WORKER: usize = 2;
const OPEN_THRESHOLD: f64 = 0.90;
const CLOSE_THRESHOLD: f64 = 0.60;
const MAX_PACING_DELAY_MS: f64 = 500.0;

pub trait ExecutionCapacity: Send + Sync {
    fn ready_workers(&self) -> usize;
}

pub struct IntakeAdmission {
    is_circuit_open: bool,
}

#[derive(Clone, Copy, Debug)]
pub struct AdmissionResult {
    pub is_open: bool,
    pub pacing_delay_ms: u64,
    pub budget: usize,
}

pub fn release_admitted_job(admitted_jobs: &AtomicUsize, stage: &'static str) {
    if admitted_jobs
        .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |current| {
            current.checked_sub(1)
        })
        .is_err()
    {
        // A wrapping counter would permanently defeat intake backpressure.
        Logger::sys_error(
            "job.intake.admission",
            &format!("Admitted job counter underflow prevented at {stage}"),
            "JOB_ADMISSION_COUNTER_UNDERFLOW",
        );
    }
}

impl IntakeAdmission {
    pub fn new() -> Self {
        Self {
            is_circuit_open: false,
        }
    }

    pub fn evaluate(
        &mut self,
        admitted_jobs: usize,
        ready_workers: usize,
        queue_capacity: usize,
    ) -> AdmissionResult {
        self.evaluate_with_load(
            admitted_jobs,
            ready_workers,
            queue_capacity,
            NodeRuntimeSampler::cpu_usage(),
            NodeRuntimeSampler::ram_usage(),
        )
    }

    fn evaluate_with_load(
        &mut self,
        admitted_jobs: usize,
        ready_workers: usize,
        queue_capacity: usize,
        cpu_usage: f64,
        ram_usage: f64,
    ) -> AdmissionResult {
        // Admitted includes queued + executing. Tying the budget to Ready workers
        // prevents a max=100/target=5 pod from prefetching as if all 100 existed.
        let budget = ready_workers
            .saturating_mul(PREFETCH_PER_READY_WORKER)
            .min(ready_workers.saturating_add(queue_capacity))
            .max(1);
        let work_pressure = if ready_workers == 0 {
            1.0
        } else {
            admitted_jobs as f64 / budget as f64
        };
        let pressure = work_pressure.max(cpu_usage).max(ram_usage);

        let pacing_delay_ms = if pressure <= CLOSE_THRESHOLD {
            0
        } else {
            let normalized =
                ((pressure - CLOSE_THRESHOLD) / (OPEN_THRESHOLD - CLOSE_THRESHOLD)).clamp(0.0, 1.0);
            (MAX_PACING_DELAY_MS * normalized) as u64
        };

        if !self.is_circuit_open && pressure >= OPEN_THRESHOLD {
            self.is_circuit_open = true;
            Logger::sys_warn(
                "job.intake.admission",
                &format!(
                    "Intake circuit opened pressure={:.1}% admitted={admitted_jobs}/{budget} ready_workers={ready_workers} cpu={:.1}% ram={:.1}%",
                    pressure * 100.0,
                    cpu_usage * 100.0,
                    ram_usage * 100.0
                ),
                "JOB_INTAKE_ADMISSION_OPEN",
            );
        } else if self.is_circuit_open && pressure <= CLOSE_THRESHOLD {
            self.is_circuit_open = false;
            Logger::sys_info(
                "job.intake.admission",
                &format!(
                    "Intake circuit closed pressure={:.1}% admitted={admitted_jobs}/{budget} ready_workers={ready_workers}",
                    pressure * 100.0
                ),
            );
        }

        AdmissionResult {
            is_open: self.is_circuit_open,
            pacing_delay_ms,
            budget,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn budget_tracks_ready_workers_instead_of_configured_maximum() {
        let mut admission = IntakeAdmission::new();
        let result = admission.evaluate_with_load(9, 5, 100, 0.1, 0.1);
        assert_eq!(result.budget, 10);
        assert!(result.is_open);
    }

    #[test]
    fn circuit_uses_hysteresis() {
        let mut admission = IntakeAdmission::new();
        assert!(admission.evaluate_with_load(10, 5, 100, 0.1, 0.1).is_open);
        assert!(admission.evaluate_with_load(7, 5, 100, 0.1, 0.1).is_open);
        assert!(!admission.evaluate_with_load(2, 5, 100, 0.1, 0.1).is_open);
    }

    #[test]
    fn admitted_counter_never_wraps_below_zero() {
        let counter = AtomicUsize::new(0);
        release_admitted_job(&counter, "test");
        assert_eq!(counter.load(Ordering::Relaxed), 0);

        counter.store(2, Ordering::Relaxed);
        release_admitted_job(&counter, "test");
        assert_eq!(counter.load(Ordering::Relaxed), 1);
    }
}
