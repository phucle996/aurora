# Dataplane Runtime - Job Consumer & Admission Control Specification

This document specifies the generic Dataplane execution runtime. It details the worker loop, concurrency limits (admission control), distributed lease locks, and task scheduling mechanisms.

---

## 🔄 Ingestion & Concurrency Lifecycle Sequence

All incoming jobs pushed to the Redis stream `jobs:<zone_id>` are fetched, validated, and scheduled according to the following runtime sequence:

```mermaid
sequenceDiagram
    autonumber
    participant RDS as Redis Streams (jobs:<zone_id>)
    participant JC as Ingestion Loop (JobConsumer)
    participant AC as Admission Controller
    participant LZ as Local Cache (redis_internal_zone)
    participant JR as Job Runner (JobRunner)

    loop Concurrency & Admission Evaluation
        JC->>JC: Query static max_workers config limit
        JC->>AC: Evaluate load (evaluate(active_jobs, max_workers))
        alt Circuit Broken (active_jobs >= max_workers)
            Note over JC: Pause pulling from Redis Stream<br/>Back off & sleep for 500ms
        else Pacing Mode (active_jobs near limit)
            Note over JC: Rate-pacing delay (sleep for pacing_delay_ms)
        else Normal Capacity
            Note over JC: Proceed to pull next stream message
        end
    end

    JC->>RDS: Blocking Read next message (fetch_next_stream_message via XREADGROUP)
    RDS-->>JC: Return job payload (event_id, payload, trace_id)
    
    JC->>LZ: Acquire Lease Lock on locks:job:<job_id> (acquire_lease_lock)
    alt Lock Already Held by other replica
        LZ-->>JC: Return lock acquisition failed (skip execution)
    else Lock Acquired Successfully
        LZ-->>JC: Return lock acquired
        JC->>JC: Increment local active_jobs counter (atomic fetch_add)
        
        JC->>JR: Spawn runner thread (JobRunner::run_job)
        Note over JR: Register ExecutionCleanupGuard (RAII lock/counter release)
        JR->>RDS: Report outcome with status: PROCESSING (XADD to job_results_stream)
        Note over JR: Dispatch specific workload executor (e.g., SmtpTestExecutor)
    end
```

---

## 📥 How Jobs are Received (Job Ingestion Loop)

- **Entrypoint File**: `dataplane/src/job_lifecycle/consumer.rs`
- **Caller/Function**: `JobConsumer::start_ingestion` (Starts at Line 32)
- **Redis Query Callsite**: `dataplane/src/infra/redis/query.rs` -> `fetch_next_stream_message` (Starts at Line 18)

### Description

The main ingestion engine runs a continuous, async-blocking loop polling the zone-specific Redis Stream `jobs:<zone_id>`. This ingestion loop runs within an independent worker spawned dynamically as a Tokio green task by the `WorkerLifecycleManager`.

To facilitate automated scaling, the orchestrator polls real-time queue metrics (including Redis stream lag, message latency, and connection states) and feeds them into the `AutoScaleEngine`. Based on these metrics, the pool dynamically provisions additional workers (scale-up) or terminates active loops (scale-down) up to the limit configured by environment variables (`max_workers`). If no new messages are present, the worker dynamically paces itself before retrying.

---

## 🛡️ How Admission Control Limits Overloads

- **Entrypoint File**: `dataplane/src/job_lifecycle/admission.rs`
- **Caller/Function**: `AdmissionController::evaluate` (Starts at Line 35)

### Description

Before pulling new messages from the Redis Stream, the ingestion loop evaluates the node's capacity ratio. The `evaluate` method computes the resource index `r = max(active_ratio, cpu_usage, ram_usage)` where `active_ratio = current_active / max_workers`:

- **Circuit Broken Mode**: If `r >= 80%` (`0.8`), the circuit breaker trips (OPEN), pausing stream ingestion and sleeping for `500ms`. It remains OPEN until load recovers and drops below `50%` (`0.5`) (hysteresis threshold).
- **Pacing Mode**: If `0.0 < r < 0.8`, a dynamic, linear pacing delay is applied: `pacing_delay_ms = 1000 * (r / 0.8)` ms. This delays the fetch operation to naturally pace the ingestion rate before the node hits limits, preventing thundering herds.

