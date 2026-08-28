# Workflow-first Proto Registry Refactor Design

Date: 2026-08-28
Status: Approved direction; pending written-spec review

## Context

Aurora keeps protobuf sources under the root `proto/` registry, but the current
layout mixes three incompatible classification axes:

- service owners such as `acr/`, `controlplane/`, `job-orchestrator/` and
  `dataplane/`;
- business workflows such as IAM device presence, VM lifecycle and mail
  consumer runtime;
- consumers that carry local copies of a producer-owned wire contract.

This creates duplicate definitions, unclear change authority and drift between
producer and consumer schemas. Examples include the two IAM device-presence
sources and the overlapping Controlplane, Job Orchestrator and Dataplane job
files for Hypervisor, Storage and Mail.

God Views remain the Source of Truth for each workflow's authority, transport,
durability and failure semantics. The proto registry owns only the canonical
wire schema referenced by those workflow documents; it must not become a
standalone architecture authority that overrides a God View.

## Goals

1. Give every protobuf message one canonical source owned by one workflow.
2. Make producer and all consumers generate bindings from that source.
3. Separate shared transport messages from producer-local or Zone-local state.
4. Make ownership, consumers, transport and God View references discoverable.
5. Prevent duplicate fully-qualified message definitions and incompatible
   schema evolution in CI.
6. Preserve deployed wire compatibility throughout the refactor.

## Non-goals

- No HTTP, Redis, Kafka, NATS or gRPC workflow behavior changes.
- No field renumbering, field reuse, type change or removal of reserved fields.
- No bulk rename of existing protobuf packages or generated language APIs.
- No generic envelope that absorbs workflow-specific command, result or journal
  semantics.
- No merging of personal and tenant contracts merely because their fields look
  similar.
- No committed generated Rust bindings and no `.proto` source inside service
  subprojects.

## Canonical layout

Canonical sources use this path convention:

```text
proto/<domain>/<workflow>/v<major>/<contract>.proto
```

The first two path components identify semantic ownership. They never identify
a generated language or a consumer service.

```text
proto/
├── iam/
│   ├── session/v1/session.proto
│   ├── device_presence/v1/device_presence.proto
│   └── wallet_provision/
│       ├── personal/v1/requested.proto
│       └── tenant/v1/requested.proto
├── hierarchy/
│   └── zone_catalog/v1/zone_catalog.proto
├── hypervisor/
│   ├── vm_lifecycle/v1/{command,result}.proto
│   ├── image_lifecycle/v1/{command,result}.proto
│   ├── allocation/v1/event.proto
│   ├── metering/v1/{accepted_usage,report}.proto
│   └── zone_journal/v1/vm_delete_journal.proto
├── storage/
│   ├── bucket_lifecycle/v1/{command,result}.proto
│   ├── access_session/v1/access.proto
│   ├── commercial_admission/v1/changed.proto
│   └── metering/v1/{usage,report}.proto
├── mail/
│   ├── consumer_runtime/v1/{command,result}.proto
│   ├── template_projection/v1/event.proto
│   ├── dispatch/v1/envelope.proto
│   ├── commercial_admission/v1/changed.proto
│   └── metering/v1/accepted_usage.proto
├── billing/
│   ├── admission/v1/commercial_admission.proto
│   ├── ownership/v1/resource_ownership.proto
│   └── pricing/<module>/v1/*.proto
├── transport/job/v1/{command,result,dead_letter}.proto
├── security/job_payload/v1/protected_payload.proto
├── notification/<workflow>/v1/*.proto
├── managed_service/<workflow>/v1/*.proto
├── zone_control/<workflow>/v1/*.proto
├── registry.yaml
└── README.md
```

Existing protobuf `package` and `go_package` declarations remain unchanged in
the source-move waves. Path and package may temporarily differ. New contracts
use `aurora.<domain>.<workflow>.v1`; existing packages migrate only through a
separate versioned rollout because gRPC method names, generated APIs and Any
type URLs depend on package identity even when ordinary protobuf bytes do not.

## Registry manifest

`proto/registry.yaml` is machine-readable metadata, not a new runtime contract.
Each entry contains:

```yaml
contracts:
  - source: iam/device_presence/v1/device_presence.proto
    owner_workflow: iam.device_presence_projection
    god_views:
      - god_view/iam/device_presence_projection_god_view_workflow.md
    producers:
      - acr
      - controlplane
    consumers:
      - acr
      - controlplane
    transports:
      - shared-redis-pubsub
    compatibility: append-only-v1
```

The manifest must point to concrete workflows and concrete services. Generic
owners such as `platform`, `shared` or `common` are forbidden. One source may
contain several messages only when they share the same owner, transport and
failure boundary.

## Contract classification and consolidation

### Class A: duplicate source for one workflow

These become one canonical file immediately:

| Current sources | Canonical source |
|---|---|
| `acr/device_presence.proto`, `controlplane/iam/device_presence.proto` | `iam/device_presence/v1/device_presence.proto` |
| `acr/device.proto`, session messages in `controlplane/iam/session.proto` | `iam/session/v1/session.proto` |
| overlapping ACR and Controlplane Zone catalogue messages | `hierarchy/zone_catalog/v1/zone_catalog.proto` |

Language-specific options remain on the canonical source. Rust consumers ignore
`go_package`; Go generation continues to target the existing generated package.

### Class B: shared intersection plus owner-local extensions

