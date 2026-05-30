import { AlertCircle, CheckCircle, Clock, Zap } from 'lucide-react'

export default function ZoneManagement() {
  return (
    <div className="max-w-4xl mx-auto p-8 space-y-8">
      {/* Header */}
      <div className="space-y-2">
        <h1 className="text-4xl font-bold">Zone Management Runbook</h1>
        <p className="text-lg text-slate-600 dark:text-slate-400">
          Operational guide for managing infrastructure zones in the controlplane
        </p>
      </div>

      {/* Warning Banner */}
      <div className="bg-amber-50 dark:bg-amber-950 border border-amber-200 dark:border-amber-800 rounded-lg p-4 flex gap-3">
        <AlertCircle className="w-5 h-5 text-amber-600 dark:text-amber-400 flex-shrink-0 mt-0.5" />
        <div>
          <p className="font-semibold text-amber-900 dark:text-amber-100">Zone is Root Topology</p>
          <p className="text-sm text-amber-800 dark:text-amber-200 mt-1">
            Zone is the root infrastructure unit. All dataplane nodes, services, and workloads are anchored to a zone. Changes to zone status or services have blast radius across all workloads running on that zone. Always follow the runbook and have a rollback plan.
          </p>
        </div>
      </div>

      {/* Table of Contents */}
      <div className="bg-slate-50 dark:bg-slate-900 rounded-lg p-6 space-y-3">
        <h2 className="font-semibold text-lg">Quick Navigation</h2>
        <ul className="space-y-2 text-sm">
          <li><a href="#create" className="text-blue-600 dark:text-blue-400 hover:underline">1. Creating a New Zone</a></li>
          <li><a href="#status-transitions" className="text-blue-600 dark:text-blue-400 hover:underline">2. Zone Status Transitions</a></li>
          <li><a href="#service-management" className="text-blue-600 dark:text-blue-400 hover:underline">3. Managing Zone Services</a></li>
          <li><a href="#deletion" className="text-blue-600 dark:text-blue-400 hover:underline">4. Deleting a Zone</a></li>
          <li><a href="#troubleshooting" className="text-blue-600 dark:text-blue-400 hover:underline">5. Troubleshooting</a></li>
        </ul>
      </div>

      {/* Section 1: Creating a Zone */}
      <section id="create" className="space-y-4">
        <h2 className="text-2xl font-bold flex items-center gap-2">
          <Zap className="w-6 h-6" />
          Creating a New Zone
        </h2>

        <div className="bg-slate-50 dark:bg-slate-900 rounded-lg p-6 space-y-4">
          <h3 className="font-semibold">Prerequisites</h3>
          <ul className="list-disc list-inside space-y-2 text-sm">
            <li>Admin API key with valid MFA</li>
            <li>Zone code must be unique (e.g., us-east-1, edge-hcm-1)</li>
            <li>Geographic location must be selected from the autocomplete list</li>
            <li>At least one service must be enabled (hypervisor, storage, mail, k8s, or ai)</li>
          </ul>

          <h3 className="font-semibold mt-6">Steps</h3>
          <ol className="list-decimal list-inside space-y-3 text-sm">
            <li>Navigate to Admin UI → Zones → Add Zone</li>
            <li>Fill in zone details:
              <ul className="list-disc list-inside ml-4 mt-2 space-y-1">
                <li><strong>Zone Name:</strong> Human-readable name (e.g., "US East 1")</li>
                <li><strong>Zone Code:</strong> Unique identifier (e.g., "us-east-1") — auto-slugified</li>
                <li><strong>Location:</strong> Geographic location from autocomplete</li>
              </ul>
            </li>
            <li>Select enabled services:
              <ul className="list-disc list-inside ml-4 mt-2 space-y-1">
                <li><strong>Hypervisor:</strong> VM/container orchestration</li>
                <li><strong>Storage:</strong> Block/object storage</li>
                <li><strong>Mail:</strong> Email service</li>
                <li><strong>Kubernetes:</strong> K8s cluster</li>
                <li><strong>AI:</strong> AI/ML workloads</li>
              </ul>
            </li>
            <li>Click "Create Zone"</li>
            <li>Zone is created with status <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded text-sm">planned</code></li>
          </ol>

          <div className="bg-blue-50 dark:bg-blue-950 border border-blue-200 dark:border-blue-800 rounded p-3 mt-4">
            <p className="text-sm text-blue-900 dark:text-blue-100">
              <strong>Note:</strong> New zones always start in <code className="bg-blue-200 dark:bg-blue-900 px-1 rounded">planned</code> status. Status cannot be set during creation. Transition to <code className="bg-blue-200 dark:bg-blue-900 px-1 rounded">active</code> after dataplane registration.
            </p>
          </div>
        </div>
      </section>

      {/* Section 2: Status Transitions */}
      <section id="status-transitions" className="space-y-4">
        <h2 className="text-2xl font-bold flex items-center gap-2">
          <Clock className="w-6 h-6" />
          Zone Status Transitions
        </h2>

        <div className="bg-slate-50 dark:bg-slate-900 rounded-lg p-6 space-y-4">
          <h3 className="font-semibold">State Machine</h3>
          <div className="bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded p-4 font-mono text-sm overflow-x-auto">
            <pre>{`planned ──→ active ──→ draining ──→ maintenance ──→ active
   └──→ disabled ←──────────────────────────────┘
        ↑
        └─────────────────────────────────────────┘`}</pre>
          </div>

          <h3 className="font-semibold mt-6">Transition Rules</h3>
          <div className="space-y-3 text-sm">
            <div className="border-l-4 border-blue-500 pl-4">
              <p className="font-semibold">planned → active</p>
              <p className="text-slate-600 dark:text-slate-400">Zone is ready to serve traffic. Dataplane must be registered first.</p>
            </div>
            <div className="border-l-4 border-blue-500 pl-4">
              <p className="font-semibold">active → draining</p>
              <p className="text-slate-600 dark:text-slate-400">Gracefully drain workloads. No new workloads accepted, existing ones continue.</p>
            </div>
            <div className="border-l-4 border-blue-500 pl-4">
              <p className="font-semibold">active → maintenance</p>
              <p className="text-slate-600 dark:text-slate-400">Enter maintenance mode. Zone services can be updated. No traffic accepted.</p>
            </div>
            <div className="border-l-4 border-red-500 pl-4">
              <p className="font-semibold">any → disabled</p>
              <p className="text-slate-600 dark:text-slate-400">Disable zone. All workloads must be migrated away first.</p>
            </div>
          </div>

          <h3 className="font-semibold mt-6">How to Transition</h3>
          <ol className="list-decimal list-inside space-y-2 text-sm">
            <li>Navigate to Admin UI → Zones → Select zone</li>
            <li>Click "Update Status"</li>
            <li>Select target status from dropdown</li>
            <li>Confirm transition</li>
            <li>Monitor zone status in real-time</li>
          </ol>
        </div>
      </section>

      {/* Section 3: Service Management */}
      <section id="service-management" className="space-y-4">
        <h2 className="text-2xl font-bold flex items-center gap-2">
          <CheckCircle className="w-6 h-6" />
          Managing Zone Services
        </h2>

        <div className="bg-slate-50 dark:bg-slate-900 rounded-lg p-6 space-y-4">
          <h3 className="font-semibold">When Can Services Be Updated?</h3>
          <p className="text-sm text-slate-600 dark:text-slate-400">
            Zone services can only be updated when the zone is in <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">planned</code> or <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">maintenance</code> status. This prevents accidental service configuration changes while the zone is actively serving traffic.
          </p>

          <h3 className="font-semibold mt-6">Workflow: Enable/Disable a Service</h3>
          <ol className="list-decimal list-inside space-y-3 text-sm">
            <li>If zone is <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">active</code>, transition to <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">maintenance</code>
              <ul className="list-disc list-inside ml-4 mt-2">
                <li>Drain all workloads from the zone</li>
                <li>Update status: active → maintenance</li>
              </ul>
            </li>
            <li>Navigate to Admin UI → Zones → Select zone → Services</li>
            <li>Toggle service enabled/disabled</li>
            <li>Confirm changes</li>
            <li>Transition zone back to <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">active</code> when ready</li>
          </ol>

          <div className="bg-red-50 dark:bg-red-950 border border-red-200 dark:border-red-800 rounded p-3 mt-4">
            <p className="text-sm text-red-900 dark:text-red-100">
              <strong>Warning:</strong> Disabling a service while workloads are running on it will cause service disruption. Always drain workloads first.
            </p>
          </div>
        </div>
      </section>

      {/* Section 4: Deletion */}
      <section id="deletion" className="space-y-4">
        <h2 className="text-2xl font-bold flex items-center gap-2">
          <AlertCircle className="w-6 h-6" />
          Deleting a Zone
        </h2>

        <div className="bg-slate-50 dark:bg-slate-900 rounded-lg p-6 space-y-4">
          <h3 className="font-semibold">Deletion Preconditions (All 3 Required)</h3>
          <div className="space-y-2 text-sm">
            <div className="flex gap-2">
              <span className="text-red-600 dark:text-red-400 font-bold">1.</span>
              <div>
                <p className="font-semibold">Zone status must be <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">disabled</code></p>
                <p className="text-slate-600 dark:text-slate-400">Transition zone through the state machine to disabled status</p>
              </div>
            </div>
            <div className="flex gap-2">
              <span className="text-red-600 dark:text-red-400 font-bold">2.</span>
              <div>
                <p className="font-semibold">No dataplane nodes attached</p>
                <p className="text-slate-600 dark:text-slate-400">Deregister all dataplane clusters from the zone</p>
              </div>
            </div>
            <div className="flex gap-2">
              <span className="text-red-600 dark:text-red-400 font-bold">3.</span>
              <div>
                <p className="font-semibold">No enabled services</p>
                <p className="text-slate-600 dark:text-slate-400">Disable all 5 services (hypervisor, storage, mail, k8s, ai)</p>
              </div>
            </div>
          </div>

          <h3 className="font-semibold mt-6">Deletion Workflow</h3>
          <ol className="list-decimal list-inside space-y-3 text-sm">
            <li>Drain all workloads from the zone</li>
            <li>Deregister dataplane cluster(s)</li>
            <li>Disable all zone services</li>
            <li>Transition zone status to <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">disabled</code></li>
            <li>Navigate to Admin UI → Zones → Select zone</li>
            <li>Click "Delete Zone"</li>
            <li>Confirm deletion (irreversible)</li>
          </ol>

          <div className="bg-red-50 dark:bg-red-950 border border-red-200 dark:border-red-800 rounded p-3 mt-4">
            <p className="text-sm text-red-900 dark:text-red-100">
              <strong>Critical:</strong> Zone deletion is irreversible. Ensure all workloads are migrated and all preconditions are met before proceeding.
            </p>
          </div>
        </div>
      </section>

      {/* Section 5: Troubleshooting */}
      <section id="troubleshooting" className="space-y-4">
        <h2 className="text-2xl font-bold">Troubleshooting</h2>

        <div className="space-y-4">
          <div className="bg-slate-50 dark:bg-slate-900 rounded-lg p-6">
            <h3 className="font-semibold mb-3">Cannot transition zone to active</h3>
            <p className="text-sm text-slate-600 dark:text-slate-400 mb-3">
              <strong>Cause:</strong> Zone is in <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">planned</code> status but dataplane is not registered.
            </p>
            <p className="text-sm text-slate-600 dark:text-slate-400">
              <strong>Solution:</strong> Register dataplane cluster first. Zone can only transition to active after dataplane is ready.
            </p>
          </div>

          <div className="bg-slate-50 dark:bg-slate-900 rounded-lg p-6">
            <h3 className="font-semibold mb-3">Cannot update zone services</h3>
            <p className="text-sm text-slate-600 dark:text-slate-400 mb-3">
              <strong>Cause:</strong> Zone is in <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">active</code>, <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">draining</code>, or <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">disabled</code> status.
            </p>
            <p className="text-sm text-slate-600 dark:text-slate-400">
              <strong>Solution:</strong> Transition zone to <code className="bg-slate-200 dark:bg-slate-800 px-2 py-1 rounded">maintenance</code> status first, then update services.
            </p>
          </div>

          <div className="bg-slate-50 dark:bg-slate-900 rounded-lg p-6">
            <h3 className="font-semibold mb-3">Cannot delete zone</h3>
            <p className="text-sm text-slate-600 dark:text-slate-400 mb-3">
              <strong>Cause:</strong> One or more preconditions not met (status not disabled, dataplane still attached, or services still enabled).
            </p>
            <p className="text-sm text-slate-600 dark:text-slate-400">
              <strong>Solution:</strong> Check all 3 preconditions above. Run through the deletion workflow step-by-step.
            </p>
          </div>
        </div>
      </section>

      {/* Footer */}
      <div className="border-t border-slate-200 dark:border-slate-700 pt-6 text-sm text-slate-600 dark:text-slate-400">
        <p>For additional support, contact the infrastructure team or check the controlplane logs.</p>
      </div>
    </div>
  )
}
