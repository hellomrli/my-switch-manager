// Run with: node --test internal/server/preview.test.js
// Extract actual production functions; the DOM stub records rendered controls.
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const test = require('node:test');
const source = fs.readFileSync(require('node:path').join(__dirname, 'web', 'app.js'), 'utf8');
function section(start, end) {
  assert.ok(source.includes(start) && source.includes(end));
  return source.slice(source.indexOf(start), source.indexOf(end));
}
function context(plan = []) {
  const nodes = [];
  const ctx = {
    S: { plan, planToken: 'token', planEpoch: 0, planOpen: new Set(), planSel: new Set(['game']), state: { config: {} } },
    MODE_LABEL: { rename: 'rename' },
    h: (...args) => {
      const node = { args, value: 'keep', appendChild() {}, replaceChildren(...children) { this.children = children; } };
      nodes.push(node); return node;
    },
    icon: () => ({}), guard: fn => fn(), nodes,
    render() {}, toast() {}, confirmDialog: async () => true,
    api: async () => ({ manifest: 'manifest', entries: 1, plan: { moves: null, pathFixes: 1, mediaFixes: 0 } }),
  };
  vm.createContext(ctx);
  vm.runInContext([
    section('function clearPlan()', 'async function loadState()'),
    section('async function makePlan()', 'function undoPanel()'),
    section('function mediaToolPanel()', 'function dbPanel()'),
  ].join('\n'), ctx, { filename: 'production-app.js' });
  return ctx;
}
for (const blocked of ['', 'target blocked']) {
  test(`folder-only/null-moves preview renders, blocked=${!!blocked}`, () => {
    const c = context([{ key: 'game', name: 'Game', mode: 'rename', oldDir: 'old', targetDir: 'new', moves: null, blocked }]);
    c.S.planOpen.add('game');
    vm.runInContext('renderPlan()', c);
    const checkbox = c.nodes.find(n => n.args[0] === 'input.check');
    assert.equal(checkbox.args[1].disabled, !!blocked);
    assert.ok(c.nodes.some(n => n.args.includes('0 个文件')));
  });
}
test('nonempty moves retain file preview', () => {
  const c = context([{ key: 'game', mode: 'rename', oldDir: 'old', targetDir: 'new', moves: [{from:'old/a.nsp',to:'new/b.nsp'}] }]);
  c.S.planOpen.add('game'); vm.runInContext('renderPlan()',c);
  assert.ok(c.nodes.some(n => n.args.includes('→ b.nsp')));
});
test('XML-only media preview enables apply without moves', async () => {
  const c = context(); vm.runInContext('mediaToolPanel()', c);
  await c.nodes.find(n => n.args[0] === 'button.btn' && n.args[2] === '预览').args[1].onclick();
  const apply = c.nodes.find(n => n.args[0] === 'button.btn.primary');
  assert.equal(!!apply.args[1].disabled, false);
});
test('makePlan captures header token and apply sends that exact token', async () => {
  const c = context();
  c.api = async (method,path,body,raw,onResponse) => {
    onResponse({headers:{get: () => 'preview-token'}});
    return [{key:'game',moves:null}];
  };
  await vm.runInContext('makePlan()',c);
  assert.equal(c.S.planToken,'preview-token');
  assert.equal(c.S.plan[0].moves.length,0);
  let submitted;
  c.startJob = async (method,path,body) => { submitted = body; };
  await vm.runInContext('applyPlan("none")',c);
  assert.equal(submitted.token,'preview-token');
  assert.deepEqual(Array.from(submitted.keys),['game']);
});
test('late preview response cannot revive invalidated preview', async () => {
  const c = context(); let resolve;
  c.api = () => new Promise(r => { resolve = r; });
  const pending = vm.runInContext('makePlan()',c);
  vm.runInContext('clearPlan()',c);
  resolve([{key:'game',moves:null}]); await pending;
  assert.equal(c.S.plan,null); assert.equal(c.S.planToken,'');
});
test('config/name/scan API requests clear preview before request', async () => {
  for (const path of ['/api/config','/api/names/abc','/api/scan']) {
    const c = context([{key:'game'}]);
    c.fetch = async () => ({ok:true,text:async ()=>'{}'});
    vm.runInContext(section('async function api(', 'function toast('),c);
    await vm.runInContext(`api('POST',${JSON.stringify(path)}, {})`,c);
    assert.equal(c.S.plan,null); assert.equal(c.S.planToken,''); assert.equal(c.S.planSel.size,0);
  }
});
