use super::jmap::JmapClient;
use super::model::{MailSubmitError, MailSubmitResult, PreparedMail};
use crate::config::Config;
use crate::executor::mail::supervisor::MailWorkloadMetrics;
use crate::observability::logger::Logger;
use std::mem;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::{mpsc, oneshot, Mutex};
use tokio::time::Instant;

struct QueuedMail {
    mail: PreparedMail,
    reply: oneshot::Sender<MailSubmitResult>,
}

enum Command {
    Submit(QueuedMail),
    Shutdown(oneshot::Sender<()>),
}

#[derive(Clone, Copy)]
struct BatcherConfig {
    max_items: usize,
    max_wait: Duration,
    max_bytes: usize,
    enqueue_timeout: Duration,
    flush_workers: usize,
}

pub struct MailBatcherHandle {
    tx: mpsc::Sender<Command>,
    closed: AtomicBool,
    metrics: Arc<MailWorkloadMetrics>,
    enqueue_timeout: Duration,
}

impl MailBatcherHandle {
    pub fn start(
        config: &Config,
        client: Arc<JmapClient>,
        metrics: Arc<MailWorkloadMetrics>,
    ) -> Arc<Self> {
        let batcher_config = BatcherConfig {
            max_items: config.mail_batch_max_items.max(1),
            max_wait: Duration::from_millis(config.mail_batch_max_wait_ms.max(1)),
            max_bytes: config.mail_batch_max_bytes.max(1024),
            enqueue_timeout: Duration::from_millis(config.mail_batch_enqueue_timeout_ms.max(1)),
            flush_workers: config.mail_jmap_max_inflight_per_pod.max(1),
        };
        let (tx, rx) = mpsc::channel(config.mail_batch_queue_capacity.max(1));
        let handle = Arc::new(Self {
            tx,
            closed: AtomicBool::new(false),
            metrics: metrics.clone(),
            enqueue_timeout: batcher_config.enqueue_timeout,
        });
        tokio::spawn(run_supervisor(rx, client, metrics, batcher_config));
        handle
    }

    pub async fn submit(&self, mail: PreparedMail) -> MailSubmitResult {
        if self.closed.load(Ordering::Acquire) {
            return Err(MailSubmitError::new(
                "MAIL_BATCHER_SHUTTING_DOWN",
                "mail batcher is shutting down",
                true,
            ));
        }
        let (reply_tx, reply_rx) = oneshot::channel();
        self.metrics.pending_items.fetch_add(1, Ordering::Relaxed);
        let enqueue = self.tx.send(Command::Submit(QueuedMail {
            mail,
            reply: reply_tx,
        }));
        match tokio::time::timeout(self.enqueue_timeout, enqueue).await {
            Ok(Ok(())) => {}
            Ok(Err(_)) => {
                self.metrics.pending_items.fetch_sub(1, Ordering::Relaxed);
                return Err(MailSubmitError::new(
                    "MAIL_BATCHER_CLOSED",
                    "mail batcher channel closed",
                    true,
                ));
            }
            Err(_) => {
                self.metrics.pending_items.fetch_sub(1, Ordering::Relaxed);
                return Err(MailSubmitError::new(
                    "MAIL_BATCHER_BACKPRESSURE",
                    "mail batch queue is full",
                    true,
                ));
            }
        }
        reply_rx.await.unwrap_or_else(|_| {
            Err(MailSubmitError::new(
                "MAIL_BATCHER_RESULT_DROPPED",
                "mail batch worker stopped before returning a result",
                true,
            ))
        })
    }

    pub async fn shutdown(&self) {
        if self.closed.swap(true, Ordering::AcqRel) {
            return;
        }
        let (done_tx, done_rx) = oneshot::channel();
        if self.tx.send(Command::Shutdown(done_tx)).await.is_ok() {
            let _ = done_rx.await;
        }
    }
}

async fn run_supervisor(
    rx: mpsc::Receiver<Command>,
    client: Arc<JmapClient>,
    metrics: Arc<MailWorkloadMetrics>,
    config: BatcherConfig,
) {
    let (batch_tx, batch_rx) = mpsc::channel::<Vec<QueuedMail>>(config.flush_workers * 2);
    let shared_rx = Arc::new(Mutex::new(batch_rx));
    let mut workers = Vec::with_capacity(config.flush_workers);
    for _ in 0..config.flush_workers {
        let receiver = shared_rx.clone();
        let client = client.clone();
        let metrics = metrics.clone();
        workers.push(tokio::spawn(async move {
            loop {
                let batch = {
                    let mut guard = receiver.lock().await;
                    guard.recv().await
                };
                let Some(batch) = batch else { break };
                flush_batch(client.clone(), metrics.clone(), batch).await;
            }
        }));
    }

    let shutdown_reply = collect_batches(rx, batch_tx, metrics.clone(), config).await;
    for worker in workers {
        let _ = worker.await;
    }
    if let Some(reply) = shutdown_reply {
        let _ = reply.send(());
    }
}

