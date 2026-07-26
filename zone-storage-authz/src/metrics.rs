use std::sync::atomic::{AtomicU64, Ordering};

#[derive(Default)]
pub struct Metrics {
    allowed: AtomicU64,
    denied: AtomicU64,
    dependency_failure: AtomicU64,
}

impl Metrics {
    pub fn allowed(&self) {
        self.allowed.fetch_add(1, Ordering::Relaxed);
    }
    pub fn denied(&self) {
        self.denied.fetch_add(1, Ordering::Relaxed);
    }
    pub fn dependency_failure(&self) {
        self.dependency_failure.fetch_add(1, Ordering::Relaxed);
    }
}
