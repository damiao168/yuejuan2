from pathlib import Path
import json,hashlib
r=Path('.'); data=json.loads((r/'reports/platform-audit-2026-10-10/sonar/issues.json').read_text(encoding='utf-8'))['issues']
items=[x for x in data if x['rule'].startswith(('typescript:','javascript:')) and not x['component'].endswith('.html')]
p=r/'reports/sonar-remediation-2026-10-11/frontend-progress.json'
p.write_text(json.dumps({'original_issue_count':len(items),'issues':[{'key':x['key'],'rule':x['rule'],'severity':x['severity'],'file':x['component'].split(':',1)[1],'original_line':x.get('line'),'status':'pending'} for x in items],'files':{},'validations':[]},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
for name in ['scripts/ci/schema-version.mjs','scripts/check-story052-production-deployment.mjs']:
 p=r/name;s=p.read_text(encoding='utf-8');assert '.sort()' in s
 s=s.replace('.sort()', '.sort((left, right) => left.localeCompare(right))');p.write_text(s,encoding='utf-8')
source=(r/'docs/architecture/evidence/extract-frontend-api.mjs').read_text(encoding='utf-8')
source=source.replace("import ts from 'typescript';", "import ts from 'typescript';\nimport { canonicalApiPath } from './frontend-api-paths.mjs';")
source=source.replace("const canonical = url => url.split('?')[0].replace(/([^/])\\{dynamic\\}$/, '$1').replace(/\\{[^}]+\\}/g, '{}');", "const canonical = canonicalApiPath;")
source=source.replace("'\\\\$&'", "String.raw`\$&`")
start=source.index('  function pathValue(node, depth = 0) {');end=source.index('  visit(source);',start)
replacement='''  function templatePathValue(node, depth) {
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
'''
source=source[:start]+replacement+source[end:]
source=source.replace("for (const imp of imports) if (imp.target && !imp.typeOnly) {", "for (const imp of imports) {\n  if (!imp.target || imp.typeOnly) continue;")
source=source.replace("if (seen.has(value)) return; seen.add(value); for (const item of importGraph.get(value) ?? []) reach(item);", "if (seen.has(value)) return;\n    seen.add(value);\n    for (const item of importGraph.get(value) ?? []) reach(item);")
(r/'docs/architecture/tools/frontend-api-scan.mjs').write_text(source,encoding='utf-8')
(r/'docs/architecture/evidence/extract-frontend-api.mjs').write_text("// 两个审计入口共用同一套 AST 规则，避免复核版本与初次盘点行为漂移。\nimport '../tools/frontend-api-scan.mjs';\n",encoding='utf-8')
(r/'docs/architecture/tools/extract-frontend-api-recheck.mjs').write_text("// 重跑仍输出 evidence/api-catalog.json；只盘点源码，不执行业务接口。\nimport './frontend-api-scan.mjs';\n",encoding='utf-8')