async fn collect_batches(
    mut rx: mpsc::Receiver<Command>,
    batch_tx: mpsc::Sender<Vec<QueuedMail>>,
    metrics: Arc<MailWorkloadMetrics>,
    config: BatcherConfig,
) -> Option<oneshot::Sender<()>> {
    let mut current = Vec::with_capacity(config.max_items);
    let mut current_bytes = 0usize;
    let mut flush_deadline = None;
    let far_future = Duration::from_secs(365 * 24 * 60 * 60);

    loop {
        let deadline = flush_deadline.unwrap_or_else(|| Instant::now() + far_future);
        tokio::select! {
            command = rx.recv() => {
                match command {
                    Some(Command::Submit(item)) => {
                        let would_exceed_bytes = !current.is_empty()
                            && current_bytes.saturating_add(item.mail.estimated_bytes) > config.max_bytes;
                        if would_exceed_bytes && !send_batch(&batch_tx, &mut current, &metrics).await {
                            fail_item(item, &metrics, "MAIL_BATCHER_FLUSH_CLOSED");
                            return None;
                        }
                        if would_exceed_bytes {
                            current_bytes = 0;
                            flush_deadline = None;
                        }
                        let starts_batch = current.is_empty();
                        current_bytes = current_bytes.saturating_add(item.mail.estimated_bytes);
                        current.push(item);
                        if starts_batch {
                            flush_deadline = Some(Instant::now() + config.max_wait);
                        }
                        if current.len() >= config.max_items || current_bytes >= config.max_bytes {
                            if !send_batch(&batch_tx, &mut current, &metrics).await {
                                return None;
                            }
                            current_bytes = 0;
                            flush_deadline = None;
                        }
                    }
                    Some(Command::Shutdown(reply)) => {
                        let _ = send_batch(&batch_tx, &mut current, &metrics).await;
                        drop(batch_tx);
                        return Some(reply);
                    }
                    None => {
                        let _ = send_batch(&batch_tx, &mut current, &metrics).await;
                        drop(batch_tx);
                        return None;
                    }
                }
            }
            _ = tokio::time::sleep_until(deadline), if !current.is_empty() => {
                if !send_batch(&batch_tx, &mut current, &metrics).await {
                    return None;
                }
                current_bytes = 0;
                flush_deadline = None;
            }
        }
    }
}

async fn send_batch(
    batch_tx: &mpsc::Sender<Vec<QueuedMail>>,
    current: &mut Vec<QueuedMail>,
    metrics: &MailWorkloadMetrics,
) -> bool {
    if current.is_empty() {
        return true;
    }
    let batch = mem::take(current);
    match batch_tx.send(batch).await {
        Ok(()) => true,
        Err(error) => {
            for item in error.0 {
                fail_item(item, metrics, "MAIL_BATCHER_FLUSH_CLOSED");
            }
            false
        }
    }
}

async fn flush_batch(
    client: Arc<JmapClient>,
    metrics: Arc<MailWorkloadMetrics>,
    batch: Vec<QueuedMail>,
) {
    metrics.in_flight_batches.fetch_add(1, Ordering::Relaxed);
    Logger::sys_info(
        "executor.mail.batch",
        &format!("Submitting JMAP mail batch with {} items", batch.len()),
    );
    let (mails, replies): (Vec<_>, Vec<_>) = batch
        .into_iter()
        .map(|item| (item.mail, item.reply))
        .unzip();
    let results = client.submit_batch(&mails).await;
    for (reply, result) in replies.into_iter().zip(results) {
        metrics.pending_items.fetch_sub(1, Ordering::Relaxed);
        if result.is_ok() {
            metrics.accepted_total.fetch_add(1, Ordering::Relaxed);
        } else {
            metrics.failed_total.fetch_add(1, Ordering::Relaxed);
        }
        let _ = reply.send(result);
    }
    metrics.in_flight_batches.fetch_sub(1, Ordering::Relaxed);
}

fn fail_item(item: QueuedMail, metrics: &MailWorkloadMetrics, code: &str) {
    metrics.pending_items.fetch_sub(1, Ordering::Relaxed);
    metrics.failed_total.fetch_add(1, Ordering::Relaxed);
    let _ = item.reply.send(Err(MailSubmitError::new(
        code,
        "mail batch flush path is unavailable",
        true,
    )));
}

#[cfg(test)]
#[path = "../test/batcher.rs"]
mod tests;