Hypervisor, Storage, Mail and job-result sources currently repeat the shared
intersection while Dataplane also contains Zone-local messages. The shared
command/result messages move to their workflow canonical files. Zone-local
journals and execution receipts move to explicit `zone_journal` or Zone-local
workflow files and are not compiled by unrelated Central consumers.

Splitting a source file may introduce protobuf imports, but the fully-qualified
message names, field numbers and generated language package remain unchanged.

### Class C: similar shape but different workflows

These remain separate:

- personal and tenant wallet provisioning;
- module-specific commercial-admission projections;
- module-specific metering reports;
- command, result, projection and journal records with different settlement or
  recovery semantics.

Syntax similarity is not sufficient evidence for shared ownership.

### Class D: already bounded service-owned workflows

Versioned Cost API pricing contracts, notification contracts, transfer tickets
and other nonduplicated sources move only for path consistency. They are not
redesigned during this refactor.

## Build and generation model

Every service owns an explicit source list. Builds must not glob all files under
`proto/`.

- Rust `build.rs` files compile canonical root sources using `proto/` as the
  include root and continue generating into Cargo `OUT_DIR`.
- Go generation writes `.pb.go` files into the existing module transport package
  selected by `go_package`.
- Producer and consumer of one contract reference the same source path.
- Generated bindings may coexist temporarily during a wave, but the old source
  is removed only after every consumer builds from the canonical source.
- Docker build contexts must preserve the root sibling layout required by the
  explicit proto paths.

## Compatibility gates

Wave 0 records the current descriptor set before any move. CI then enforces:

1. `buf lint` for registry sources.
2. `buf breaking` against the accepted main-branch descriptor baseline.
3. A registry check that every source exists and every God View reference exists.
4. A descriptor check that a fully-qualified message or service is defined by
   exactly one canonical source.
5. A repository check rejecting `.proto` files outside the root registry.
6. Per-language generated-binding freshness where generated Go code is committed.
7. Fixed Go/Rust cross-language fixtures for protected job payloads and shared
   command/result contracts.

File relocation alone is allowed when full names and wire declarations remain
stable. Compatibility evidence compares semantic descriptors rather than
requiring the old source filename to remain part of the descriptor identity.

## Migration waves

Each wave is independently buildable, reviewable and reversible.

### Wave 0 — governance and baseline

- Add `registry.yaml`, Buf configuration and validation scripts.
- Inventory every current source, owner workflow, God View, producer and
  consumer.
- Capture the descriptor baseline without moving sources.

### Wave 1 — IAM and Hierarchy edge contracts

- Consolidate device presence, session revoke and Zone catalogue duplicates.
- Update ACR and Controlplane build inputs.
- Regenerate Go bindings and run ACR/IAM/Hierarchy boundary tests.

### Wave 2 — outer job transport

- Canonicalize command, result and dead-letter envelopes.
- Separate Dataplane-only completion receipt if it is not part of JO settlement.
- Update JO and Dataplane generation together.

### Wave 3 — Hypervisor

- Split VM lifecycle, image lifecycle, allocation/metering and Zone journal.
- Update Controlplane, JO, Dataplane and Cost consumers.

### Wave 4 — Storage

- Split bucket lifecycle, access session, admission and metering contracts.
- Update Controlplane, JO, Dataplane and Cost consumers.

### Wave 5 — Mail

- Split consumer runtime, templates, dispatch, admission and metering contracts.
- Update Controlplane, JO, Dataplane and Cost consumers.

### Wave 6 — remaining bounded owners

- Move Billing, Notification, Managed Service and Zone Control sources for path
  consistency without changing their workflow contracts.
- Remove obsolete service/consumer directories after the registry reports no
  remaining references.

### Separate future rollout — package normalization

Package names such as `iam.rpc`, `iamproto`, `hypervisor`, `storage` and `zone`
are not renamed in waves 0–6. A future v2 design may introduce consistent
`aurora.<domain>.<workflow>.v2` packages with dual-read/dual-publish or gRPC
service migration as required by each workflow.

## Testing and verification

For every moved contract:

1. Build the descriptor set and compare message/service full names, field
   numbers, field types, cardinality, oneofs, enums and reserved declarations.
2. Build every producer and consumer named by the manifest.
3. Run workflow behavior tests from the associated God Views, including poison
   input, stale event, retry/replay and settlement behavior where applicable.
4. Encode a fixed fixture with one language and decode it with every other
   consumer language.
5. Verify Docker builds that use the production build context.
6. Update God View source-path references in the same wave. A source move does
   not silently change workflow semantics.

## Rollout and rollback

Source moves preserve wire bytes, so runtime rollout order is not used as a
compatibility mechanism. Each wave must be safe under mixed old/new binaries
before deployment.

The old source remains until all consumers compile from the canonical source in
the same change set. Rollback restores build-source paths and generated bindings;
it never rewrites persisted Kafka/Redis/PostgreSQL payloads. Any wave whose
descriptor comparison changes a wire contract is stopped and redesigned as a
versioned workflow migration.

## Success criteria

- Every `.proto` source is reachable from `registry.yaml`.
- Every registry entry names one workflow owner and at least one God View.
- No duplicate fully-qualified message/service definitions exist.
- No producer/consumer maintains a local source copy.
- All current services build and all relevant workflow tests pass.
- Existing deployed payloads decode with bindings generated after the refactor.
- Package normalization remains explicitly out of scope until a separate v2
  migration is approved.
