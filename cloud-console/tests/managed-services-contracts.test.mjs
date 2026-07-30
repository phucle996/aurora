import assert from "node:assert/strict";
import test from "node:test";

import {
  decodeManagedServiceVersionContract,
  localizedText,
} from "../src/features/managed-services/contract.ts";

const id = "10000000-0000-4000-8000-000000000033";
const hash = "00".repeat(32);

function contractFixture() {
  return {
    category: { id, code: "messaging", name_i18n: { en: "Messaging", vi: "Tin nhắn" }, description_i18n: {}, icon_key: "boxes" },
    definition: { id, code: "apache-kafka", name_i18n: { en: "Apache Kafka" }, description_i18n: {}, icon_key: "boxes" },
    version: { id, code: "3-8", display_version: "3.8", name_i18n: { en: "Kafka 3.8" }, description_i18n: {}, icon_key: "boxes" },
    revision: { id, number: 3, contract_version: "platform-form/v1", contract_sha256: hash },
    input_schema: {
      fields: [
        { key: "replicas", value_type: "INT64", cardinality: "ONE", required: true, mutable: true, min: 1, max: 100 },
        { key: "exposure", value_type: "ENUM", cardinality: "SET", required: true, mutable: true, enum_values: ["private", "public"], min_items: 1, max_items: 2 },
      ],
    },
    input_schema_sha256: hash,
    ui_schema: {
      groups: [{ key: "capacity", order: 10, label_i18n: { en: "Capacity" } }],
      fields: [
        { key: "replicas", group: "capacity", order: 10, widget: "NUMBER", label_i18n: { en: "Replicas" } },
        { key: "exposure", group: "capacity", order: 20, widget: "MULTI_SELECT", label_i18n: { en: "Exposure" } },
      ],
    },
    ui_schema_sha256: hash,
  };
}

test("managed service decoder accepts the finite type/cardinality contract", () => {
  const decoded = decodeManagedServiceVersionContract(contractFixture());
  assert.equal(decoded.revision.number, 3);
  assert.equal(decoded.input_schema.fields[1].cardinality, "SET");
  assert.equal(decoded.ui_schema.fields[1].widget, "MULTI_SELECT");
});

test("managed service decoder fails closed for unknown and incompatible widgets", () => {
  const unknown = contractFixture();
  unknown.ui_schema.fields[0].widget = "SLIDER";
  assert.throws(() => decodeManagedServiceVersionContract(unknown), /widget/i);

  const incompatible = contractFixture();
  incompatible.ui_schema.fields[0].widget = "TEXT";
  assert.throws(() => decodeManagedServiceVersionContract(incompatible), /incompatible/i);
});

test("managed service decoder rejects malformed ranges and duplicate field keys", () => {
  const malformedRange = contractFixture();
  malformedRange.input_schema.fields[0].min = 101;
  assert.throws(() => decodeManagedServiceVersionContract(malformedRange), /constraint/i);

  const duplicate = contractFixture();
  duplicate.input_schema.fields[1].key = "replicas";
  assert.throws(() => decodeManagedServiceVersionContract(duplicate), /duplicate|missing/i);
});

test("managed service localization uses selected locale then English fallback", () => {
  assert.equal(localizedText({ en: "Messaging", vi: "Tin nhắn" }, "vi"), "Tin nhắn");
  assert.equal(localizedText({ en: "Messaging" }, "fr"), "Messaging");
  assert.equal(localizedText({}, "fr"), "");
});
