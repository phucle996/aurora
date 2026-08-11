# Dataplane Architecture

Dataplane is the runtime process for exactly one Zone. Every replica runs the
same binary and can execute jobs. A short-lived, fenced leader role is layered
on top of those workers for Zone-wide singleton duties.

This document is the source of truth for Dataplane component topology,
coordination, execution, and failure boundaries. API workflows and cross-service
durable contracts remain in their dedicated God Views.

## Boundary and components

~~~text
                                 Central
        PostgreSQL outbox -> Job Orchestrator -> Kafka Zone topics
                                                   |
                                                   v
+------------------------------ one Zone --------------------------------+
|                                                                          |
|  Dataplane pod A                  Dataplane pod B                       |
|  +---------------------+          +---------------------+               |
|  | Kafka intake        |          | Kafka intake        |               |
|  | bounded job queue   |          | bounded job queue   |               |
|  | worker slots        |          | worker slots        |               |
|  | local admission     |          | local admission     |               |
|  | NodeRuntimeSampler  |          | NodeRuntimeSampler  |               |
|  | scale follower      |          | scale follower      |               |
|  | leader overlay?     |          | leader overlay?     |               |
|  +----------+----------+          +----------+----------+               |
|             |                                |                            |
|             +----------+ Zone NATS JetStream KV +------------------------+
|                        | metadata / health / coordination                |
|                        +--------------------------------------------------+
|                                                                          |
|  leader-only: metadata projection and repair, Zone report, probes,       |
|  storage-size scan, worker-scale decision                               |
+--------------------------------------------------------------------------+
          |                  |                 |                 |
          v                  v                 v                 v
        Kafka              JMAP             Proxmox          MinIO / STS
        results          / Stalwart
~~~

A leader remains a worker: holding the leadership lease does not remove that
pod from Kafka consumer-group capacity. Only the leader performs recurring
infrastructure probes, metadata repair, Zone reports, inventory scans, and
scale decisions. A worker may contact infrastructure only for a routed business
job.

Dataplane has no Controlplane PostgreSQL, Central Redis, or Vault credential.
Kafka carries durable Central-to-Zone commands and Zone-to-Central results.
NATS Core is ephemeral runtime transport. Zone-local JetStream KV holds
rebuildable metadata, health, leases, and scale coordination; it is not the
durable business source of truth.

## Leader election and fencing

~~~text
Dataplane Pod A                         Zone Coordination KV       Dataplane Pod B
      |                                          |                        |
      +-- CAS acquire leader lease, TTL 15 s --->|<-- CAS acquire --------+
      |<-- owner=A, fencing token N -------------+                        |
      |                                          +---- not acquired ------>|
      |                                                                  retry
      |-- renew owner=A, token N every 5 s ----->|
      |-- current-owner check before side effect ->|
      |                                                                  |
      |  crash, pause, or partition                                     |
      X-- renewal fails ------------------------------------------------>|
      |  cancel all leader duties                                        |
      |                                          |<-- acquire after TTL --+
      |                                          +---- owner=B, token N+1 ->|
~~~

The leader owner ID is hostname plus boot UUID; a restarted process cannot
renew the old incarnation. The coordinator alone renews the lease. Each leader
duty checks current ownership immediately before a probe or publication. Failed
reads and failed renewals fail closed and cancel the entire session.

One duty ending unexpectedly resigns the whole leader session. Duties drain for
at most 8 seconds, then are aborted. Lease release is attempted only by the
current owner; TTL plus monotonic fencing blocks stale external effects after
failover.

## Leader duty topology

~~~text
                                 leader session
                                      |
     +---------------+----------------+------------------+---------------+
     |               |                |                  |               |
     v               v                v                  v               v
metadata listener  repair query   Zone report      health/inventory    scale controller
and Kafka DLQ      publisher      publisher        probes and scan          |
     |               |                |                  |                 |
     +---------------+----------------+------------------+-----------------+
                                      |                                   |
                                      v                                   v
                           Zone metadata / health KV           fenced scale directive
                                                                      |
                                                                      v
                                                       scale follower on every pod
~~~

The leader supervises one concrete duty set: metadata listener and repair
publisher, Zone report publisher, Proxmox/MinIO/JMAP/Stalwart health probes,
storage bucket-size scan, and Zone worker-scale controller.

Metadata and storage-size messages are at-least-once. An invalid record goes to
its durable DLQ before source settlement. Health writes carry the current leader
fencing token and use monotonic CAS semantics.

## Node sampling and worker scaling

~~~text
cgroup v2 (/proc fallback) --+
Kafka local lag cache -------+--> NodeRuntimeSampler, every 5 s
worker-slot registry --------+              |
                                            +--> immutable RAM snapshot
                                            |      |             |
                                            |      |          local admission
                                            |      +---------- Zone Health KV: zone.node.<node_id>
                                            +----------------- OTel export only

fresh node snapshots -> leader aggregate -> fenced scale directive, TTL 15 s
                                              |
                                              v
                           scale follower on every pod -> worker slot target
~~~

