// Run with AURORA_TEST_NODE_MODULES pointing to a test-only installation of jsdom.
// Executes the production component with mocked API calls and the console's React/TypeScript.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const external = process.env.AURORA_TEST_NODE_MODULES;

test('resource plan editor uses scheduled latest revision, paginates, and never retries a conflict automatically',
  { skip: !external && 'AURORA_TEST_NODE_MODULES with jsdom is required' }, async () => {
  const { JSDOM } = require(path.join(external, 'jsdom'));
  const dom = new JSDOM('<div id="root"></div>', { url: 'http://localhost' });
  global.window = dom.window;
  global.document = dom.window.document;
  global.HTMLElement = dom.window.HTMLElement;
  global.IS_REACT_ACT_ENVIRONMENT = true;
  Object.defineProperty(global, 'navigator', { value: dom.window.navigator, configurable: true });
  const React = require('react');
  const { createRoot } = require('react-dom/client');
  const ts = require('typescript');
  let latest = '2';
  let conflict = true;
  const published = [];
  const pages = [];
  const toasts = [];
  const selectedPlan = () => ({ plan_id: 'plan-one', code: 'compute', display_name: 'Compute',
    description: '', state: 'ACTIVE', latest_revision_number: latest, effective_revision_number: '1' });
  const api = {
    async listHypervisorResourcePlans(_limit, _signal, after) {
      pages.push(after);
      return { plans: after ? [{ ...selectedPlan(), plan_id: 'future', display_name: 'Future', effective_revision_number: '0' }] : [selectedPlan()],
        next_cursor: after ? '' : 'plan-one', observed_at: new Date().toISOString() };
    },
    async listHypervisorResourcePlanRevisions(planID) {
      return { revisions: [{ plan_id: planID, revision_id: 'revision-' + latest, revision_number: latest,
        cpu_cores: '4', memory_mib: '8192', boot_disk_gib: '64', effective_from: '2030-09-01T00:00:00Z',
        effective_to: null, state: 'SCHEDULED', change_reason: 'scheduled offer', is_latest: true, is_effective: false }],
        next_cursor: '', observed_at: new Date().toISOString() };
    },
    async publishHypervisorResourcePlanRevision(planID, payload) {
      published.push({ planID, ...payload });
      if (conflict) { latest = '3'; conflict = false; throw new Error('HYPERVISOR_RESOURCE_PLAN_REVISION_CONFLICT'); }
      latest = '4'; return { revision_number: latest };
    },
  };
  const source = fs.readFileSync(path.join(__dirname, '../src/page/pricing-schedules/HypervisorResourcePlansPanel.tsx'), 'utf8');
  const compiled = ts.transpileModule(source, { compilerOptions: {
    jsx: ts.JsxEmit.ReactJSX, module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022,
  } }).outputText;
  const moduleValue = { exports: {} };
  const load = (name) => {
    if (name === '../../lib/api/billing') return { billingApi: api };
    if (name === '../../lib/utils') return { cn: (...parts) => parts.filter(Boolean).join(' ') };
    if (name === 'sonner') return { toast: { error: (value) => toasts.push(value), success: (value) => toasts.push(value) } };
    if (name === 'lucide-react') return { Check: () => null, RefreshCw: () => null };
    return require(name);
  };
  vm.runInNewContext('(function(require,module,exports){' + compiled + '\n})',
    { Date, DOMException, AbortController, console })(load, moduleValue, moduleValue.exports);
  const root = createRoot(document.getElementById('root'));
  const button = (text) => [...document.querySelectorAll('button')].find((item) => item.textContent.includes(text));
  const click = async (element) => { assert.ok(element); await React.act(async () => { element.click(); }); };
  try {
    await React.act(async () => root.render(React.createElement(moduleValue.exports.HypervisorResourcePlansPanel, { canPublish: true })));
    await click(button('Compute'));
    assert.match(document.body.textContent, /r2.*Latest/);
    const reason = [...document.querySelectorAll('label')].find((item) => item.textContent.includes('Change reason')).querySelector('input');
    await React.act(async () => {
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(reason, 'new offer');
      reason.dispatchEvent(new window.Event('input', { bubbles: true }));
    });
    // Selecting the same identity must not clear the editor's OCC token.
    await click(button('Compute'));
    assert.equal(button('Publish revision').disabled, false);
    await click(button('Publish revision'));
    assert.equal(published.length, 1);
    assert.equal(published[0].expected_latest_revision, '2');
    assert.match(toasts.join(' '), /Review the refreshed revision/);
    assert.match(document.body.textContent, /r3.*Latest/);
    await React.act(async () => Promise.resolve());
    assert.equal(published.length, 1, 'conflict must never auto-retry');
    await click(button('Publish revision'));
    assert.equal(published.length, 2);
    assert.equal(published[1].expected_latest_revision, '3');
    await click(button('Next plans'));
    assert.equal(pages.at(-1), 'plan-one');
    assert.match(document.body.textContent, /Future/);
    assert.match(document.body.textContent, /No effective revision yet/);
  } finally {
    await React.act(async () => root.unmount());
    dom.window.close();
    delete global.IS_REACT_ACT_ENVIRONMENT;
  }
});
