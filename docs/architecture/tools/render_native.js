// 通过实际 diagrams.net 编辑器导入原生 XML 并逐页导出；测试不接触业务API或账号。
// 这是 playwright-cli run-code 的函数输入，由 runner 注入 page 后调用，不能直接作为 Node 入口自调用。
async (page) => {
  const base = "http://127.0.0.1:8766";
  const manifest = await (await page.request.get(`${base}/evidence/diagram-validation.json`)).json();
  const names = [...new Set(manifest.details.map(p => p.file))];
  const results = [];
  const inputs = [];
  await page.evaluate(() => {
    if (!window.auditUI) {
      const box = document.createElement("div");
      box.style.cssText = "position:fixed;inset:0;z-index:1000;background:white";
      document.body.appendChild(box);
      window.auditUI = new EditorUi(new Editor(false), box);
    }
  });
  for (const name of names) {
    const xml = await (await page.request.get(`${base}/diagrams/${name}`)).text();
    const source_sha256 = await page.evaluate(async xml => {
      const digest=await crypto.subtle.digest('SHA-256',new TextEncoder().encode(xml));
      return [...new Uint8Array(digest)].map(v=>v.toString(16).padStart(2,'0')).join('');
    },xml);
    inputs.push({file:name,source_sha256});
    const metadata = await page.evaluate(xml => {
      auditUI.setFileData(xml);
      return auditUI.pages.map(p => ({id:p.getId(),title:p.getName()}));
    }, xml);
    for (let index = 0; index < metadata.length; index++) {
      const rendered = await page.evaluate(async index => {
        const ui = auditUI;
        ui.selectPage(ui.pages[index]);
        const graph = ui.editor.graph;
        graph.container.style.width = "2600px";
        graph.container.style.height = "2600px";
        graph.view.scaleAndTranslate(1,0,0);
        graph.refresh();
        await document.fonts.ready;
        graph.view.validate();
        const vertices = [], edges = [], overflow = [];
        const model = graph.getModel();
        function collectCell(cell) {
          if (!cell.vertex) {
            if (cell.edge) edges.push({ id: cell.id, source: cell.source?.id, target: cell.target?.id });
            return;
          }
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
        const svg = mxUtils.getXml(graph.getSvg("#ffffff",1,20));
        return {vertices:vertices.length,edges:edges.length,dangling:edges.filter(e=>!e.source||!e.target),overflow,obstructions,svg};
      }, index);
      const slug = `${name.slice(0,-7)}-${String(index+1).padStart(2,"0")}`;
      const saved = await page.request.post(`${base}/previews/${slug}.svg`, {data:rendered.svg,headers:{"Content-Type":"image/svg+xml"}});
      if (!saved.ok()) throw new Error(`Export failed ${slug}`);
      const {svg, ...checks} = rendered;
      results.push({file:name,index:index+1,...metadata[index],preview:`previews/${slug}.svg`,...checks});
    }
  }
  const report = {renderer:"Actual app.diagrams.net EditorUi / mxGraph",files:names.length,pages:results.length,inputs,results};
  await page.request.post(`${base}/evidence/native-render-validation.json`, {data:JSON.stringify(report,null,2),headers:{"Content-Type":"application/json"}});
  return {files:names.length,pages:results.length,dangling:results.reduce((n,p)=>n+p.dangling.length,0),overflow:results.filter(p=>p.overflow.length).map(p=>({file:p.file,page:p.title,objects:p.overflow})),obstructions:results.filter(p=>p.obstructions.length).map(p=>({file:p.file,id:p.id,count:p.obstructions.length}))};
}
