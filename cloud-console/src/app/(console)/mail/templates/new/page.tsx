"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, ArrowLeft, CheckCircle2, Eye, FileCode2, Info, Loader2, Sparkles } from "lucide-react";

import RouteGuard from "@/components/route-guard";
import { createMailTemplate } from "@/lib/api/mail";

const PARAM_GRAMMAR_REGEX = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/;
const PLACEHOLDER_FIND_REGEX = /\{\{\s*([^}]+?)\s*\}\}/g;

type ValidationResult = {
  validKeys: string[];
  invalidKeys: string[];
};

function parseAndValidatePlaceholders(text: string): ValidationResult {
  const matches = Array.from(text.matchAll(PLACEHOLDER_FIND_REGEX));
  const validSet = new Set<string>();
  const invalidSet = new Set<string>();

  for (const match of matches) {
    const rawKey = match[1].trim();
    if (PARAM_GRAMMAR_REGEX.test(rawKey)) {
      validSet.add(rawKey);
    } else {
      invalidSet.add(rawKey);
    }
  }

  return {
    validKeys: Array.from(validSet).sort(),
    invalidKeys: Array.from(invalidSet).sort(),
  };
}

function NewTemplateContent() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [subjectTemplate, setSubjectTemplate] = useState("Hello {{name}}, your order {{order_id}} is confirmed!");
  const [htmlTemplate, setHtmlTemplate] = useState(
    `<div style="font-family: sans-serif; padding: 20px;">\n  <h2>Welcome {{name}}!</h2>\n  <p>Thank you for your purchase of <strong>{{amount}}</strong>.</p>\n</div>`
  );
  const [sampleJSON, setSampleJSON] = useState(
    JSON.stringify({ name: "Alice", order_id: "ORD-2026", amount: "$150.00" }, null, 2)
  );
  const [activeTab, setActiveTab] = useState<"edit" | "preview">("edit");
  const [errorMsg, setErrorMsg] = useState("");

  const validation = useMemo(() => {
    const combined = `${subjectTemplate} ${htmlTemplate}`;
    return parseAndValidatePlaceholders(combined);
  }, [subjectTemplate, htmlTemplate]);

  // Preview rendering
  const testPreview = useMemo(() => {
    try {
      const parsedParams = JSON.parse(sampleJSON || "{}") as Record<string, unknown>;
      let renderedSubject = subjectTemplate;
      let renderedHTML = htmlTemplate;

      for (const key of validation.validKeys) {
        const rawVal = parsedParams[key] !== undefined ? String(parsedParams[key]) : `[MISSING: {{${key}}}]`;
        const escapedVal = rawVal.replace(/[&<>"']/g, (c) => {
          switch (c) {
            case "&": return "&amp;";
            case "<": return "&lt;";
            case ">": return "&gt;";
            case '"': return "&quot;";
            case "'": return "&#39;";
            default: return c;
          }
        });
        const regex = new RegExp(`\\{\\{\\s*${key}\\s*\\}\\}`, "g");
        renderedSubject = renderedSubject.replace(regex, rawVal);
        renderedHTML = renderedHTML.replace(regex, escapedVal);
      }

      return {
        subject: renderedSubject,
        html: renderedHTML,
        validJSON: true,
        error: "",
      };
    } catch (err) {
      return {
        subject: subjectTemplate,
        html: htmlTemplate,
        validJSON: false,
        error: err instanceof Error ? err.message : "Invalid JSON syntax",
      };
    }
  }, [subjectTemplate, htmlTemplate, sampleJSON, validation.validKeys]);

  const saveMutation = useMutation({
    mutationFn: async () => {
      return createMailTemplate({
        code: code.trim(),
        name: name.trim(),
        subject_template: subjectTemplate,
        html_template: htmlTemplate,
      });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["mail"] });
      router.push("/mail/templates");
    },
    onError: (err: Error) => {
      setErrorMsg(err.message);
    },
  });

  return (
    <div className="flex min-h-[calc(100vh-110px)] w-full flex-col gap-6 px-6 pb-10 text-foreground">
      <header className="flex flex-col gap-3 border-b border-border pb-5 md:flex-row md:items-end md:justify-between">
        <div className="flex items-start gap-3">
          <Link
            href="/mail/templates"
            className="flex size-10 items-center justify-center rounded-lg border border-border bg-muted/20 text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-all mt-0.5"
          >
            <ArrowLeft className="size-5" />
          </Link>
          <div>
            <div className="flex items-center gap-2">
              <FileCode2 className="size-4 text-purple-500" />
              <h1 className="text-xl font-bold tracking-tight">Create Email Template</h1>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              Define a canonical email template version with automatic placeholder grammar validation.
            </p>
          </div>
        </div>
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => setActiveTab(activeTab === "edit" ? "preview" : "edit")}
            className="flex items-center gap-1.5 rounded-lg border border-border bg-muted/30 px-3.5 py-2 text-xs font-semibold hover:bg-muted/60 transition-all"
          >
            <Eye className="size-4 text-purple-400" />
            <span>{activeTab === "edit" ? "Test Preview" : "Edit Source"}</span>
          </button>
          <button
            type="button"
            disabled={!code.trim() || !name.trim() || saveMutation.isPending}
            onClick={() => saveMutation.mutate()}
            className="flex items-center gap-1.5 rounded-lg bg-purple-600 px-4 py-2 text-xs font-semibold text-white hover:bg-purple-500 disabled:opacity-50 transition-all shadow-xs"
          >
            {saveMutation.isPending ? <Loader2 className="size-4 animate-spin" /> : <Sparkles className="size-4" />}
            <span>Publish Template</span>
          </button>
        </div>
      </header>

      {errorMsg && (
        <div className="flex items-center gap-3 rounded-lg border border-red-500/20 bg-red-500/10 p-4 text-xs font-medium text-red-500">
          <AlertCircle className="size-4 shrink-0" />
          <span>{errorMsg}</span>
        </div>
      )}

      {activeTab === "edit" ? (
        <div className="grid gap-6 lg:grid-cols-3">
          {/* Main Template Form */}
          <div className="lg:col-span-2 space-y-5 rounded-xl border border-border bg-card p-6 shadow-xs">
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider block mb-1.5">
                  Template Code <span className="text-red-400">*</span>
                </label>
                <input
                  type="text"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  placeholder="e.g. order_confirmation"
                  className="w-full rounded-lg border border-border bg-background px-3.5 py-2 text-sm focus:outline-hidden focus:ring-2 focus:ring-purple-500/40"
                />
              </div>
              <div>
                <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider block mb-1.5">
                  Template Name <span className="text-red-400">*</span>
                </label>
                <input
                  type="text"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="e.g. Order Confirmation Email"
                  className="w-full rounded-lg border border-border bg-background px-3.5 py-2 text-sm focus:outline-hidden focus:ring-2 focus:ring-purple-500/40"
                />
              </div>
            </div>

            <div>
              <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider block mb-1.5">
                Subject Template
              </label>
              <input
                type="text"
                value={subjectTemplate}
                onChange={(e) => setSubjectTemplate(e.target.value)}
                className="w-full rounded-lg border border-border bg-background px-3.5 py-2 text-sm font-mono focus:outline-hidden focus:ring-2 focus:ring-purple-500/40"
              />
            </div>

            <div>
              <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider block mb-1.5">
                HTML Body Template
              </label>
              <textarea
                rows={12}
                value={htmlTemplate}
                onChange={(e) => setHtmlTemplate(e.target.value)}
                className="w-full rounded-lg border border-border bg-background p-3.5 text-xs font-mono focus:outline-hidden focus:ring-2 focus:ring-purple-500/40"
              />
            </div>
          </div>

          {/* Validation & Grammar Panel */}
          <div className="space-y-5">
            <div className="rounded-xl border border-border bg-card p-5 shadow-xs">
              <h3 className="text-xs font-bold text-muted-foreground uppercase tracking-wider mb-3 flex items-center gap-1.5">
                <Info className="size-3.5 text-purple-400" />
                Dataplane Grammar Compliance
              </h3>
              <p className="text-xs text-muted-foreground leading-relaxed">
                Placeholders must match <code className="text-purple-400">^[A-Za-z_][A-Za-z0-9_]{"{0,127}"}$</code>.
              </p>

              {/* Invalid Keys Alert */}
              {validation.invalidKeys.length > 0 && (
                <div className="mt-4 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-xs">
                  <div className="font-semibold text-red-500 flex items-center gap-1">
                    <AlertCircle className="size-3.5" /> Invalid Placeholders Found:
                  </div>
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {validation.invalidKeys.map((k) => (
                      <span key={k} className="rounded-md bg-red-500/20 px-2 py-0.5 font-mono text-[11px] text-red-400 border border-red-500/30">
                        {"{{" + k + "}}"}
                      </span>
                    ))}
                  </div>
                  <p className="mt-2 text-[11px] text-red-400/90">
                    Dot notation, hyphens, and functions are disallowed.
                  </p>
                </div>
              )}

              {/* Valid Keys List */}
              <div className="mt-4">
                <div className="text-xs font-semibold text-foreground flex items-center gap-1 mb-2">
                  <CheckCircle2 className="size-3.5 text-emerald-500" /> Extracted Valid Keys:
                </div>
                {validation.validKeys.length > 0 ? (
                  <div className="flex flex-wrap gap-1.5">
                    {validation.validKeys.map((k) => (
                      <span key={k} className="rounded-md bg-purple-500/10 px-2 py-0.5 font-mono text-[11px] text-purple-400 border border-purple-500/20">
                        {"{{" + k + "}}"}
                      </span>
                    ))}
                  </div>
                ) : (
                  <span className="text-xs text-muted-foreground italic">No placeholders detected.</span>
                )}
              </div>
            </div>

            {/* Quick Test JSON Input */}
            <div className="rounded-xl border border-border bg-card p-5 shadow-xs">
              <h3 className="text-xs font-bold text-muted-foreground uppercase tracking-wider mb-2">
                Sample Test JSON
              </h3>
              <textarea
                rows={6}
                value={sampleJSON}
                onChange={(e) => setSampleJSON(e.target.value)}
                className="w-full rounded-lg border border-border bg-background p-3 text-xs font-mono focus:outline-hidden focus:ring-2 focus:ring-purple-500/40"
              />
              <button
                type="button"
                onClick={() => setActiveTab("preview")}
                className="mt-3 flex w-full items-center justify-center gap-1.5 rounded-lg border border-purple-500/30 bg-purple-500/10 py-2 text-xs font-semibold text-purple-400 hover:bg-purple-500/20 transition-all"
              >
                <Eye className="size-3.5" />
                <span>Switch to Live Preview</span>
              </button>
            </div>
          </div>
        </div>
      ) : (
        /* Live Test Preview Tab */
        <div className="grid gap-6 lg:grid-cols-2">
          <div className="space-y-4 rounded-xl border border-border bg-card p-6 shadow-xs">
            <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wider">
              Rendered Email Subject
            </h2>
            <div className="rounded-lg border border-border/70 bg-background p-3.5 text-sm font-medium">
              {testPreview.subject}
            </div>

            <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wider pt-3">
              Rendered HTML Body Output
            </h2>
            <iframe
              title="Template Live Preview"
              srcDoc={`<!DOCTYPE html><html><head><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data: https:;"></head><body style="font-family: sans-serif; margin: 0; padding: 16px; background-color: #ffffff; color: #0f172a;">${testPreview.html}</body></html>`}
              sandbox=""
              className="w-full min-h-[320px] rounded-lg border border-border/70 bg-white"
            />
          </div>

          <div className="space-y-5 rounded-xl border border-border bg-card p-6 shadow-xs">
            <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wider">
              Test JSON Parameters
            </h2>
            <textarea
              rows={10}
              value={sampleJSON}
              onChange={(e) => setSampleJSON(e.target.value)}
              className="w-full rounded-lg border border-border bg-background p-3.5 text-xs font-mono focus:outline-hidden focus:ring-2 focus:ring-purple-500/40"
            />
            {!testPreview.validJSON && (
              <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-xs text-red-400">
                JSON Syntax Error: {testPreview.error}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

export default function NewTemplatePage() {
  return (
    <RouteGuard requiredKey="email:template" requiredAction="create">
      <NewTemplateContent />
    </RouteGuard>
  );
}
