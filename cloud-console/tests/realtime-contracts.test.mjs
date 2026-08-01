import assert from "node:assert/strict";
import test from "node:test";
import { decodeEvent, dedupeKey } from "../src/realtime/contracts.ts";

test("bucket runtime decoder rejects malformed or oversized soft state", () => {
  assert.equal(decodeEvent("storage.bucket.sizes.sync", { sizes: { bucket: -1 } }), null);
  const oversized = Object.fromEntries(Array.from({ length: 2_001 }, (_, index) => [`b-${index}`, index]));
  assert.equal(decodeEvent("storage.bucket.sizes.sync", { sizes: oversized }), null);
  assert.deepEqual(
    decodeEvent("storage.bucket.sizes.sync", { sizes: { alpha: 12, beta: 0 } }),
    { sizes: { alpha: 12, beta: 0 } },
  );
});

test("mail runtime decoder requires the complete revision-fenced snapshot", () => {
  assert.equal(decodeEvent("mail.consumer.runtime.changed", { consumer_id: "consumer" }), null);
  const payload = {
    consumer_id: "consumer",
    config_version: 3,
    runtime_epoch: "epoch",
    runtime_revision: 7,
    state: "running",
    active_instances: 2,
    consumer_lag: 0,
    error_code: "",
    error_message: "",
    observed_at: "2026-07-26T00:00:00Z",
    expires_at: "2026-07-26T00:00:30Z",
  };
  assert.deepEqual(decodeEvent("mail.consumer.runtime.changed", payload), payload);
  assert.equal(
    dedupeKey("mail.consumer.runtime.changed", payload),
    "mail.consumer.runtime.changed:consumer:epoch:7",
  );
});

test("job notifications require a stable resource or operation identifier", () => {
  assert.equal(decodeEvent("job.notification", { title: "unsafe anonymous event" }), null);
  const payload = { operation_id: "operation-1", title: "Applied" };
  assert.deepEqual(decodeEvent("job.notification", payload), payload);
  assert.equal(dedupeKey("job.notification", payload), "job.notification:operation-1");
});

test("job notification dedupe preserves monotonic status updates", () => {
  const processing = {
    notification_id: "notification-1",
    operation_id: "operation-1",
    status: "PROCESSING",
    status_version: 2,
  };
  const terminal = { ...processing, status: "SUCCESS", status_version: 3 };
  assert.equal(dedupeKey("job.notification", processing), "job.notification:notification-1:2");
  assert.equal(dedupeKey("job.notification", terminal), "job.notification:notification-1:3");
});
