use super::*;
use crate::executor::ExecutorError;
use crate::job_runtime::test::validated_job;
use hypervisor_proto::{VmDeleteJournalV1, VmDeleteResultV1, VmDeleteV1};
use prost::Message;
use serde_json::{json, Value};

const INVENTORY: &str = "GET /api2/json/cluster/resources?type=vm";
const HISTORY: &str =
    "GET /api2/json/nodes/node/tasks?vmid=100&typefilter=qmdestroy&source=all&start=0&limit=64";

fn task(upid: &str) -> Value {
    json!({"upid":upid,"node":"node","id":"100","type":"qmdestroy","user":"aurora@pve","tokenid":"dp"})
}

#[tokio::test]
#[ignore = "requires dedicated AURORA_TEST_NATS"]
async fn vm_absence_without_evidence_never_fabricates_a_completion_timestamp() {
    let store = ZoneKvStore::for_test().await;
    let id = uuid::Uuid::new_v4();
    let command = VmDeleteV1 {
        schema_version: 1,
        vm_id: id.as_bytes().to_vec(),
        provider_name: format!("aurora-{id}"),
        provider_vmid: 100,
    };
    let job = validated_job(
        "HYPERVISOR",
        "hypervisor.vm.delete",
        &id.to_string(),
        &command.encode_to_vec(),
    );
    let (runtime, server) = runtime(store, vec![(INVENTORY.into(), Some(json!([])))]).await;
    assert!(matches!(processor::execute_vm_delete(job, runtime).await,
        Err(ExecutorError::OutcomeUnknown(ref code)) if code == "VM_DELETE_COMPLETION_EVIDENCE_UNAVAILABLE"));
    server.await.unwrap();
}

#[tokio::test]
#[ignore = "requires dedicated AURORA_TEST_NATS"]
async fn ambiguous_provider_history_never_guesses_which_task_deleted_the_vm() {
    let store = ZoneKvStore::for_test().await;
    let id = uuid::Uuid::new_v4();
    let command = VmDeleteV1 {
        schema_version: 1,
        vm_id: id.as_bytes().to_vec(),
        provider_name: format!("aurora-{id}"),
        provider_vmid: 100,
    };
    let job = validated_job(
        "HYPERVISOR",
        "hypervisor.vm.delete",
        &id.to_string(),
        &command.encode_to_vec(),
    );
    let journal = VmDeleteJournalV1 {
        schema_version: 1,
        vm_id: command.vm_id,
        provider_name: command.provider_name,
        provider_vmid: 100,
        provider_node: "node".into(),
        ..Default::default()
    };
    store
        .config_create(
            format!("hypervisor.vm.deletion.journal.{}", job.job_id),
            journal.encode_to_vec().into(),
        )
        .await
        .unwrap();
    let (runtime, server) = runtime(
        store,
        vec![(HISTORY.into(), Some(json!([task("first"), task("second")])))],
    )
    .await;
    assert!(matches!(processor::execute_vm_delete(job, runtime).await,
        Err(ExecutorError::OutcomeUnknown(ref code)) if code == "VM_DELETE_TASK_RECOVERY_AMBIGUOUS"));
    server.await.unwrap();
}

async fn runtime(
    store: Arc<ZoneKvStore>,
    responses: Vec<(String, Option<Value>)>,
) -> (Arc<HypervisorRuntime>, tokio::task::JoinHandle<()>) {
    let (proxmox, server) = processor::mock_provider(responses).await;
    (
        Arc::new(HypervisorRuntime {
            proxmox: Arc::new(proxmox),
            provider_bindings: Arc::new(runtime::ProviderBindingRuntime::new(store.clone())),
            zone_kv: store,
            image_store: None,
            mutation_limit: Arc::new(Semaphore::new(1)),
        }),
        server,
    )
}

