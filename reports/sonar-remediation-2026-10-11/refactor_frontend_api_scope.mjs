// 只迁移盘点器的文件上下文；用 TypeScript AST 避开字符串、注释和对象属性名。
import fs from 'node:fs';
import ts from 'typescript';

const file = 'docs/architecture/tools/frontend-api-scan.mjs';
const text = fs.readFileSync(file, 'utf8');
const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
const loop = source.statements.find(node => ts.isForOfStatement(node) && node.expression.getText(source) === 'sourceMap');
if (!loop || !ts.isBlock(loop.statement)) throw new Error('Original per-file scanner loop is missing');
const declarations = loop.statement.statements.filter(ts.isFunctionDeclaration);
if (declarations.length !== 15) throw new Error(`Unexpected helper count: ${declarations.length}`);
const helpers = new Set(declarations.map(node => node.name.text));
const locals = new Set(['file', 'source', 'functions', 'sdkNames', 'constants']);

function promotedFunction(declaration) {
  const edits = [{start: declaration.parameters.pos, end: declaration.parameters.pos, value: 'context, '}];
  function visit(node) {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && helpers.has(node.expression.text)) {
      const opening = text.indexOf('(', node.expression.end);
      edits.push({start: opening + 1, end: opening + 1, value: 'context, '});
    }
    if (ts.isIdentifier(node)) {
      const parent = node.parent;
      const isName = (ts.isFunctionDeclaration(parent) || ts.isParameter(parent)) && parent.name === node;
      const isProperty = (ts.isPropertyAssignment(parent) || ts.isPropertyAccessExpression(parent)) && parent.name === node;
      if (!isName && !isProperty && locals.has(node.text)) {
        edits.push({start: node.getStart(source), end: node.end, value: `context.${node.text}`});
      } else if (!isName && helpers.has(node.text) && !(ts.isCallExpression(parent) && parent.expression === node)) {
        if (node.text !== 'collect' && node.text !== 'visit') throw new Error(`Unexpected helper reference: ${node.text}`);
        edits.push({start: node.getStart(source), end: node.end, value: `(child) => ${node.text}(context, child)`});
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(declaration);
  const start = declaration.getStart(source);
  let rewritten = text.slice(start, declaration.end);
  for (const edit of edits.sort((left, right) => right.start - left.start)) {
    rewritten = rewritten.slice(0, edit.start - start) + edit.value + rewritten.slice(edit.end - start);
  }
  // 原循环中的函数已有两空格缩进；迁出后只调整行首缩进，保留运行字符串。
  return rewritten.replaceAll('\n  ', '\n');
}

const moved = declarations.map(promotedFunction).join('\n\n');
const perFile = `// 常量、局部函数与 SDK 实例名只属于当前文件；递归展开不能串用另一个文件的符号表。
for (const [file, source] of sourceMap) {
  const context = { file, source, functions: new Map(), sdkNames: new Set(), constants: new Map() };
  collect(context, source);
  visit(context, source);
}`;
const rewritten = text.slice(0, loop.getStart(source)) + moved + '\n\n' + perFile + text.slice(loop.end);
const parsed = ts.createSourceFile(file, rewritten, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
if (parsed.parseDiagnostics.length) throw new Error('Refactored scanner contains syntax errors');
fs.writeFileSync(file, rewritten);
console.log(JSON.stringify({moved_functions: declarations.length, preserved_file_context: [...locals]}));