---

## 🔒 How Distributed Lease Locks & Watchdog are Managed

- **Lock Acquisition Callsite**: `dataplane/src/infra/redis/query.rs` -> `acquire_lease_lock` (Starts at Line 142)
- **Lock Release Callsite**: `dataplane/src/infra/redis/query.rs` -> `release_lease_lock` (Starts at Line 163)
- **Registry**: `dataplane/src/workerpool/watchdog.rs` -> `ActiveLockRegistry` (Starts at Line 31)
- **Watchdog Background Loop**: `dataplane/src/workerpool/watchdog.rs` -> `start_watchdog_loop` (Starts at Line 111)

### 1. Distributed Lease Lock Concept

To achieve reliable multi-replica execution in a High Availability (HA) cluster and guarantee exactly-once processing, a distributed lease lock is acquired on Redis before processing a job:

- **Key Pattern**: `locks:job:<job_id>`
- **Acquisition**: Done via `SETNX` with a TTL of 30 seconds.
- **Fail-Safe**: If lock acquisition fails (meaning another Dataplane node is already processing the job), the current node immediately skips the job message to avoid double execution.

### 2. Active Lock Registry

Once the lock is successfully acquired, its metadata is registered in a thread-safe registry:

- **Structure**: `ActiveLockRegistry` wrapping a `RwLock<HashMap<String, ActiveLockInfo>>`.
- **Metadata stored**:
  - `started_at` (timestamp when the execution started).
  - `max_execution_limit` (the `idle` timeout option specified in the job payload, or a fallback default).
  - `abort_handle` (the Tokio `AbortHandle` of the running task).
  - `job_id`, `job_version`, `attempt`.

### 3. Background Watchdog Loop (Auto-Renewal via Pipelining)

The Watchdog runs as a background task executing every 10 seconds. In each tick, it scans all active registered locks:

- **Renewal (Time elapsed < limit)**:
  - If the job is still running and the elapsed time has not hit `max_execution_limit`, the Watchdog includes the lock key in a batch list.
  - To optimize performance and prevent network blocking under high-concurrency, the Watchdog performs **Redis Pipelining** to batch-extend the expiration TTL of all running locks (`EXPIRE key 30`) in a single network request.
- **Forced Timeout Abort (Time elapsed >= limit)**:
  - If a job hangs or exceeds its maximum execution limit, the Watchdog triggers proactive cancellation:
    1. Calls `abort_handle.abort()` to terminate the Tokio green task executing the job.
    2. Deregisters the lock from the registry.
    3. Spawns a background task to report a `FAILED` result with error code `EXECUTION_TIMEOUT` to the Redis Result Stream (`job_results_stream`).

### 4. RAII Cleanup Guard (`ExecutionCleanupGuard`)

To prevent lock leaks and resource leaks when a task finishes, is aborted, or panics, the execution is wrapped using an RAII pattern:

- **Drop Lifecycle**:
  - The runner registers `ExecutionCleanupGuard` which is bound to the task context.
  - When the task completes (successfully or via failure/abort/panic), the guard's `drop()` method is automatically called.
  - On drop, the guard:
    1. **Instantly deregisters** the lock from the `ActiveLockRegistry` to stop the Watchdog from sending lease renewals.
    2. Spawns a detached tokio task to call `release_lease_lock` (deleting the Redis lock key) and atomically decrements the global `active_jobs` counter.

---

## 🚀 How Jobs are Executed & Cleaned Up

- **Task Spawner Callsite**: `dataplane/src/job_lifecycle/runner.rs` -> `JobRunner::run_job` (Starts at Line 37)
- **RAII Cleanup Guard**: `dataplane/src/job_lifecycle/runner.rs` -> `ExecutionCleanupGuard::drop` (Starts at Line 19)

### Description

Once the lock is acquired, the consumer increments the active task counter and calls `run_job`, spawning a non-blocking async Tokio task:

- **Early Processing Notification**: Before executing the workload, the runner immediately publishes a `PROCESSING` status update via the Redis Stream `job_results_stream` (XADD). This allows the Job-Proxy to receive the update, modify the Controlplane DB status to `PROCESSING` in a transaction, and trigger real-time progress notifications to the UI.
- **Execution Guard**: The runner registers an `ExecutionCleanupGuard` (RAII pattern).
- **Execution Timeout**: Workload execution is regulated by the Watchdog loop. The watchdog monitors the execution time against the specific task's `idle` limit (default 10 minutes). If a task exceeds its limit, the Watchdog triggers proactive cancellation via `AbortHandle::abort()`.
- **Auto Release**: When the executor finishes (or is aborted/panics), the `ExecutionCleanupGuard` drops. This drops the active job count and releases the Redis lock asynchronously, ensuring complete resource cleanup and preventing leakage.

---

## 📐 Architectural Design Patterns

The Dataplane's high-performance, resilient runtime is built using several core design patterns:

### 1. Job Ingestion Loop: Reactive Pull / Event Loop Pattern

- **Implementation**: `dataplane/src/job_lifecycle/consumer.rs`
- **Pattern**: An asynchronous, non-blocking polling loop subscribing to Redis Streams via `XREADGROUP`. It operates on a **Pull Model** to allow backpressure propagation where downstream workers decide when they are ready to process more items, rather than having tasks pushed to them blindly.

### 2. Admission Control: Circuit Breaker with Hysteresis & Rate Pacing

- **Implementation**: `dataplane/src/job_lifecycle/admission.rs`
- **Pattern**: Implements a **Circuit Breaker** with hysteresis bounds to shield the node from cascading resource failures. If local CPU, RAM, or worker thread usage exceeds `80%`, the loop cuts ingestion (OPEN state). It stays open until usage drops below `50%` (CLOSED state). Additionally, near-capacity workloads trigger a linear **Rate Pacing Delay** to prevent thundering herd spikes.

### 3. Worker Pool & Auto Scale: Dynamic Task Spawning & Scaler Strategy

- **Implementation**: `dataplane/src/workerpool/lifecycle.rs` & `dataplane/src/workerpool/auto_scale.rs`
- **Pattern**: Instead of managing a custom operating system thread-pool with heavy mutex locks, this implementation uses a **Tokio task-allocation pattern** where logical workers map directly to lightweight Tokio green tasks. The `WorkerLifecycleManager` manages graceful shutdowns via `CancellationToken` and tracking wrappers. The `AutoScaleEngine` applies a **Strategy Pattern** to scale active loops up or down based on lag and latency metrics, with a hard resource safeguard capping scaling at 90%.

### 4. Distributed Lock and Lease: Watchdog-driven Lease & Execution Timeout Control Pattern

- **Implementation**: `dataplane/src/infra/redis/query.rs`, `dataplane/src/workerpool/watchdog.rs` & `dataplane/src/job_lifecycle/runner.rs`
- **Pattern**: Implements a **Lease Lock Pattern** using Redis key-value isolation (`SETNX` with TTL) to guarantee exactly-once processing in HA clusters. To prevent ghost-job duplicates and hung worker execution, the node maintains a thread-safe `ActiveLockRegistry` containing active task start times, abort handles, and execution limits, monitored by a background **Watchdog loop**. Every 10 seconds, the watchdog checks all active locks. If the elapsed execution time is below the task-specific `idle` timeout, the watchdog uses **Redis Pipelining** to batch-extend the lease TTL on Redis by 30 seconds. If a task exceeds its timeout limit, the watchdog triggers proactive task cancellation via its `AbortHandle`, stops extending the lease, and reports an `EXECUTION_TIMEOUT` failure to the Controlplane.

### 5. Job Dispatch Workload: Command / Strategy Pattern

- **Implementation**: `dataplane/src/job_lifecycle/consumer.rs`
- **Pattern**: Utilizes a dynamic **Command Dispatcher** pattern. The ingestion loop parses incoming jobs by their dot-separated namespace/topic prefix (e.g., routing `mail.test_connection` to `SmtpTestExecutor`). The runner resolves these namespaces to individual execution strategies, decoupling the ingestion loop framework from concrete job business logic.
