import type {
  CatalogDisplay,
  CatalogRevision,
  CatalogVersionDisplay,
  FormCardinality,
  FormInputField,
  FormUIField,
  FormUIGroup,
  FormValueType,
  FormWidget,
  LocalizedText,
  ManagedServiceCatalogItem,
  ManagedServiceFormContract,
  ManagedServiceVersionContract,
} from "./model";

function record(value: unknown): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("Managed Service catalog returned an invalid contract.");
  }
  return value as Record<string, unknown>;
}

function localized(value: unknown): LocalizedText {
  const source = record(value);
  if (Object.keys(source).length > 16) throw new Error("Managed Service localization is invalid.");
  const output: LocalizedText = {};
  for (const [locale, text] of Object.entries(source)) {
    if (locale.length === 0 || locale.length > 16 || typeof text !== "string" || text.length > 4096) {
      throw new Error("Managed Service localization is invalid.");
    }
    output[locale] = text;
  }
  return output;
}

function display(value: unknown): CatalogDisplay {
  const source = record(value);
  if (typeof source.id !== "string" || typeof source.code !== "string" || typeof source.icon_key !== "string") {
    throw new Error("Managed Service display metadata is invalid.");
  }
  return {
    id: source.id,
    code: source.code,
    name_i18n: localized(source.name_i18n),
    description_i18n: localized(source.description_i18n),
    icon_key: source.icon_key,
  };
}

function versionDisplay(value: unknown): CatalogVersionDisplay {
  const source = record(value);
  if (typeof source.display_version !== "string") throw new Error("Managed Service version metadata is invalid.");
  return { ...display(source), display_version: source.display_version };
}

function revision(value: unknown): CatalogRevision {
  const source = record(value);
  if (
    typeof source.id !== "string" ||
    typeof source.number !== "number" ||
    !Number.isSafeInteger(source.number) ||
    source.contract_version !== "platform-form/v1" ||
    typeof source.contract_sha256 !== "string" ||
    !/^[a-f0-9]{64}$/.test(source.contract_sha256)
  ) {
    throw new Error("This Managed Service form contract is not supported.");
  }
  return {
    id: source.id,
    number: source.number,
    contract_version: "platform-form/v1",
    contract_sha256: source.contract_sha256,
  };
}

export function decodeManagedServiceCatalogItem(value: unknown): ManagedServiceCatalogItem {
  const source = record(value);
  return {
    category: display(source.category),
    definition: display(source.definition),
    version: versionDisplay(source.version),
    revision: revision(source.revision),
  };
}

const valueTypes = new Set<FormValueType>(["STRING", "BOOLEAN", "INT64", "DECIMAL", "ENUM", "DNS_LABEL", "CIDR", "PORT", "DURATION", "BYTE_SIZE"]);
const cardinalities = new Set<FormCardinality>(["ONE", "LIST", "SET"]);
const widgets = new Set<FormWidget>(["TEXT", "TEXTAREA", "NUMBER", "SWITCH", "SELECT", "RADIO", "TOKEN_LIST", "MULTI_SELECT"]);

