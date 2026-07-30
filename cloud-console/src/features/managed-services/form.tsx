"use client";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { localizedText } from "@/features/managed-services/api";
import type { FormDraftValue, FormInputField, FormUIField } from "@/features/managed-services/model";

export function ManagedServiceContractField({
  input,
  ui,
  locale,
  value,
  onChange,
}: {
  input: FormInputField;
  ui: FormUIField;
  locale: string;
  value: FormDraftValue | undefined;
  onChange: (value: FormDraftValue) => void;
}) {
  const id = `managed-service-${input.key}`;
  const label = localizedText(ui.label_i18n, locale) || input.key;
  const help = ui.help_i18n ? localizedText(ui.help_i18n, locale) : "";
  const placeholder = ui.placeholder_i18n ? localizedText(ui.placeholder_i18n, locale) : "";
  const required = input.required;

  let control;
  if (input.cardinality !== "ONE" && ui.widget === "MULTI_SELECT") {
    control = (
      <select
        id={id}
        multiple
        value={Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : []}
        required={required}
        onChange={(event) => onChange(Array.from(event.currentTarget.selectedOptions, (option) => option.value))}
        className="flex min-h-24 w-full rounded-md border border-input bg-background px-3 py-2 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        {(input.enum_values ?? []).map((option) => <option key={option} value={option}>{option}</option>)}
      </select>
    );
  } else if (input.cardinality !== "ONE") {
    control = (
      <Input
        id={id}
        value={Array.isArray(value) ? value.join(", ") : ""}
        placeholder={placeholder || "value-a, value-b"}
        required={required}
        onChange={(event) => {
          const items = event.target.value.split(",").map((item) => item.trim()).filter(Boolean);
          if (input.value_type === "INT64" || input.value_type === "DECIMAL" || input.value_type === "PORT") {
            onChange(items.map((item) => Number(item)));
            return;
          }
          if (input.value_type === "BOOLEAN") {
            onChange(items.map((item) => item.toLowerCase() === "true" ? true : item.toLowerCase() === "false" ? false : item));
            return;
          }
          onChange(items);
        }}
      />
    );
  } else if (ui.widget === "SWITCH") {
    control = (
      <label htmlFor={id} className="flex h-9 items-center gap-2 rounded-md border border-input px-3 text-sm">
        <input id={id} type="checkbox" checked={value === true} onChange={(event) => onChange(event.target.checked)} />
        {value === true ? "Enabled" : "Disabled"}
      </label>
    );
  } else if (ui.widget === "SELECT") {
    control = (
      <select
        id={id}
        value={typeof value === "string" ? value : ""}
        required={required}
        onChange={(event) => onChange(event.target.value)}
        className="flex h-9 w-full rounded-md border border-input bg-background px-3 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <option value="">Select…</option>
        {(input.enum_values ?? []).map((option) => <option key={option} value={option}>{option}</option>)}
      </select>
    );
  } else if (ui.widget === "RADIO") {
    control = (
      <div id={id} className="flex flex-wrap gap-3">
        {(input.enum_values ?? []).map((option) => (
          <label key={option} className="flex items-center gap-1.5 text-sm">
            <input type="radio" name={id} value={option} checked={value === option} required={required} onChange={() => onChange(option)} />
            {option}
          </label>
        ))}
      </div>
    );
  } else if (ui.widget === "TEXTAREA") {
    control = (
      <Textarea
        id={id}
        value={typeof value === "string" ? value : ""}
        placeholder={placeholder}
        minLength={input.min_length}
        maxLength={input.max_length ?? 4096}
        required={required}
        rows={4}
        onChange={(event) => onChange(event.target.value)}
      />
    );
  } else if (ui.widget === "NUMBER") {
    control = (
      <Input
        id={id}
        type="number"
        value={typeof value === "number" ? value : ""}
        min={input.min}
        max={input.max}
        required={required}
        onChange={(event) => onChange(event.target.value === "" ? "" : Number(event.target.value))}
      />
    );
  } else {
    control = (
      <Input
        id={id}
        value={typeof value === "string" ? value : ""}
        placeholder={placeholder}
        minLength={input.min_length}
        maxLength={input.max_length ?? 4096}
        required={required}
        onChange={(event) => onChange(event.target.value)}
      />
    );
  }

  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}{required ? " *" : ""}</Label>
      {control}
      {help ? <p className="text-[11px] text-muted-foreground">{help}</p> : null}
    </div>
  );
}
