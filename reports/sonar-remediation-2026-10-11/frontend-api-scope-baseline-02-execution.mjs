// 架构审计的静态盘点器：只写本目录的证据，不修改接口契约、生成 SDK 或业务源码。
// 模块可达性只能说明页面会导入 API 模块，不能据此声称其中每个接口都会在该页面执行。
import fs from 'node:fs';
import path from 'node:path';
import ts from 'typescript';
import { canonicalApiPath } from "file:///D:/tools/sonar-remediation-20261011/full-checkpoint-02/workspace/docs/architecture/tools/frontend-api-paths.mjs";
import { buildRouteCoverage } from "file:///D:/tools/sonar-remediation-20261011/full-checkpoint-02/workspace/scripts/update-api-route-coverage.mjs";

const root = "D:/tools/sonar-remediation-20261011/full-checkpoint-02/workspace";
const output = path.join(root, 'docs/architecture/evidence');
const relative = value => path.relative(root, value).replaceAll('\\', '/');
function walk(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap(entry => {
    if (['node_modules', 'dist', 'target', '.git'].includes(entry.name)) return [];
    const file = path.join(dir, entry.name);
    return entry.isDirectory() ? walk(file) : [file];
  });
}
const frontendFiles = ['apps/web-admin/src', 'apps/student-portal/src', 'apps/desktop-client/src', 'packages/sdk/src', 'packages/ui/src', 'packages/shared-types/src', 'packages/design-tokens/src']
  .flatMap(dir => walk(path.join(root, dir))).filter(file => /\.(?:tsx?|css)$/.test(file));
