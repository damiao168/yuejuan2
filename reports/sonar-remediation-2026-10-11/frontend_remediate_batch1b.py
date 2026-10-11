from pathlib import Path
p=Path('docs/architecture/tools/frontend-api-paths.mjs')
p.write_text('''// 按字符扫描占位符，未闭合花括号不会反复回溯；只归一化盘点路径，不改变真实请求 URL。
export function canonicalApiPath(url) {
  let clean = url.split('?')[0];
  const suffix = '{dynamic}';
  if (clean.endsWith(suffix) && clean.at(-suffix.length - 1) && clean.at(-suffix.length - 1) !== '/') clean = clean.slice(0, -suffix.length);
  const parts = [];
  let cursor = 0;
  while (cursor < clean.length) {
    const open = clean.indexOf('{', cursor);
    if (open < 0) break;
    const close = clean.indexOf('}', open + 1);
    // 没有右括号时剩余部分都是字面量，不能再从后续左括号重复搜索。
    if (close < 0) break;
    parts.push(clean.slice(cursor, open), '{}');
    cursor = close + 1;
  }
  parts.push(clean.slice(cursor));
  return parts.join('');
}
''',encoding='utf-8')
p=Path('docs/architecture/tools/render_native.js');s=p.read_text(encoding='utf-8')
start=s.index('        const vertices = [], edges = [], overflow = [], obstructions = [];');end=s.index('        const svg = ',start)
new='''        const vertices = [], edges = [], overflow = [];
        const model = graph.getModel();
        function collectCell(cell) {
          if (cell.edge) {
            edges.push({ id: cell.id, source: cell.source?.id, target: cell.target?.id });
            return;
          }
          if (!cell.vertex) return;
          vertices.push(cell.id);
          const state = graph.view.getState(cell);
          const box = state?.text?.boundingBox;
          if (!box || cell.id.startsWith("__")) return;
          // 小幅边缘差异属于字体排版误差；明显超过节点才列为人工复查线索。
          if (box.width > state.width + 4 || box.height > state.height + 4) {
            overflow.push({ id: cell.id, width: box.width, height: box.height, nodeW: state.width, nodeH: state.height });
          }
        }
        Object.values(model.cells).forEach(collectCell);
        function obstacleBounds(id, edge) {
          if (id === edge.source || id === edge.target || id.startsWith("__")) return null;
          const state = graph.view.getState(model.cells[id]);
          if (!state || state.style.dashed === "1" && state.style.fillColor === "#F7F9FC") return null;
          return { left: state.x + 3, right: state.x + state.width - 3, top: state.y + 3, bottom: state.y + state.height - 3 };
        }
        function crossesHorizontal(a, b, bounds) {
          return Math.abs(a.y - b.y) < 1 && a.y > bounds.top && a.y < bounds.bottom
            && Math.min(a.x, b.x) < bounds.right && Math.max(a.x, b.x) > bounds.left;
        }
        function crossesVertical(a, b, bounds) {
          return Math.abs(a.x - b.x) < 1 && a.x > bounds.left && a.x < bounds.right
            && Math.min(a.y, b.y) < bounds.bottom && Math.max(a.y, b.y) > bounds.top;
        }
        function crossesBounds(a, b, bounds) {
          if (!a || !b) return false;
          return crossesHorizontal(a, b, bounds) || crossesVertical(a, b, bounds);
        }
        function obstructionForVertex(id, edge, points) {
          const bounds = obstacleBounds(id, edge);
          if (!bounds) return null;
          for (let index = 1; index < points.length; index++) {
            if (crossesBounds(points[index - 1], points[index], bounds)) {
              return { edge: edge.id, node: id, points: points.map(point => point && [point.x, point.y]) };
            }
          }
          return null;
        }
        function edgeObstructions(edge) {
          const points = graph.view.getState(model.cells[edge.id])?.absolutePoints;
          if (!points) return [];
          return vertices.map(id => obstructionForVertex(id, edge, points)).filter(Boolean);
        }
        // 只检查连线穿过无关节点内部；沿边框、经过自己的端点及分组背景不算遮挡。
        const obstructions = edges.flatMap(edgeObstructions);
'''
s=s[:start]+new+s[end:];p.write_text(s,encoding='utf-8')
