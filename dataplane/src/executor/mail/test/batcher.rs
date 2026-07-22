use super::*;

fn queued_mail(index: usize) -> QueuedMail {
    let (reply, _result) = oneshot::channel();
    QueuedMail {
        mail: PreparedMail {
            job_id: format!("00000000-0000-4000-8000-{index:012}"),
            recipient: format!("user-{index}@example.com"),
            subject: "Subject".to_string(),
            text_body: Some("Body".to_string()),
            html_body: None,
            estimated_bytes: 1024,
        },
        reply,
    }
}

fn config(max_items: usize, max_wait: Duration) -> BatcherConfig {
    BatcherConfig {
        max_items,
        max_wait,
        max_bytes: 1024 * 1024,
        enqueue_timeout: Duration::from_secs(1),
        flush_workers: 1,
    }
}

#[tokio::test]
async fn flushes_immediately_when_item_limit_is_reached() {
    let (command_tx, command_rx) = mpsc::channel(4);
    let (batch_tx, mut batch_rx) = mpsc::channel(4);
    let metrics = Arc::new(MailWorkloadMetrics::default());
    let collector = tokio::spawn(collect_batches(
        command_rx,
        batch_tx,
        metrics,
        config(2, Duration::from_secs(30)),
    ));

    command_tx
        .send(Command::Submit(queued_mail(1)))
        .await
        .unwrap();
    command_tx
        .send(Command::Submit(queued_mail(2)))
        .await
        .unwrap();

    // [COMMENT]: Không đợi timer dài; item thứ hai phải đóng batch ngay theo count cap.
    let batch = tokio::time::timeout(Duration::from_millis(100), batch_rx.recv())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(batch.len(), 2);

    let (shutdown_tx, _shutdown_rx) = oneshot::channel();
    command_tx
        .send(Command::Shutdown(shutdown_tx))
        .await
        .unwrap();
    assert!(collector.await.unwrap().is_some());
}

#[tokio::test]
async fn shutdown_flushes_a_partial_batch() {
    let (command_tx, command_rx) = mpsc::channel(4);
    let (batch_tx, mut batch_rx) = mpsc::channel(4);
    let metrics = Arc::new(MailWorkloadMetrics::default());
    let collector = tokio::spawn(collect_batches(
        command_rx,
        batch_tx,
        metrics,
        config(50, Duration::from_secs(30)),
    ));

    command_tx
        .send(Command::Submit(queued_mail(1)))
        .await
        .unwrap();
    let (shutdown_tx, _shutdown_rx) = oneshot::channel();
    command_tx
        .send(Command::Shutdown(shutdown_tx))
        .await
        .unwrap();

    let batch = batch_rx.recv().await.unwrap();
    assert_eq!(batch.len(), 1);
    assert!(collector.await.unwrap().is_some());
}

#[tokio::test]
async fn flush_deadline_starts_at_the_first_item() {
    let (command_tx, command_rx) = mpsc::channel(4);
    let (batch_tx, mut batch_rx) = mpsc::channel(4);
    let metrics = Arc::new(MailWorkloadMetrics::default());
    let collector = tokio::spawn(collect_batches(
        command_rx,
        batch_tx,
        metrics,
        config(50, Duration::from_millis(10)),
    ));

    command_tx
        .send(Command::Submit(queued_mail(1)))
        .await
        .unwrap();
    let batch = tokio::time::timeout(Duration::from_millis(250), batch_rx.recv())
        .await
        .expect("partial batch must flush on its first-item deadline")
        .unwrap();
    assert_eq!(batch.len(), 1);

    let (shutdown_tx, _shutdown_rx) = oneshot::channel();
    command_tx
        .send(Command::Shutdown(shutdown_tx))
        .await
        .unwrap();
    assert!(collector.await.unwrap().is_some());
}