const sourceFiles = frontendFiles.filter(file => /\.(?:tsx?|js)$/.test(file) && !/\.(?:test|spec)\./.test(file) && !file.includes(`${path.sep}test${path.sep}`));
const sourceMap = new Map(sourceFiles.map(file => [relative(file), ts.createSourceFile(file, fs.readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true, file.endsWith('tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)]));
const spec = JSON.parse(fs.readFileSync(path.join(root, 'services/api-gateway/openapi/edugrade-api.openapi.json'), 'utf8'));
const routeCoverage = buildRouteCoverage({ root, gatewayRoot: path.join(root, 'services/api-gateway/internal'), openapiPath: path.join(root, 'services/api-gateway/openapi/edugrade-api.openapi.json'), exceptionsPath: path.join(root, 'contracts/openapi/registered-route-exceptions.json') });
const line = (source, node) => source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1;
const operations = [];
for (const [url, item] of Object.entries(spec.paths)) for (const method of ['get', 'post', 'put', 'patch', 'delete', 'head', 'options']) {
  if (item[method]) operations.push({ method: method.toUpperCase(), path: url, operationId: item[method].operationId, tags: item[method].tags ?? [] });
}
const operationMap = new Map(operations.map(op => [op.operationId, op]));
const canonical = canonicalApiPath;
const matches = (method, url) => {
  const pattern = canonical(url).split('{}').map(part => part.replace(/[.*+?^${}()|[\]\\]/g, String.raw`\$&`)).join('[^/]+');
  const matcher = new RegExp(`^${pattern}$`);
  return routeCoverage.routes.filter(route => route.method === method && matcher.test(route.path));
};
const calls = [];
const imports = [];
function resolveImport(file, value) {
  let base;
  if (value.startsWith('.')) base = path.resolve(root, path.dirname(file), value);
  else if (value.startsWith('@edugrade/')) base = path.join(root, 'packages', value.slice('@edugrade/'.length), 'src/index');
  else return null;
  for (const suffix of ['', '.ts', '.tsx', '/index.ts', '/index.tsx']) {
    const candidate = relative(base + suffix);
    if (sourceMap.has(candidate)) return candidate;
  }
  return null;
}
for (const [file, source] of sourceMap) {
  const functions = new Map();
  const sdkNames = new Set();
  const constants = new Map();
  function collect(node) {
    if (ts.isFunctionDeclaration(node) && node.name) functions.set(node.name.text, node);
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.initializer) {
      constants.set(node.name.text, node.initializer);
      if (ts.isNewExpression(node.initializer) && node.initializer.expression.getText(source) === 'EduGradeApi') sdkNames.add(node.name.text);
    }
    ts.forEachChild(node, collect);
  }
  collect(source);
  function templatePathValue(node, depth) {
    let value = node.head.text;
    for (const span of node.templateSpans) value += (pathValue(span.expression, depth + 1) ?? '{dynamic}') + span.literal.text;
    return value;
  }
  function binaryPathValue(node, depth) {
    const left = pathValue(node.left, depth + 1), right = pathValue(node.right, depth + 1);
    return left || right ? (left ?? '{dynamic}') + (right ?? '{dynamic}') : null;
  }
  function returnedPathValue(node, depth) {
    let result;
    function find(n) {
      if (ts.isReturnStatement(n) && n.expression && result === undefined) {
        result = pathValue(n.expression, depth + 1);
      } else {
        ts.forEachChild(n, find);
      }
    }
    find(functions.get(node.expression.text).body);
    return result ?? null;
  }
  function callPathValue(node, depth) {
    if (['buildQueryString', 'studentReleaseQuery'].includes(node.expression.getText(source))) return '';
    if (ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 'url') return pathValue(node.arguments[0], depth + 1);
    if (ts.isIdentifier(node.expression) && functions.has(node.expression.text)) return returnedPathValue(node, depth);
    return null;
  }
  function pathValue(node, depth = 0) {
    // 只展开有限层的常量和函数返回值；未知动态部分保持占位，不推断运行时访问权限。
    if (!node || depth > 5) return null;
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text;
    if (ts.isTemplateExpression(node)) return templatePathValue(node, depth);
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) return binaryPathValue(node, depth);
    if (ts.isIdentifier(node)) return constants.has(node.text) ? pathValue(constants.get(node.text), depth + 1) : null;
    if (ts.isCallExpression(node)) return callPathValue(node, depth);
    return null;
  }
  function recordCall(node, kind, method, url, extra) {
    calls.push({ source: file, line: line(source, node), kind, method, path: url, ...extra,
      matches: matches(method, url).map(route => `${route.method} ${route.path}`), expression: node.getText(source).slice(0, 600) });
  }
  function collectImageResource(node) {
    if (!ts.isReturnStatement(node) || !node.expression) return;
    let owner = node.parent;
    while (owner && !ts.isFunctionDeclaration(owner)) owner = owner.parent;
    if (!owner?.name?.text.endsWith('URL')) return;
    const value = pathValue(node.expression);
    if (!value?.includes('/api/')) return;
    recordCall(node, 'image-resource-url', 'GET', value.slice(value.indexOf('/api/')));
  }
  function collectImports(node) {
    if (ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier)) {
      imports.push({ source: file, target: resolveImport(file, node.moduleSpecifier.text), specifier: node.moduleSpecifier.text,
        line: line(source, node), typeOnly: node.importClause?.phaseModifier === ts.SyntaxKind.TypeKeyword });
    }
    if (ts.isCallExpression(node) && node.expression.getText(source) === 'import' && node.arguments[0] && ts.isStringLiteral(node.arguments[0])) {
      imports.push({ source: file, target: resolveImport(file, node.arguments[0].text), specifier: node.arguments[0].text, line: line(source, node), lazy: true });
    }
  }
  function explicitMethod(init, fallback) {
    if (!init || !ts.isObjectLiteralExpression(init)) return fallback;
    const property = init.properties.find(prop => ts.isPropertyAssignment(prop) && prop.name.getText(source) === 'method');
    if (!property) return fallback;
    return ts.isStringLiteral(property.initializer) ? property.initializer.text.toUpperCase() : 'DYNAMIC';
  }
  function resourceMethod(node, fallback) {
    const owner = node.parent;
    if (!ts.isCallExpression(owner)) return fallback;
    if (owner.expression.getText(source).endsWith('.open') && ts.isStringLiteral(owner.arguments[0])) return owner.arguments[0].text.toUpperCase();
    if (owner.expression.getText(source) !== 'fetch') return fallback;
    const method = explicitMethod(owner.arguments[1], fallback);
    // 原盘点器不将外层 fetch 的动态 method 覆盖到资源 URL；继续保留内层默认值。
    return method === 'DYNAMIC' ? fallback : method;
  }
  function isDirectTransport(expression) {
    if (ts.isPropertyAccessExpression(expression)) return ['request', 'requestBlob', 'requestEventStream', 'requestStream', 'url'].includes(expression.name.text);
    return ts.isIdentifier(expression) && ['request', 'fetch'].includes(expression.text);
  }
  function collectDirectCall(node) {
    const value = pathValue(node.arguments[0]);
    if (!value?.includes('/api/') && value !== '/health') return;
    let method = explicitMethod(node.arguments[1], 'GET');
    if (ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 'url') method = resourceMethod(node, method);
    const url = value.slice(Math.max(0, value.indexOf('/api/')));
    recordCall(node, 'direct-transport', method, url);
  }
  function collectCalls(node) {
    if (!ts.isCallExpression(node)) return;
    if (ts.isPropertyAccessExpression(node.expression) && sdkNames.has(node.expression.expression.getText(source))) {
      const operation = operationMap.get(node.expression.name.text);
      calls.push({ source: file, line: line(source, node), kind: 'generated-sdk', operationId: node.expression.name.text,
        ...operation, matches: operation ? matches(operation.method, operation.path).map(route => `${route.method} ${route.path}`) : [], expression: node.getText(source).slice(0, 600) });
      return;
    }
    if (isDirectTransport(node.expression)) collectDirectCall(node);
  }
  function visit(node) {
    collectImageResource(node);
    collectImports(node);
    collectCalls(node);
    ts.forEachChild(node, visit);
  }
  visit(source);
}
const routingSource = sourceMap.get('apps/web-admin/src/router/routes.tsx');
function literal(node) {
  if (!node) return undefined;
  if (ts.isStringLiteral(node)) return node.text;
  if (node.kind === ts.SyntaxKind.TrueKeyword) return true;
  if (node.kind === ts.SyntaxKind.FalseKeyword) return false;
  if (ts.isArrayLiteralExpression(node)) return node.elements.map(literal).filter(value => value !== undefined);
  if (ts.isObjectLiteralExpression(node)) return Object.fromEntries(node.properties.filter(ts.isPropertyAssignment).map(prop => [prop.name.getText(routingSource), literal(prop.initializer)]).filter(([,value]) => value !== undefined));
  return undefined;
}
const frontendRoutes = [];
function visitRoute(node) {
  if (ts.isVariableDeclaration(node) && ['productionRouteDefinitions', 'mockRouteDefinitions'].includes(node.name.getText(routingSource))) {
    for (const item of node.initializer.elements) frontendRoutes.push({ ...literal(item), source: 'apps/web-admin/src/router/routes.tsx', line: line(routingSource, item), registry: node.name.getText(routingSource) });
  }
  ts.forEachChild(node, visitRoute);
}
visitRoute(routingSource);
const importGraph = new Map();
for (const imp of imports) {
  if (!imp.target || imp.typeOnly) continue;
  if (!importGraph.has(imp.source)) importGraph.set(imp.source, []);
  importGraph.get(imp.source).push(imp.target);
}
const apiModules = new Set(calls.map(call => call.source));
const pageReachability = [];
for (const file of sourceMap.keys()) if (/(?:Page|App)\.tsx$/.test(file)) {
  const seen = new Set();
  function reach(value) { if (seen.has(value)) return;
    seen.add(value);
    for (const item of importGraph.get(value) ?? []) reach(item); }
  reach(file);
  const modules = [...seen].filter(value => apiModules.has(value));
  pageReachability.push({ page: file, reachableApiModules: modules, interpretation: 'Static import reachability; does not prove execution of every function in these modules.' });
}
const backendRoutes = routeCoverage.routes.map(route => {
  const content = fs.readFileSync(path.join(root, route.source), 'utf8');
  const token = `"${route.method} ${route.path}"`;
  const pos = content.indexOf(token);
  const sourceLine = pos < 0 ? null : content.slice(0, pos).split('\n').length;
  const registration = pos < 0 ? '' : content.slice(content.lastIndexOf('\n', pos) + 1, content.indexOf('\n', pos));
  return { ...route, line: sourceLine, registration, frontendCallers: calls.filter(call => call.matches.includes(`${route.method} ${route.path}`)).map(({source,line,kind}) => ({source,line,kind})) };
});
const summary = {
  frontend_source_inventory: frontendFiles.length,
  non_test_ts_source_files: sourceMap.size,
  production_frontend_routes: frontendRoutes.filter(route => !route.mock).length,
  mock_frontend_routes: frontendRoutes.filter(route => route.mock).length,
  registered_backend_routes: backendRoutes.length,
  registered_with_openapi: backendRoutes.filter(route => route.coverage === 'openapi').length,
  registered_exception_routes: backendRoutes.filter(route => route.coverage !== 'openapi').length,
  openapi_operations: operations.length,
  openapi_parameter_name_aliases: operations.filter(op => !routeCoverage.routes.some(route => route.method === op.method && route.path === op.path) && matches(op.method, op.path).length).length,
  generated_sdk_unique_used_operations: new Set(calls.filter(call => call.kind === 'generated-sdk' && call.operationId).map(call => call.operationId)).size,
  frontend_call_sites: calls.length,
  frontend_matched_unique_registered_routes: backendRoutes.filter(route => route.frontendCallers.length).length,
  frontend_matched_openapi_routes: backendRoutes.filter(route => route.frontendCallers.length && route.coverage === 'openapi').length,
  frontend_matched_exception_routes: backendRoutes.filter(route => route.frontendCallers.length && route.coverage !== 'openapi').length,
  unresolved_static_call_sites: calls.filter(call => !call.matches.length).length,
};
const parameterNameAliases = operations.filter(op => !routeCoverage.routes.some(route => route.method === op.method && route.path === op.path) && matches(op.method, op.path).length).map(op => ({ ...op, registered_equivalents: matches(op.method, op.path).map(route => `${route.method} ${route.path}`) }));
const supplementalRegistrations = [];
for (const file of walk(path.join(root, 'services/api-gateway/internal')).filter(file => file.endsWith('.go') && !file.endsWith('_test.go'))) {
  const content = fs.readFileSync(file, 'utf8');
  for (const match of content.matchAll(/\bmux\.HandleFunc\("(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) ([^" ]+)"/g)) supplementalRegistrations.push({ method: match[1], path: match[2], source: relative(file), line: content.slice(0,match.index).split('\n').length, interpretation: 'HandleFunc registration outside repository route-coverage scanner.' });
}
fs.writeFileSync(path.join(output, 'api-catalog.json'), JSON.stringify({ schema_version: 1, generated_at: '2026-10-10', method: 'Recomputed Go literal Handle registrations + TypeScript AST call/import scan. Parameter names are normalized only for mapping; repository OpenAPI debt remains exact-string based. Conditional runtime activation and handlers require separate review.', summary, frontendRoutes, backendRoutes, supplementalRegistrations, parameterNameAliases, frontendCalls: calls, pageReachability, importEdges: imports, unregisteredOpenapi: operations.filter(op => !matches(op.method, op.path).length) }, null, 2) + '\n');
console.log(JSON.stringify(summary, null, 2));
console.log('Unresolved call sites:');
for (const call of calls.filter(call => !call.matches.length)) console.log(`${call.source}:${call.line} ${call.method} ${call.path} ${call.operationId ?? ''}`);