function inputField(value: unknown): FormInputField {
  const source = record(value);
  const allowedProperties = new Set(["key", "value_type", "cardinality", "required", "mutable", "enum_values", "min", "max", "min_length", "max_length", "min_items", "max_items"]);
  if (
    Object.keys(source).some((key) => !allowedProperties.has(key)) ||
    typeof source.key !== "string" ||
    !/^[a-z][a-z0-9_]{0,63}$/.test(source.key) ||
    typeof source.value_type !== "string" ||
    !valueTypes.has(source.value_type as FormValueType) ||
    typeof source.cardinality !== "string" ||
    !cardinalities.has(source.cardinality as FormCardinality) ||
    typeof source.required !== "boolean" ||
    typeof source.mutable !== "boolean"
  ) {
    throw new Error("This Managed Service input contract is not supported.");
  }
  const enumValues = source.enum_values;
  if (
    (source.value_type === "ENUM" && (!Array.isArray(enumValues) || enumValues.length === 0 || enumValues.length > 128)) ||
    (source.value_type !== "ENUM" && enumValues !== undefined) ||
    (Array.isArray(enumValues) && (
      !enumValues.every((item) => typeof item === "string" && item.length > 0 && item.length <= 4096) ||
      new Set(enumValues).size !== enumValues.length
    ))
  ) {
    throw new Error("This Managed Service enum contract is not supported.");
  }
  const numericConstraintAllowed = source.value_type === "INT64" || source.value_type === "DECIMAL" || source.value_type === "PORT";
  const min = source.min;
  const max = source.max;
  if (
    (min !== undefined && (typeof min !== "number" || !Number.isFinite(min) || !numericConstraintAllowed)) ||
    (max !== undefined && (typeof max !== "number" || !Number.isFinite(max) || !numericConstraintAllowed)) ||
    (typeof min === "number" && typeof max === "number" && min > max) ||
    ((source.value_type === "INT64" || source.value_type === "PORT") && (
      (typeof min === "number" && !Number.isSafeInteger(min)) ||
      (typeof max === "number" && !Number.isSafeInteger(max))
    )) ||
    (source.value_type === "PORT" && (
      (typeof min === "number" && (min < 1 || min > 65535)) ||
      (typeof max === "number" && (max < 1 || max > 65535))
    ))
  ) {
    throw new Error("This Managed Service numeric constraint is not supported.");
  }
  const lengthConstraintAllowed = ["STRING", "ENUM", "DNS_LABEL", "CIDR", "DURATION", "BYTE_SIZE"].includes(source.value_type as string);
  const minLength = source.min_length;
  const maxLength = source.max_length;
  if (
    (minLength !== undefined && (!Number.isSafeInteger(minLength) || (minLength as number) < 0 || (minLength as number) > 4096 || !lengthConstraintAllowed)) ||
    (maxLength !== undefined && (!Number.isSafeInteger(maxLength) || (maxLength as number) < 0 || (maxLength as number) > 4096 || !lengthConstraintAllowed)) ||
    (typeof minLength === "number" && typeof maxLength === "number" && minLength > maxLength)
  ) {
    throw new Error("This Managed Service length constraint is not supported.");
  }
  const minItems = source.min_items;
  const maxItems = source.max_items;
  if (
    (minItems !== undefined && (!Number.isSafeInteger(minItems) || (minItems as number) < 0 || (minItems as number) > 64 || source.cardinality === "ONE")) ||
    (maxItems !== undefined && (!Number.isSafeInteger(maxItems) || (maxItems as number) < 0 || (maxItems as number) > 64 || source.cardinality === "ONE")) ||
    (typeof minItems === "number" && typeof maxItems === "number" && minItems > maxItems)
  ) {
    throw new Error("This Managed Service collection constraint is not supported.");
  }
  return {
    key: source.key,
    value_type: source.value_type as FormValueType,
    cardinality: source.cardinality as FormCardinality,
    required: source.required,
    mutable: source.mutable,
    enum_values: enumValues as string[] | undefined,
    min: min as number | undefined,
    max: max as number | undefined,
    min_length: minLength as number | undefined,
    max_length: maxLength as number | undefined,
    min_items: minItems as number | undefined,
    max_items: maxItems as number | undefined,
  };
}

function uiGroup(value: unknown): FormUIGroup {
  const source = record(value);
  if (
    Object.keys(source).some((key) => key !== "key" && key !== "order" && key !== "label_i18n") ||
    typeof source.key !== "string" ||
    !/^[a-z][a-z0-9_]{0,63}$/.test(source.key) ||
    !Number.isSafeInteger(source.order) ||
    (source.order as number) < 0
  ) {
    throw new Error("This Managed Service UI group is not supported.");
  }
  const label = localized(source.label_i18n);
  if (!label.en?.trim() || label.en.length > 160) throw new Error("This Managed Service UI group is not supported.");
  return { key: source.key, order: source.order as number, label_i18n: label };
}

function uiField(value: unknown): FormUIField {
  const source = record(value);
  const allowedProperties = new Set(["key", "group", "order", "widget", "label_i18n", "help_i18n", "placeholder_i18n"]);
  if (
    Object.keys(source).some((key) => !allowedProperties.has(key)) ||
    typeof source.key !== "string" ||
    !/^[a-z][a-z0-9_]{0,63}$/.test(source.key) ||
    typeof source.group !== "string" ||
    !/^[a-z][a-z0-9_]{0,63}$/.test(source.group) ||
    !Number.isSafeInteger(source.order) ||
    (source.order as number) < 0 ||
    typeof source.widget !== "string" ||
    !widgets.has(source.widget as FormWidget)
  ) {
    throw new Error("This Managed Service UI widget is not supported.");
  }
  const label = localized(source.label_i18n);
  if (!label.en?.trim() || label.en.length > 160) throw new Error("This Managed Service UI widget is not supported.");
  return {
    key: source.key,
    group: source.group,
    order: source.order as number,
    widget: source.widget as FormWidget,
    label_i18n: label,
    help_i18n: source.help_i18n === undefined ? undefined : localized(source.help_i18n),
    placeholder_i18n: source.placeholder_i18n === undefined ? undefined : localized(source.placeholder_i18n),
  };
}

