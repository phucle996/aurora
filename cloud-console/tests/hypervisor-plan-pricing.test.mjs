import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { fileURLToPath } from "node:url";

const createScreenPath = fileURLToPath(
  new URL("../src/features/compute/create-screen.tsx", import.meta.url),
);

test("Hypervisor plan cards use published resource plans and never define price authority", async () => {
  const screen = await readFile(createScreenPath, "utf8");
  assert.match(screen, /listHypervisorResourcePlans/);
  assert.match(screen, /const resourcePlans = resourcePlanQuery\.data \?\? \[\]/);
  assert.doesNotMatch(screen, /hypervisorPlanPresets/);

  assert.match(screen, /getHypervisorEstimate/);
  assert.match(screen, /monthly_730_hour_estimate_micro_units/);
  assert.match(screen, /network usage is separate/i);
});

test("VM create submits a fixed profile plus additional disks but never custom CPU or memory", async () => {
  const screen = await readFile(createScreenPath, "utf8");
  const mutateStart = screen.indexOf("mutation.mutate({");
  const mutateEnd = screen.indexOf("});", mutateStart);
  assert.ok(mutateStart >= 0 && mutateEnd > mutateStart);

  const createPayload = screen.slice(mutateStart, mutateEnd);
  assert.match(createPayload, /resource_plan_id: selectedProfile\.plan_id/);
  assert.match(createPayload, /resource_plan_revision_id: selectedProfile\.revision_id/);
  assert.match(createPayload, /additional_disks:/);
  assert.doesNotMatch(createPayload, /cpu_cores|memory_mb|disk_gb|price|currency|estimate/i);
  assert.doesNotMatch(screen, /setCpuCores|setMemoryMB|selectedPlan === "custom"/);
});