Each process has one sampler. Its immutable snapshot contains resource usage,
CPU throttling, memory working set, worker-state counts, admitted jobs, Kafka
lag/freshness, and payload-key readiness. Admission consumes that in-memory
snapshot; it never reads from OTel or the Collector.

A sample is stale when invalid, future-dated, or older than 15 seconds.
Admission fails closed rather than treating unreadable cgroup data as spare
capacity. The leader aggregates only fresh snapshots, protects the hottest node
using maximum CPU/RAM/throttling values, and otherwise keeps the previous target.

The controller needs two consecutive observations to scale up and six calm
observations to scale down. Cooldowns are 15 seconds and 30 seconds
respectively. Scale-down removes one slot at a time.

~~~text
scale directive = zone_id + target_per_node + observed lag + lag_stale
                  + issued_at + expiry + leader_fencing_token

follower accepts only:
  matching Zone, non-stale lag, current expiry,
  target in configured bounds, and monotonic fence/timestamp
otherwise:
  retain current capacity
~~~

Scale coordination is soft, short-lived state. It does not replace business
desired state and does not control Kubernetes HPA.

## Job execution and durable settlement

~~~text
Kafka manual-commit intake
  -> validate Zone, route, version, protected payload, trace context
  -> bounded multi-consumer queue
  -> worker slot owns one execution at a time
  -> acquire fenced job lease only after dequeue
  -> execute with watchdog renew and cancellation
  -> completion coordinator
  -> durable Kafka result, retry, or DLQ
  -> settle contiguous source offset, per partition
~~~

The worker target is a real concurrency limit: a slot awaits one execution
rather than spawning detached tasks. Slots are Starting, Ready, or Draining,
each with a generation fence. Scale-down cancels further receives but lets the
active job reach its fenced durability boundary; an ID is not reused until the
old task exits.

The job lease starts at the execution boundary, so queue dwell cannot consume
its TTL. Lease contention uses bounded delayed retry. A retry record becomes
durable before its original source record can settle. The bounded completion
coordinator, not the watchdog, owns terminal result/retry/DLQ publication and
source settlement.

~~~text
watchdog: renew 30 s lease every 10 s
  -> current registration and phase recheck
  -> on timeout: cancel execution and enqueue bounded completion event
  -> completion: publish durable terminal record first
  -> settlement: advance only contiguous offsets in one partition
~~~

The watchdog has 3-second renew timeouts and bounded concurrency. Its registry
uses a registration ID and phase progression Preparing -> Executing ->
Completing, so an old snapshot cannot cancel or report a newer job. The
settlement window is bounded and never commits a higher offset across an
unfinished lower one. A critical runtime task exit fences active work and causes
a bounded graceful process stop; records not durably settled replay on restart.

## Shutdown ownership

~~~text
SIGTERM
  -> stop intake and leader/scale loops through cancellation
  -> mark worker slots draining; no new receive
  -> wait for tracked workers, retry scheduler, completion reporter,
     watchdog, sampler, follower, and lease cleanup
  -> stop shared mail runtime
  -> flush OpenTelemetry
  -> release leader lease when still current owner
~~~

All asynchronous work registers in the process-wide shutdown barrier before its
parent can complete. Dataplane does not stop mail runtime or OpenTelemetry while
tracked work can still publish a result, settle an offset, or release a lease.

## Failure invariants

| Failure or race | Guard | Outcome |
| --- | --- | --- |
| two pods elect together | Zone KV CAS | one current leader |
| old leader resumes | owner and monotonic fencing | stale probe/publication rejected |
| Zone KV partition | renewal/current-owner check | leader duties fail closed |
| leader duty panic | one supervised session | all duties resign and election restarts |
| stale lag or node sample | freshness/validity checks | keep scale target; admission fails closed |
| stale or foreign directive | Zone, expiry, range, fencing checks | capacity is retained |
| scale-down during execution | draining state and generation | current job completes; slot ID is fenced |
| queued job waits too long | lease after dequeue | no expired lease begins execution |
| completion/Kafka failure | durable record before settlement | source remains unsettled for replay |
| out-of-order completion | per-partition terminal set | no commit across a gap |
| worker dies in flight | Kafka replay plus job lease/fencing | at-least-once; executor remains idempotent |

Leader election alone never creates exactly-once external behavior. Every
executor and consumer retains its own stable identifiers, ordering constraint,
retry boundary, and idempotency rule.

## Code map

| Concern | Implementation |
| --- | --- |
| application wiring and startup | src/app.rs, src/bootstrap.rs |
| leader entry point, election, fencing, duty supervision | src/leader/mod.rs, src/leader/leadership.rs |
| metadata, report, probes, storage scan, scale policy | src/leader/zone_metadata.rs, src/leader/zone_report.rs, src/leader/infra/, src/leader/worker_scaling.rs |
| Zone KV leases and fencing | src/infra/zone_kv.rs |
| worker lifecycle, generation, shutdown barrier | src/workerpool/pool.rs |
| worker-scale directive follower | src/workerpool/scale_follower.rs |
| bounded intake, execution, completion, watchdog | src/job_runtime/ |
| runtime sampling and local-admission signals | src/observability/metrics.rs |

