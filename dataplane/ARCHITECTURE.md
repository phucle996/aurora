# Dataplane Architecture

Dataplane is the execution process for one Zone. Every replica runs the same
binary and owns only local intake, admission, worker execution, result
settlement, node sampling, and application of already-issued control
directives. Zone-wide control is owned by the distributed Zone Control
scheduler; Dataplane has no leader election, singleton supervisor, or repair
loop.

## Boundary and components

~~~text
                                 Central
        PostgreSQL outbox -> Job Orchestrator -> Kafka Zone topics
                                                   |
                                                   v
+------------------------------ one Zone --------------------------------+
|                                                                         |
| Zone Control replicas                         Dataplane replicas        |
| +---------------------------+                 +-----------------------+ |
| | membership + assignments  |                 | Kafka command intake  | |
| | metadata projection/repair|                 | bounded job queue     | |
| | health probes + Zone report|                 | worker slots          | |
| | storage inventory          |                 | local admission       | |
| | worker-scale controller   |                 | node sampler          | |
| +-------------+-------------+                 | scale directive apply | |
|               |                               +-----------+-----------+ |
|               +--> Zone KV/Kafka ------------------------+             |
|                                                         |             |
|  MinIO / Stalwart / Proxmox                             |             |
|       ^                                                 v             |
|       +------ assigned control side effects       job adapters        |
+-------------------------------------------------------------------------+
~~~

Zone Control work units are assigned with weighted rendezvous hashing and
CAS-updated epochs. A replica losing a unit cancels it before another owner
continues. Metadata and inventory consumers commit only after their KV/outbox
side effect. Health and scaling writes carry the assignment epoch and are
monotonic. Dataplane consumes those projections; it does not produce them.

## Execution path

~~~text
Kafka manual-commit intake
  -> validate Zone, route, version, protected payload and trace context
  -> open HPKE payload from the read-only Zone key mount
  -> bounded queue and Ready-worker budget
  -> acquire fenced job lease after dequeue
  -> execute idempotent storage/mail/hypervisor adapter
  -> completion coordinator publishes durable result/retry/DLQ
  -> settle contiguous source offset
~~~

The worker target is a real concurrency limit. A slot awaits one execution;
scale-down marks slots draining and never reuses a slot ID before its previous
task exits. Lease acquisition begins after queue dwell, so a queued command
cannot consume execution lease time. A durable terminal record always precedes
source settlement.

## Local state and control directives

| State | Writer | Dataplane use |
| --- | --- | --- |
| `AURORA_ZONE_CONFIG` metadata | assigned Zone Control projection | cached admission/config read |
| `AURORA_ZONE_HEALTH/zone.node.*` | each Dataplane sampler | source for Zone Control aggregation |
| `AURORA_ZONE_COORDINATION/signal.workers.scale` | assigned Zone Control scaler | validate and apply target |
| job execution lease | Dataplane execution boundary | duplicate/failure fencing |

The scale follower accepts only the matching Zone, unexpired directive, valid
worker bounds, fresh lag, and monotonic `assignment_epoch`. Invalid or stale
directives retain current capacity. This is a local execution reaction, not a
second controller.

## Failure and rebalance invariants

| Failure | Guard | Outcome |
| --- | --- | --- |
| Zone Control replica dies | assignment TTL + CAS epoch | another replica resumes the unit |
| stale control writer | assignment epoch + monotonic KV write | stale side effect is rejected |
| Dataplane pod dies in flight | Kafka replay + execution lease | command replays; adapter remains idempotent |
| Kafka result unavailable | completion remains unsettled | source offset is replayable |
| stale node sample | freshness/validity checks | Zone Control holds scale target; admission fails closed |
| scale-down during execution | draining slot + generation fence | active job reaches durable boundary |

Signals that used to be named `leader_fencing_token` in the Zone report
protobuf remain wire-compatible for Job Orchestrator, but their producer is now
an assigned Zone Control work unit and the value is the assignment epoch.

## Shutdown

~~~text
SIGTERM -> stop Kafka intake -> drain workers/completion/watchdog
        -> stop local mail runtime -> flush telemetry -> exit
~~~

There is no lease release step because Dataplane does not own a Zone-wide
lease. Unsettled Kafka records remain available for replay.

## Code map

| Concern | Implementation |
| --- | --- |
| Kafka command intake and settlement | `src/job_runtime/intake`, `src/job_runtime/completion` |
| Worker lifecycle and scale apply | `src/workerpool` |
| Local node health snapshot | `src/observability/metrics` |
| Job execution leases | `src/job_runtime/coordination` |
| Zone Control assignment and control duties | `../zone-control/src/orchestrator.rs` and duty modules |
