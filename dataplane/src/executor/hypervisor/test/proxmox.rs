use super::*;
use tokio::io::{AsyncReadExt, AsyncWriteExt};

// Test-only provider boundary: assert real HTTP paths and simulate a lost ACK
// by closing the socket after accepting DELETE without returning a response.
pub(crate) async fn mock_provider(
    responses: Vec<(String, Option<serde_json::Value>)>,
) -> (ProxmoxClient, tokio::task::JoinHandle<()>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!("http://{}", listener.local_addr().unwrap());
    let server = tokio::spawn(async move {
        for (expected, response) in responses {
            let (mut socket, _) = tokio::time::timeout(Duration::from_secs(15), listener.accept())
                .await
                .unwrap()
                .unwrap();
            let mut request = Vec::new();
            loop {
                let mut bytes = [0u8; 4096];
                let read = socket.read(&mut bytes).await.unwrap();
                assert!(read > 0);
                request.extend_from_slice(&bytes[..read]);
                if let Some(end) = request.windows(4).position(|bytes| bytes == b"\r\n\r\n") {
                    let headers = String::from_utf8_lossy(&request[..end]);
                    let length = headers
                        .lines()
                        .find_map(|line| {
                            line.to_ascii_lowercase()
                                .strip_prefix("content-length: ")
                                .and_then(|value| value.parse::<usize>().ok())
                        })
                        .unwrap_or(0);
                    if request.len() >= end + 4 + length {
                        break;
                    }
                }
            }
            let request = String::from_utf8(request).unwrap();
            assert!(
                request.starts_with(&expected),
                "expected {expected}; got {}",
                request.lines().next().unwrap()
            );
            if let Some(response) = response {
                let body = serde_json::json!({"data": response}).to_string();
                let wire = format!("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len());
                socket.write_all(wire.as_bytes()).await.unwrap();
            }
        }
    });
    (
        ProxmoxClient {
            client: reqwest::Client::builder()
                .timeout(Duration::from_secs(5))
                .build()
                .unwrap(),
            api_url: url,
            api_token: "PVEAPIToken=aurora@pve!dp=test-only".into(),
            task_timeout: Duration::from_secs(5),
        },
        server,
    )
}

#[tokio::test]
async fn delete_task_outcomes_distinguish_failure_running_and_missing_timestamp() {
    let path = "GET /api2/json/nodes/node/tasks/task/status".to_string();
    let (client, server) = mock_provider(vec![
        (path.clone(), Some(serde_json::json!({"status":"running"}))),
        (
            path.clone(),
            Some(serde_json::json!({"status":"stopped","exitstatus":"volume locked"})),
        ),
        (
            path.clone(),
            Some(serde_json::json!({"status":"stopped","exitstatus":"OK","endtime":123})),
        ),
        (
            path,
            Some(serde_json::json!({"status":"stopped","exitstatus":"OK"})),
        ),
    ])
    .await;
    assert_eq!(
        client.delete_task_outcome("node", "task").await.unwrap(),
        DeleteTaskOutcome::Running
    );
    assert_eq!(
        client.delete_task_outcome("node", "task").await.unwrap(),
        DeleteTaskOutcome::Failed
    );
    assert_eq!(
        client.delete_task_outcome("node", "task").await.unwrap(),
        DeleteTaskOutcome::Succeeded(123)
    );
    assert!(client.delete_task_outcome("node", "task").await.is_err());
    server.await.unwrap();
}

#[test]
fn proxmox_vm_config_decodes_supported_boot_disk_fields() {
    let config: ProxmoxVmConfig = serde_json::from_str(
        r#"{
            "description": "Managed by Aurora",
            "bootdisk": "virtio0",
            "virtio0": "local-lvm:vm-100-disk-0,size=32G"
        }"#,
    )
    .expect("valid Proxmox config fixture");
    assert_eq!(config.bootdisk.as_deref(), Some("virtio0"));
    assert_eq!(
        config.virtio0.as_deref(),
        Some("local-lvm:vm-100-disk-0,size=32G")
    );
}

#[tokio::test]
async fn delete_task_history_rejects_wrong_scope_and_ignores_other_principals() {
    let path =
        "GET /api2/json/nodes/node/tasks?vmid=100&typefilter=qmdestroy&source=all&start=0&limit=64"
            .to_string();
    let (client, server) = mock_provider(vec![
        (path.clone(), Some(serde_json::json!([
            {"upid":"ours","node":"node","id":"100","type":"qmdestroy","user":"aurora@pve","tokenid":"dp"},
            {"upid":"other","node":"node","id":"100","type":"qmdestroy","user":"root@pam"}
        ]))),
        (path, Some(serde_json::json!([
            {"upid":"wrong-vm","node":"node","id":"999","type":"qmdestroy","user":"aurora@pve","tokenid":"dp"}
        ]))),
    ]).await;
    assert_eq!(
        client.delete_tasks("node", 100).await.unwrap(),
        vec!["ours"]
    );
    assert!(client.delete_tasks("node", 100).await.is_err());
    server.await.unwrap();
}
