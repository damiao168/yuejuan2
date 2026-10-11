import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import { webcrypto } from 'node:crypto';
import { canonicalApiPath } from '../../docs/architecture/tools/frontend-api-paths.mjs';

// 查询字符串、动态资源后缀和未闭合占位符保护静态路由映射边界。
test('canonical API paths preserve literal and dynamic resource semantics', () => {
  for (const [input, expected] of [
    ['/api/exams/{exam}/papers?q=1', '/api/exams/{}/papers'],
    ['/api/exams{dynamic}', '/api/exams'],
    ['/api/{dynamic}', '/api/{}'],
    ['/api/{dynamic}-commands/{id}', '/api/{}-commands/{}'],
    ['/api/{unclosed', '/api/{unclosed'],
    ['/api/{}', '/api/{}'],
    ['/api/{{nested}', '/api/{}']
  ]) assert.equal(canonicalApiPath(input), expected);
});

test('unclosed repeated braces are scanned as literal suffix once', () => {
  const suffix = '{'.repeat(100_000);
  assert.equal(canonicalApiPath(`/api/${suffix}`), `/api/${suffix}`);
});

function fakeGraph() {
  const cells = {};
  for (const [id, x, y, style] of [
    ['a', 0, 0, {}], ['b', 80, 0, {}], ['cross', 40, 0, {}],
    ['background', 40, 0, { dashed: '1', fillColor: '#F7F9FC' }],
    ['__frame', 40, 0, {}], ['border', 40, 7, {}]
  ]) cells[id] = { id, vertex: true, state: { x, y, width: 20, height: 20, style } };
  cells.cross.state.text = { boundingBox: { width: 25, height: 20 } };
  cells.edge = { id: 'edge', edge: true, source: cells.a, target: cells.b, state: { absolutePoints: [{ x: 10, y: 10 }, { x: 90, y: 10 }] } };
  cells.dangling = { id: 'dangling', edge: true, source: cells.a };
  return { container: { style: {} }, view: { getState: cell => cell.state, scaleAndTranslate() {}, validate() {} },
    getModel: () => ({ cells }), refresh() {}, getSvg: () => '<svg/>' };
}

async function renderGeometry(file) {
  const graph = fakeGraph();
  const pageInfo = { getId: () => 'page', getName: () => 'Fixture' };
  const auditUI = { editor: { graph }, pages: [pageInfo], setFileData() {}, selectPage() {} };
  const exports = [];
  const context = { window: { auditUI }, auditUI, document: { fonts: { ready: Promise.resolve() } },
    crypto: webcrypto, TextEncoder, mxUtils: { getXml: value => value } };
  const render = vm.runInNewContext(fs.readFileSync(file, 'utf8'), context, { filename: file });
  const page = { evaluate: (callback, argument) => callback(argument), request: {
    get: async () => ({ json: async () => ({ details: [{ file: 'fixture.drawio' }] }), text: async () => '<mxfile/>' }),
    post: async (url, input) => { exports.push({ url, data: input.data }); return { ok: () => true }; }
  } };
  return { summary: await render(page), exports };
}

// 这是本地几何规则回归；实际 diagrams.net 的导入和浏览器字体渲染仍需单独验证。
test('native geometry retains endpoints, background, border tolerance and dangling edges', async () => {
  const result = await renderGeometry('docs/architecture/tools/render_native.js');
  assert.equal(result.summary.dangling, 1);
  assert.deepEqual(JSON.parse(JSON.stringify(result.summary.overflow)), [{ file: 'fixture.drawio', page: 'Fixture', objects: [{ id: 'cross', width: 25, height: 20, nodeW: 20, nodeH: 20 }] }]);
  assert.deepEqual(JSON.parse(JSON.stringify(result.summary.obstructions)), [{ file: 'fixture.drawio', id: 'page', count: 1 }]);
  const report = JSON.parse(result.exports.at(-1).data);
  assert.deepEqual(report.results[0].obstructions.map(item => item.node), ['cross']);
});
