export type LocalizedText = Record<string, string>;

export type CatalogDisplay = {
  id: string;
  code: string;
  name_i18n: LocalizedText;
  description_i18n: LocalizedText;
  icon_key: string;
};

export type CatalogVersionDisplay = CatalogDisplay & {
  display_version: string;
};

export type CatalogRevision = {
  id: string;
  number: number;
  contract_version: "platform-form/v1";
  contract_sha256: string;
};

export type ManagedServiceCatalogItem = {
  category: CatalogDisplay;
  definition: CatalogDisplay;
  version: CatalogVersionDisplay;
  revision: CatalogRevision;
};

export type ManagedServiceCatalogPage = {
  items: ManagedServiceCatalogItem[];
  next_cursor: string;
};

export type FormValueType =
  | "STRING"
  | "BOOLEAN"
  | "INT64"
  | "DECIMAL"
  | "ENUM"
  | "DNS_LABEL"
  | "CIDR"
  | "PORT"
  | "DURATION"
  | "BYTE_SIZE";

export type FormCardinality = "ONE" | "LIST" | "SET";

export type FormInputField = {
  key: string;
  value_type: FormValueType;
  cardinality: FormCardinality;
  required: boolean;
  mutable: boolean;
  enum_values?: string[];
  min?: number;
  max?: number;
  min_length?: number;
  max_length?: number;
  min_items?: number;
  max_items?: number;
};

export type FormWidget =
  | "TEXT"
  | "TEXTAREA"
  | "NUMBER"
  | "SWITCH"
  | "SELECT"
  | "RADIO"
  | "TOKEN_LIST"
  | "MULTI_SELECT";

export type FormUIGroup = {
  key: string;
  order: number;
  label_i18n: LocalizedText;
};

export type FormUIField = {
  key: string;
  group: string;
  order: number;
  widget: FormWidget;
  label_i18n: LocalizedText;
  help_i18n?: LocalizedText;
  placeholder_i18n?: LocalizedText;
};

export type ManagedServiceVersionContract = ManagedServiceCatalogItem & {
  input_schema: { fields: FormInputField[] };
  input_schema_sha256: string;
  ui_schema: { groups: FormUIGroup[]; fields: FormUIField[] };
  ui_schema_sha256: string;
};

export type FormDraftScalar = string | number | boolean;
export type FormDraftValue = FormDraftScalar | FormDraftScalar[];

export type ManagedServiceLifecycleState =
  | "provisioning"
  | "active"
  | "deleting"
  | "deleted"
  | "failed"
  | string;

export type ManagedServiceOperationState =
  | "accepted"
  | "processing"
  | "succeeded"
  | "terminal_failed"
  | string;

export type ManagedServiceOperation = {
  id: string;
  instance_id?: string;
  kind: string;
  state: ManagedServiceOperationState;
  generation: number;
  attempt: number;
  delivery_epoch?: number;
  target_revision_id?: string;
  blueprint_revision_id?: string;
  retry_of_operation_id?: string | null;
  status_version?: number;
  last_error_code?: string | null;
  last_sanitized_error?: string | null;
  completed_at?: string | null;
  created_at?: string;
  updated_at?: string;
};

export type ManagedServiceInstanceSummary = {
  id: string;
  code: string;
  name: string;
  desired: {
    state: ManagedServiceLifecycleState;
    generation: number;
    active_revision_id?: string | null;
    pending_revision_id?: string | null;
  };
  observed: {
    state: string;
    version: number;
    observed_at?: string | null;
  };
  metadata_version: number;
  latest_operation?: ManagedServiceOperation | null;
  created_at?: string;
  updated_at?: string;
};

export type ManagedServiceNetworkPort = {
  name: string;
  port: number;
  protocol: string;
};

export type ManagedServiceNetworkComponent = {
  component_code: string;
  service_name: string;
  pod_selector?: Record<string, string> | string | null;
  ports: ManagedServiceNetworkPort[];
};

export type ManagedServiceInstance = ManagedServiceInstanceSummary & {
  desired: ManagedServiceInstanceSummary["desired"] & {
    revision_sequence: number;
  };
  observed: ManagedServiceInstanceSummary["observed"] & {
    output?: Record<string, unknown> | null;
  };
  network_contract?: {
    namespace: string;
    components: ManagedServiceNetworkComponent[];
  } | null;
  resize_contract?: ManagedServiceFormContract | null;
};

export type ManagedServiceOperationPage = {
  items: ManagedServiceOperation[];
  next_cursor: string;
};

export type ManagedServiceFormContract = {
  contract_version: "platform-form/v1";
  input_schema: { fields: FormInputField[] };
  input_schema_sha256: string;
  ui_schema: { groups: FormUIGroup[]; fields: FormUIField[] };
  ui_schema_sha256: string;
};
