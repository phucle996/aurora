// Workflow-local tests are kept beside the managed-service implementation so
// they cannot accidentally share fixtures with mail, storage or hypervisor.

use std::collections::BTreeMap;

use prost::Message;
use serde_json::json;
use sha2::{Digest, Sha256};
use uuid::Uuid;

use crate::infra::kafka::managed_service_proto::{
    ManagedServiceCommandV1, ManagedServiceComponentV1, ManagedServiceOperationKindV1,
    ManagedServiceOwnerTypeV1,
};

use super::admission::{admit_command, component_contract_hash_for_test, ManagedServiceOuterFence};
use super::entity::{
    ManagedServiceCommand, ManagedServiceComponent, ManagedServiceOperation, ManagedServiceOwner,
};
use super::renderer::render_graph;

fn command(template_yaml: &str, operation: ManagedServiceOperation) -> ManagedServiceCommand {
    let owner_id = Uuid::new_v4();
    let workspace_id = Uuid::new_v4();
    let zone_id = Uuid::new_v4();
    let instance_id = Uuid::new_v4();
    let components = vec![ManagedServiceComponent {
        id: "primary".to_owned(),
        document_indexes: vec![],
        apply_order: 1,
        delete_order: 1,
        readiness_rule: "exists".to_owned(),
        readiness_deadline_seconds: 30,
    }];
    let mut parameters = BTreeMap::new();
    parameters.insert("replicas".to_owned(), json!(2));
    ManagedServiceCommand {
        command_event_id: Uuid::new_v4(),
        operation_id: Uuid::new_v4(),
        instance_id,
        owner_type: ManagedServiceOwner::Personal,
        owner_id,
        workspace_id,
        zone_id,
        instance_code: "demo".to_owned(),
        operation,
        generation: 1,
        instance_revision_id: Uuid::new_v4(),
        blueprint_revision_id: Uuid::new_v4(),
        template_yaml: template_yaml.to_owned(),
        component_contract_hash: component_contract_hash_for_test(&components),
        bundle_hash: Sha256::digest(template_yaml.as_bytes()).into(),
        input_hash: Sha256::digest(br#"{"replicas":2}"#).into(),
        desired_spec_hash: Sha256::digest(b"desired").into(),
        parameters,
        components,
        _issued_at_unix_ms: 1,
    }
}

#[test]
fn renderer_replaces_typed_parameter_and_injects_namespace_markers() {
    let command = command(
        "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: !aurora/component primary\nspec:\n  replicas: !aurora/param replicas\n",
        ManagedServiceOperation::Create,
    );
    let graph = render_graph(&command).expect("typed template should render");
    assert_eq!(graph.resources.len(), 1);
    let manifest = serde_json::to_value(&graph.resources[0].manifest).expect("yaml to json");
    assert_eq!(manifest["metadata"]["name"], "demo");
    assert_eq!(manifest["spec"]["replicas"], 2);
    assert_eq!(
        manifest["metadata"]["annotations"]["platform.aurora.io/instance-id"],
        command.instance_id.to_string()
    );
    assert_eq!(
        manifest["metadata"]["labels"]["platform.aurora.io/component"],
        "primary"
    );
}

#[test]
fn renderer_rejects_literal_secret_values() {
    let command = command(
        "apiVersion: v1\nkind: Secret\nmetadata:\n  name: !aurora/component primary\nstringData:\n  password: plaintext\n",
        ManagedServiceOperation::Create,
    );
    let error = match render_graph(&command) {
        Ok(_) => panic!("literal secret must be rejected"),
        Err(error) => error,
    };
    assert_eq!(error.code, "SRE_TEMPLATE_INPUT_MISMATCH");
}

#[test]
fn renderer_rejects_component_tag_outside_root_metadata_name() {
    let command = command(
        "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: !aurora/component primary\n  annotations:\n    bad: !aurora/component primary\n",
        ManagedServiceOperation::Create,
    );
    let error = match render_graph(&command) {
        Ok(_) => panic!("secondary component tag must be rejected"),
        Err(error) => error,
    };
    assert_eq!(error.code, "SRE_TEMPLATE_INPUT_MISMATCH");
}

#[test]
fn component_contract_hash_covers_explicit_document_set() {
    let component = ManagedServiceComponent {
        id: "primary".to_owned(),
        document_indexes: vec![0, 2],
        apply_order: 1,
        delete_order: 1,
        readiness_rule: "exists".to_owned(),
        readiness_deadline_seconds: 30,
    };
    let canonical = vec![json!({
        "id": "primary",
        "document_indexes": [0, 2],
        "apply_order": 1,
        "delete_order": 1,
        "readiness": {"type": "exists", "deadline_seconds": 30}
    })];
    let expected: [u8; 32] =
        Sha256::digest(serde_json::to_vec(&canonical).expect("bounded test contract")).into();

    assert_eq!(component_contract_hash_for_test(&[component]), expected);
}

#[test]
fn admission_rejects_outer_fence_mismatch_before_execution() {
    let owner_id = Uuid::new_v4();
    let workspace_id = Uuid::new_v4();
    let zone_id = Uuid::new_v4();
    let command_event_id = Uuid::new_v4();
    let parameters = br#"{"replicas":2}"#.to_vec();
    let component = ManagedServiceComponentV1 {
        component_id: "primary".to_owned(),
        document_indexes: vec![],
        apply_order: 1,
        delete_order: 1,
        readiness_rule: "exists".to_owned(),
        readiness_deadline_seconds: 30,
    };
    let component_json = vec![json!({
        "id": "primary",
        "apply_order": 1,
        "delete_order": 1,
        "readiness": {"type": "exists", "deadline_seconds": 30}
    })];
    let component_hash: [u8; 32] =
        Sha256::digest(serde_json::to_vec(&component_json).unwrap()).into();
    let template = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: !aurora/component primary\ndata:\n  count: !aurora/param replicas\n";
    let input_hash: [u8; 32] = Sha256::digest(&parameters).into();
    let desired_source = format!(
        "{}:{}:{}:{}:{}:",
        workspace_id,
        zone_id,
        "demo",
        Uuid::new_v4(),
        1
    );
    let mut desired_bytes = desired_source.into_bytes();
    desired_bytes.extend_from_slice(&parameters);
    let instance_id = Uuid::new_v4();
    let wire = ManagedServiceCommandV1 {
        command_event_id: command_event_id.as_bytes().to_vec(),
        operation_id: Uuid::new_v4().as_bytes().to_vec(),
        instance_id: instance_id.as_bytes().to_vec(),
        owner_type: ManagedServiceOwnerTypeV1::ManagedServiceOwnerTypePersonal as i32,
        owner_id: owner_id.as_bytes().to_vec(),
        workspace_id: workspace_id.as_bytes().to_vec(),
        zone_id: zone_id.as_bytes().to_vec(),
        instance_code: "demo".to_owned(),
        operation_kind: ManagedServiceOperationKindV1::ManagedServiceOperationKindCreate as i32,
        generation: 1,
        instance_revision_id: Uuid::new_v4().as_bytes().to_vec(),
        blueprint_revision_id: Uuid::new_v4().as_bytes().to_vec(),
        template_yaml: template.to_owned(),
        components: vec![component],
        bundle_hash: Sha256::digest(template.as_bytes()).to_vec(),
        component_contract_hash: component_hash.to_vec(),
        input_hash: input_hash.to_vec(),
        desired_spec_hash: Sha256::digest(desired_bytes).to_vec(),
        parameter_values: parameters.clone(),
        parameter_values_sha256: input_hash.to_vec(),
        schema_version: 1,
        issued_at_unix_ms: chrono::Utc::now().timestamp_millis(),
        traceparent: String::new(),
        tracestate: String::new(),
    };
    let error = match admit_command(
        &wire.encode_to_vec(),
        ManagedServiceOuterFence {
            job_id: command_event_id,
            resource_id: &Uuid::new_v4().to_string(),
            zone_id: Uuid::new_v4(),
            source_domain: "MANAGED_SERVICE",
            job_topic: "managed_service.instance.execute",
            payload_schema_version: 1,
        },
    ) {
        Ok(_) => panic!("outer zone fence must be checked before execution"),
        Err(error) => error,
    };
    assert_eq!(error.code, "MANAGED_SERVICE_OUTER_FENCE_MISMATCH");
}
