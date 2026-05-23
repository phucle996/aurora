"use client";

import PageBreadcrumb from "@/components/common/PageBreadCrumb";
import Label from "@/components/form/Label";
import Input from "@/components/form/input/InputField";
import Button from "@/components/ui/button/Button";
import { createTenant } from "@/lib/api/tenant";
import { useRouter } from "next/navigation";
import React, { useMemo, useState } from "react";

type FormState = {
  name: string;
  code: string;
  primaryDomain: string;
  confirm: boolean;
};

type FieldErrors = {
  name?: string;
  code?: string;
  primaryDomain?: string;
};

const domainRegex = /^(?=.{3,253}$)(?!-)[a-z0-9-]+(\.[a-z0-9-]+)+$/;
const codeRegex = /^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$/;

export default function CreateTenantPage() {
  const router = useRouter();
  const [form, setForm] = useState<FormState>({
    name: "",
    code: "",
    primaryDomain: "",
    confirm: false,
  });
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [submitError, setSubmitError] = useState<string>("");
  const [submitSuccess, setSubmitSuccess] = useState<string>("");
  const [submitting, setSubmitting] = useState(false);

  const canSubmit = useMemo(() => {
    return (
      form.confirm &&
      form.name.trim().length >= 2 &&
      codeRegex.test(form.code.trim()) &&
      domainRegex.test(form.primaryDomain.trim().toLowerCase())
    );
  }, [form]);

  function validate(): boolean {
    const nextErrors: FieldErrors = {};
    if (form.name.trim().length < 2) {
      nextErrors.name = "Tenant name must be at least 2 characters.";
    }
    if (!codeRegex.test(form.code.trim())) {
      nextErrors.code = "Code must be lowercase slug, 3-64 chars, no leading/trailing hyphen.";
    }
    if (!domainRegex.test(form.primaryDomain.trim().toLowerCase())) {
      nextErrors.primaryDomain = "Primary domain is invalid.";
    }
    setFieldErrors(nextErrors);
    return Object.keys(nextErrors).length === 0;
  }

  async function handleSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitError("");
    setSubmitSuccess("");

    if (!validate() || !form.confirm) {
      return;
    }

    setSubmitting(true);
    try {
      const payload = {
        name: form.name.trim(),
        code: form.code.trim(),
        primary_domain: form.primaryDomain.trim().toLowerCase(),
      };
      const result = await createTenant(payload);
      setSubmitSuccess("Tenant created successfully. Redirecting...");
      setTimeout(() => {
        router.push(`/tenants/${result.tenant_id}`);
      }, 700);
    } catch {
      setSubmitError("Unable to create tenant. Please verify tenant code/domain and try again.");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div>
      <PageBreadcrumb pageTitle="Create Tenant" />

      <div className="grid grid-cols-1 gap-6 xl:grid-cols-12">
        <aside className="xl:col-span-4">
          <div className="rounded-2xl border border-gray-200 bg-white p-6 dark:border-gray-800 dark:bg-white/[0.03]">
            <h3 className="text-base font-semibold text-gray-900 dark:text-white">Tenant Setup Policy</h3>
            <ul className="mt-4 space-y-3 text-sm text-gray-600 dark:text-gray-300">
              <li>Primary domain must be unique globally.</li>
              <li>Users will sign in using <strong>username@domain</strong>.</li>
              <li>Default roles are auto-provisioned: <strong>tenant_owner</strong>, <strong>tenant_admin</strong>, <strong>tenant_member</strong>.</li>
              <li>Creator will be linked as tenant owner automatically.</li>
            </ul>
          </div>
        </aside>

        <section className="xl:col-span-8">
          <div className="rounded-2xl border border-gray-200 bg-white p-6 dark:border-gray-800 dark:bg-white/[0.03]">
            <form className="space-y-6" onSubmit={handleSubmit}>
              <div className="grid grid-cols-1 gap-5 md:grid-cols-2">
                <div>
                  <Label htmlFor="tenant-name">Tenant Name</Label>
                  <Input
                    id="tenant-name"
                    name="tenant-name"
                    placeholder="Acme Vietnam"
                    defaultValue={form.name}
                    onChange={(event) => setForm((prev) => ({ ...prev, name: event.target.value }))}
                    error={Boolean(fieldErrors.name)}
                    hint={fieldErrors.name}
                    disabled={submitting}
                  />
                </div>
                <div>
                  <Label htmlFor="tenant-code">Tenant Code</Label>
                  <Input
                    id="tenant-code"
                    name="tenant-code"
                    placeholder="acme-vn"
                    defaultValue={form.code}
                    onChange={(event) =>
                      setForm((prev) => ({ ...prev, code: event.target.value.toLowerCase().trim() }))
                    }
                    error={Boolean(fieldErrors.code)}
                    hint={fieldErrors.code || "Lowercase slug; immutable after creation."}
                    disabled={submitting}
                  />
                </div>
              </div>

              <div>
                <Label htmlFor="primary-domain">Primary Domain</Label>
                <Input
                  id="primary-domain"
                  name="primary-domain"
                  placeholder="acme.cloud.local"
                  defaultValue={form.primaryDomain}
                  onChange={(event) =>
                    setForm((prev) => ({ ...prev, primaryDomain: event.target.value.toLowerCase().trim() }))
                  }
                  error={Boolean(fieldErrors.primaryDomain)}
                  hint={fieldErrors.primaryDomain || "Used for login context: username@domain"}
                  disabled={submitting}
                />
              </div>

              <label className="flex items-start gap-3 rounded-lg border border-gray-200 p-4 text-sm text-gray-700 dark:border-gray-800 dark:text-gray-300">
                <input
                  type="checkbox"
                  checked={form.confirm}
                  onChange={(event) => setForm((prev) => ({ ...prev, confirm: event.target.checked }))}
                  disabled={submitting}
                  className="mt-0.5 h-4 w-4 rounded border-gray-300 text-brand-500 focus:ring-brand-500"
                />
                <span>I confirm the tenant information is correct and ready to provision.</span>
              </label>

              {submitError ? (
                <div className="rounded-lg border border-error-200 bg-error-50 px-4 py-3 text-sm text-error-700 dark:border-error-900/40 dark:bg-error-900/20 dark:text-error-300">
                  {submitError}
                </div>
              ) : null}

              {submitSuccess ? (
                <div className="rounded-lg border border-success-200 bg-success-50 px-4 py-3 text-sm text-success-700 dark:border-success-900/40 dark:bg-success-900/20 dark:text-success-300">
                  {submitSuccess}
                </div>
              ) : null}

              <div className="flex flex-wrap items-center justify-end gap-3">
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => router.push("/")}
                  disabled={submitting}
                >
                  Cancel
                </Button>
                <Button type="submit" disabled={!canSubmit || submitting}>
                  {submitting ? "Creating..." : "Create Tenant"}
                </Button>
              </div>
            </form>
          </div>
        </section>
      </div>
    </div>
  );
}
