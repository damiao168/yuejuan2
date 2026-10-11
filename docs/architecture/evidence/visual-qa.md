# 原生图纸视觉复查

最终原生渲染、截图与视觉复查：2026-10-11。最终图纸为10文件、70页面，渲染器是实际app.diagrams.net的EditorUi/mxGraph。`render_native.js`逐文件导入、切页、在1:1视图检查原生对象，再由graph.getSvg导出。末次交付检查逐一核对10份XML的SHA-256与原生渲染输入一致。

## 结果

- XML重新解析、节点id、源码出处路径/行号、页面范围、非分组节点重叠和边端点：0错误。
- 原生70页：0悬空连线、0超过容差的节点文本溢出、0正交连线穿过无关节点。
- 第一轮发现22条边的默认路由穿过其他节点；`route_obstructions.py`仅修改ports/points，第二轮全部通过。修改记录见[edge-routing-adjustments.json](edge-routing-adjustments.json)。
- 中文字体Microsoft YaHei显示正常，白底、统一圆角、状态配色、菱形判断与图例一致。ER详页采用精选FK，完整定义在catalog，降低密集长线。
- 简化后686节点可见文字减少26.8%，关系不变。图面保留短名称、分层及必要边界；原文和源码保存在details/evidence属性。ER显示精选字段、核心IDX和条件唯一约束，FK线只显示基数，完整字段组合保存在属性；AI失败页保留超时不重试、修复及费用未知等短标签。

字号与紧框更新：普通框标题40–72px、正文32px，ER表名34px、字段26px，箭头文字26px、图例24px。普通标题单行居中，边距12px；方框按真实字宽和正文行数收紧。65个过长显示标题改为短名，完整说明不变。更新后重新导入全部10份原生XML，70页仍为0溢出、0悬空边、0穿越无关节点；[typography-validation.json](typography-validation.json)核对全文、出处、状态与关系未变，且当前XML哈希与渲染输入一致。

本次再查看全部8张联系表及5张总览大图。预览默认100%原生尺寸，125%/150%及适应页面可选，图内滚动；深链接标题可见，切页保留缩放，见[preview-typography-validation.json](preview-typography-validation.json)。该更新只覆盖图面文字和排版，源码审计时点沿用原记录。

人工查看8张九宫格联系表，依次覆盖页1–9、10–18、19–27、28–36、37–45、46–54、55–63、64–70。检查页面留白、截断、节点遮挡、主方向、颜色与分域阅读入口；另查看功能总览、总体架构、ER总览、三智能体内核和物理部署的大幅预览，确认细字和中文可读。联系表是缩略检查，阅读详细字段应放大原生文件或独立SVG。

预览HTML补充了hashchange处理：外部深链接与上一/下一页选择实际显示对应页面；截图脚本等待picker的目标index后截图，避免只改URL而内容停在旧页。图纸自身未受此预览修正影响。

## 复查对象

| 文件 | 页数 | 原生导入/渲染 | 人工视觉复查 |
| --- | ---: | --- | --- |
| 01-feature-tree.drawio | 11 | 通过 | 全页联系表+总览放大 |
| 02-system-architecture.drawio | 2 | 通过 | 全页联系表+分层总览放大 |
| 03-module-dependencies.drawio | 4 | 通过 | 全页联系表 |
| 04-database-er.drawio | 29 | 通过 | 全页联系表+核心ER放大 |
| 05-business-workflow.drawio | 3 | 通过 | 全页联系表 |
| 06-image-template-pipeline.drawio | 3 | 通过 | 全页联系表 |
| 07-ai-grading.drawio | 4 | 通过 | 全页联系表+Panel放大 |
| 08-api-data-flow.drawio | 8 | 通过 | 全页联系表 |
| 09-task-state-machine.drawio | 3 | 通过 | 全页联系表 |
| 10-security-deployment.drawio | 3 | 通过 | 全页联系表+部署放大 |

全部SVG位于`../previews/`，离线入口为[index.html](../previews/index.html)。浏览器截图副本位于`../previews/qa/`。检查机器只检测正交连线与节点边界，不证明所有边标签永远不重叠；本批以视觉复查补充，不声称图纸证明业务运行通过。
