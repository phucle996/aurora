use super::entity::{
    ManagedServiceCommand, ManagedServiceFailure, ManagedServiceObservedState, RenderedGraph,
};
use super::kubernetes::KubernetesRuntime;

pub(crate) async fn delete_graph(
    runtime: &KubernetesRuntime,
    command: &ManagedServiceCommand,
    graph: &RenderedGraph,
) -> Result<DeleteObservation, ManagedServiceFailure> {
    let mut resources = graph.resources.iter().collect::<Vec<_>>();
    // delete_order is an explicit execution order, not an apply priority to
    // invert implicitly. SRE can encode workload -> service -> policy directly.
    resources.sort_by_key(|resource| resource.identity.delete_order);
    for resource in resources {
        runtime.delete_resource(&resource.identity, command).await?;
    }
    Ok(DeleteObservation {
        state: ManagedServiceObservedState::Ready,
    })
}

pub(crate) struct DeleteObservation {
    pub state: ManagedServiceObservedState,
}
