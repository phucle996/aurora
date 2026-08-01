use super::entity::{
    ManagedServiceCommand, ManagedServiceFailure, ManagedServiceObservedState, RenderedGraph,
};
use super::kubernetes::KubernetesRuntime;

pub(crate) async fn apply_graph(
    runtime: &KubernetesRuntime,
    command: &ManagedServiceCommand,
    graph: &RenderedGraph,
) -> Result<ApplyObservation, ManagedServiceFailure> {
    runtime.ensure_namespace(&graph.namespace, command).await?;
    let mut observed = ManagedServiceObservedState::Unknown;
    // Apply order is part of the immutable SRE component contract. A later
    // component is never started before an earlier component has passed its
    // own readiness gate, which bounds dependency races during convergence.
    for resource in &graph.resources {
        runtime
            .apply_resource(&resource.identity, &resource.manifest, command)
            .await?;
        observed = runtime.wait_ready(&resource.identity).await?;
    }
    Ok(ApplyObservation { state: observed })
}

pub(crate) struct ApplyObservation {
    pub state: ManagedServiceObservedState,
}