export function decodeManagedServiceFormContract(value: unknown): ManagedServiceFormContract {
  const source = record(value);
  if (source.contract_version !== undefined && source.contract_version !== "platform-form/v1") {
    throw new Error("This Managed Service form contract is not supported.");
  }
  const inputSchema = record(source.input_schema);
  const uiSchema = record(source.ui_schema);
  if (
    Object.keys(inputSchema).length !== 1 ||
    Object.keys(uiSchema).length !== 2 ||
    !Array.isArray(inputSchema.fields) ||
    !Array.isArray(uiSchema.groups) ||
    !Array.isArray(uiSchema.fields) ||
    inputSchema.fields.length > 64 ||
    uiSchema.groups.length > 32
  ) {
    throw new Error("This Managed Service form contract is not supported.");
  }
  const inputFields = inputSchema.fields.map(inputField);
  const uiGroups = uiSchema.groups.map(uiGroup);
  const uiFields = uiSchema.fields.map(uiField);
  const inputKeys = new Set(inputFields.map((field) => field.key));
  const groupKeys = new Set(uiGroups.map((group) => group.key));
  if (
    inputKeys.size !== inputFields.length ||
    groupKeys.size !== uiGroups.length ||
    new Set(uiFields.map((field) => field.key)).size !== uiFields.length ||
    uiFields.length !== inputFields.length
  ) {
    throw new Error("This Managed Service UI contract contains duplicate or missing fields.");
  }
  for (const field of uiFields) {
    if (!inputKeys.has(field.key) || !groupKeys.has(field.group)) {
      throw new Error("This Managed Service UI contract references an unknown field or group.");
    }
    const input = inputFields.find((candidate) => candidate.key === field.key)!;
    let compatible = false;
    if (input.cardinality !== "ONE") {
      compatible = field.widget === "TOKEN_LIST" || (input.value_type === "ENUM" && field.widget === "MULTI_SELECT");
    } else if (input.value_type === "BOOLEAN") {
      compatible = field.widget === "SWITCH";
    } else if (input.value_type === "INT64" || input.value_type === "DECIMAL" || input.value_type === "PORT") {
      compatible = field.widget === "NUMBER";
    } else if (input.value_type === "ENUM") {
      compatible = field.widget === "SELECT" || field.widget === "RADIO";
    } else if (input.value_type === "STRING") {
      compatible = field.widget === "TEXT" || field.widget === "TEXTAREA";
    } else {
      compatible = field.widget === "TEXT";
    }
    // A stale or corrupted transport contract must become non-actionable; the
    // browser never guesses a coercion that the future mutation path may hash.
    if (!compatible) throw new Error("This Managed Service UI widget is incompatible with its input field.");
  }
  if (
    typeof source.input_schema_sha256 !== "string" ||
    !/^[a-f0-9]{64}$/.test(source.input_schema_sha256) ||
    typeof source.ui_schema_sha256 !== "string" ||
    !/^[a-f0-9]{64}$/.test(source.ui_schema_sha256)
  ) {
    throw new Error("Managed Service form integrity metadata is missing.");
  }
  return {
    contract_version: "platform-form/v1",
    input_schema: { fields: inputFields },
    input_schema_sha256: source.input_schema_sha256,
    ui_schema: { groups: uiGroups, fields: uiFields },
    ui_schema_sha256: source.ui_schema_sha256,
  };
}

export function decodeManagedServiceVersionContract(value: unknown): ManagedServiceVersionContract {
  const source = record(value);
  const form = decodeManagedServiceFormContract(source);
  const item = decodeManagedServiceCatalogItem(source);
  return { ...item, ...form };
}

export function localizedText(value: LocalizedText, locale: string | undefined): string {
  return (locale && value[locale]) || value.en || "";
}
