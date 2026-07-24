/// Leader-owned scale policy for worker capacity.
///
/// Consecutive observations and cooldowns prevent every five-second telemetry
/// fluctuation from turning into worker churn across the whole Zone.
pub struct WorkerScalePolicy {
    min_workers: usize,
    max_workers: usize,
    scale_up_streak: u8,
    scale_down_streak: u8,
    last_change_at_unix_ms: Option<u64>,
}

#[derive(Clone, Copy, Debug, Default)]
pub struct WorkerScaleSignals {
    pub queue_lag: u64,
    pub cpu_utilization: f64,
    pub memory_utilization: f64,
    pub cpu_throttled_ratio: f64,
}

impl WorkerScalePolicy {
    pub fn new(min_workers: usize, max_workers: usize) -> Self {
        Self {
            min_workers: min_workers.min(max_workers),
            max_workers,
            scale_up_streak: 0,
            scale_down_streak: 0,
            last_change_at_unix_ms: None,
        }
    }

    pub fn evaluate(
        &mut self,
        current_workers: usize,
        signals: WorkerScaleSignals,
        now_ms: u64,
    ) -> usize {
        let current_workers = current_workers.min(self.max_workers);
        if current_workers < self.min_workers {
            self.record_change(now_ms);
            return self.min_workers;
        }

        let cpu_pressure = signals.cpu_utilization >= 0.95;
        let memory_pressure = signals.memory_utilization >= 0.90;
        let throttled = signals.cpu_throttled_ratio >= 0.20;
        if cpu_pressure || memory_pressure || throttled {
            self.reset_streaks();
            return current_workers;
        }

        let has_scale_up_headroom = signals.queue_lag > 100
            && signals.cpu_utilization < 0.80
            && signals.memory_utilization < 0.80
            && signals.cpu_throttled_ratio < 0.10;
        if has_scale_up_headroom {
            self.scale_down_streak = 0;
            self.scale_up_streak = self.scale_up_streak.saturating_add(1);
            if self.scale_up_streak >= 2 && self.cooldown_elapsed(now_ms, 15_000) {
                self.scale_up_streak = 0;
                let target = current_workers.saturating_add(2).min(self.max_workers);
                if target != current_workers {
                    self.record_change(now_ms);
                }
                return target;
            }
            return current_workers;
        }

        let is_calm = signals.queue_lag == 0
            && signals.cpu_utilization < 0.60
            && signals.memory_utilization < 0.65;
        if is_calm {
            self.scale_up_streak = 0;
            self.scale_down_streak = self.scale_down_streak.saturating_add(1);
            if self.scale_down_streak >= 6 && self.cooldown_elapsed(now_ms, 30_000) {
                self.scale_down_streak = 0;
                let target = current_workers.saturating_sub(1).max(self.min_workers);
                if target != current_workers {
                    self.record_change(now_ms);
                }
                return target;
            }
            return current_workers;
        }

        self.reset_streaks();
        current_workers
    }

    /// Drop confirmation streaks when telemetry is stale; a fresh window must
    /// prove the condition again instead of inheriting pre-outage samples.
    pub fn hold_on_stale_observation(&mut self) {
        self.reset_streaks();
    }

    fn cooldown_elapsed(&self, now_ms: u64, cooldown_ms: u64) -> bool {
        self.last_change_at_unix_ms
            .is_none_or(|last_change| now_ms.saturating_sub(last_change) >= cooldown_ms)
    }

    fn record_change(&mut self, now_ms: u64) {
        self.last_change_at_unix_ms = Some(now_ms);
    }

    fn reset_streaks(&mut self) {
        self.scale_up_streak = 0;
        self.scale_down_streak = 0;
    }
}

#[cfg(test)]
mod tests {
    use super::{WorkerScalePolicy, WorkerScaleSignals};

    fn signals(queue_lag: u64, cpu: f64, memory: f64, throttled: f64) -> WorkerScaleSignals {
        WorkerScaleSignals {
            queue_lag,
            cpu_utilization: cpu,
            memory_utilization: memory,
            cpu_throttled_ratio: throttled,
        }
    }

    #[test]
    fn resource_pressure_freezes_scale_up() {
        let mut policy = WorkerScalePolicy::new(1, 10);
        assert_eq!(policy.evaluate(3, signals(500, 0.96, 0.2, 0.0), 0), 3);
        assert_eq!(policy.evaluate(3, signals(500, 0.2, 0.91, 0.0), 5_000), 3);
        assert_eq!(policy.evaluate(3, signals(500, 0.2, 0.2, 0.21), 10_000), 3);
    }

    #[test]
    fn lag_requires_two_observations_and_respects_cooldown() {
        let mut policy = WorkerScalePolicy::new(1, 10);
        assert_eq!(policy.evaluate(3, signals(101, 0.5, 0.5, 0.01), 0), 3);
        assert_eq!(policy.evaluate(3, signals(101, 0.5, 0.5, 0.01), 5_000), 5);
        assert_eq!(policy.evaluate(5, signals(101, 0.5, 0.5, 0.01), 10_000), 5);
        assert_eq!(policy.evaluate(5, signals(101, 0.5, 0.5, 0.01), 20_000), 7);
    }

    #[test]
    fn empty_queue_scales_down_one_slot_after_six_calm_samples() {
        let mut policy = WorkerScalePolicy::new(2, 10);
        for sample in 0..5 {
            assert_eq!(
                policy.evaluate(8, signals(0, 0.2, 0.3, 0.0), sample * 5_000),
                8
            );
        }
        assert_eq!(policy.evaluate(8, signals(0, 0.2, 0.3, 0.0), 25_000), 7);
        assert_eq!(policy.evaluate(7, signals(0, 0.7, 0.3, 0.0), 30_000), 7);
    }
}