#[tokio::test]
#[ignore = "requires dedicated AURORA_TEST_NATS"]
async fn vm_delete_lost_http_ack_recovers_task_and_original_completion_time() {
    let store = ZoneKvStore::for_test().await;
    let id = uuid::Uuid::new_v4();
    let name = format!("aurora-{id}");
    let command = VmDeleteV1 {
        schema_version: 1,
        vm_id: id.as_bytes().to_vec(),
        provider_name: name.clone(),
        provider_vmid: 100,
    };
    let job = validated_job(
        "HYPERVISOR",
        "hypervisor.vm.delete",
        &id.to_string(),
        &command.encode_to_vec(),
    );
    let inventory =
        json!([{"vmid":100,"name":name,"node":"node","type":"qemu","netin":10,"netout":20}]);
    let (runtime, server) = runtime(
        store.clone(),
        vec![
            (INVENTORY.into(), Some(inventory.clone())),
            (
                "GET /api2/json/nodes/node/qemu/100/status/current".into(),
                Some(json!({"status":"stopped"})),
            ),
            (INVENTORY.into(), Some(inventory)),
            (HISTORY.into(), Some(json!([]))),
            ("DELETE /api2/json/nodes/node/qemu/100 ".into(), None),
            (HISTORY.into(), Some(json!([task("delete-new")]))),
            (
                "GET /api2/json/nodes/node/tasks/delete-new/status".into(),
                Some(json!({"status":"stopped","exitstatus":"OK","endtime":1700000000})),
            ),
            (INVENTORY.into(), Some(json!([]))),
            (INVENTORY.into(), Some(json!([]))),
        ],
    )
    .await;
    assert!(matches!(
        processor::execute_vm_delete(job.clone(), runtime.clone()).await,
        Err(ExecutorError::OutcomeUnknown(_))
    ));
    let key = format!("hypervisor.vm.deletion.journal.{}", job.job_id);
    let journal =
        VmDeleteJournalV1::decode(store.config_get(&key).await.unwrap().unwrap()).unwrap();
    assert!(journal.task_upid.is_empty()); // provider ACK was lost, intent survived
    for _ in 0..2 {
        let result = processor::execute_vm_delete(job.clone(), runtime.clone())
            .await
            .unwrap();
        let result = VmDeleteResultV1::decode(result.result_payload.as_slice()).unwrap();
        assert_eq!(result.provider_completed_at_unix_ms, 1700000000000);
    }
    server.await.unwrap();
}

#[tokio::test]
#[ignore = "requires dedicated AURORA_TEST_NATS"]
async fn vm_delete_failed_task_is_retired_before_new_provider_attempt() {
    let store = ZoneKvStore::for_test().await;
    let id = uuid::Uuid::new_v4();
    let name = format!("aurora-{id}");
    let command = VmDeleteV1 {
        schema_version: 1,
        vm_id: id.as_bytes().to_vec(),
        provider_name: name.clone(),
        provider_vmid: 100,
    };
    let job = validated_job(
        "HYPERVISOR",
        "hypervisor.vm.delete",
        &id.to_string(),
        &command.encode_to_vec(),
    );
    let key = format!("hypervisor.vm.deletion.journal.{}", job.job_id);
    let journal = VmDeleteJournalV1 {
        schema_version: 1,
        vm_id: command.vm_id.clone(),
        provider_name: name.clone(),
        provider_vmid: 100,
        provider_node: "node".into(),
        task_upid: "failed".into(),
        ..Default::default()
    };
    store
        .config_create(&key, journal.encode_to_vec().into())
        .await
        .unwrap();
    let inventory = json!([{"vmid":100,"name":name,"node":"node","type":"qemu"}]);
    let (runtime, server) = runtime(
        store.clone(),
        vec![
            (
                "GET /api2/json/nodes/node/tasks/failed/status".into(),
                Some(json!({"status":"stopped","exitstatus":"locked"})),
            ),
            (HISTORY.into(), Some(json!([task("failed")]))),
            (INVENTORY.into(), Some(inventory.clone())),
            (
                "GET /api2/json/nodes/node/qemu/100/status/current".into(),
                Some(json!({"status":"stopped"})),
            ),
            (INVENTORY.into(), Some(inventory)),
            (
                "DELETE /api2/json/nodes/node/qemu/100 ".into(),
                Some(json!("replacement")),
            ),
            (
                "GET /api2/json/nodes/node/tasks/replacement/status".into(),
                Some(json!({"status":"stopped","exitstatus":"OK","endtime":1700000001})),
            ),
            (INVENTORY.into(), Some(json!([]))),
        ],
    )
    .await;
    assert!(matches!(
        processor::execute_vm_delete(job.clone(), runtime.clone()).await,
        Err(ExecutorError::Retryable(_))
    ));
    let journal =
        VmDeleteJournalV1::decode(store.config_get(&key).await.unwrap().unwrap()).unwrap();
    assert_eq!(journal.failed_tasks, 1);
    assert_eq!(journal.previous_task_upids, vec!["failed"]);
    assert!(journal.task_upid.is_empty());
    assert!(matches!(
        processor::execute_vm_delete(job.clone(), runtime.clone()).await,
        Err(ExecutorError::OutcomeUnknown(_))
    ));
    assert!(processor::execute_vm_delete(job, runtime).await.is_ok());
    server.await.unwrap();
}
