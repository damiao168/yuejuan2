# 原生图纸视觉复查

2026-11-13，10 份原生 draw.io 文件、73 个页面。使用本地 draw.io EditorUi / mxGraph 导入、逐页切换并导出 SVG。本地渲染工具来自上游 draw.io 的 `c72c9a98b7a6ce392556ed04c05e0ff19f43f5c3`。

- XML、节点、边端点、出处和页面几何检查通过。
- 原生渲染检查为 0 条悬空边、0 处文本溢出、0 处连线穿过无关节点。
- 已查看覆盖全部 73 页的 9 张联系表，并放大核对系统总览、ER 和评分流程。修改后的发布页展示兼容请求进入同一发布协调器，配准及主观评分页展示实际原子提交。
- 普通节点标题为 40–72px，正文为 32px。ER 表名为 34px，字段为 26px。原生文件可继续编辑。
- 预览默认使用 100% 原生尺寸，支持放大与适应页面。深链接采用页标识选择页面，标题可见，切页保留缩放。

[原生渲染](native-render-validation.json)绑定当前 XML 指纹。[图纸校验](diagram-validation.json)记录源码出处与几何检查。[预览交互](preview-typography-validation.json)记录深链接、缩放和联系表显示。[离线预览](../previews/index.html)包含全部 SVG。[本轮业务回归](remediation-validation-2026-11-13.md)记录代码验收范围。

首次审计的字号比较记录仍保存在 `typography-before.json` 和 `typography-validation.json`。本轮包含语义和页面变化，当前版本以原生渲染与交付校验记录为准。
