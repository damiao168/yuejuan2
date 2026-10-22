# EduGrade Web Admin

EduGrade Enterprise Web 管理后台基础框架。

学校 AI 助手支持在对话输入框中用 Ctrl+V 粘贴 PNG/JPEG 图片，也可通过“上传文件”选择。图片会先显示缩略图，点击发送后才提交给学校配置的模型；每张最多 3 MB，与 PDF/文本合计每次最多 3 个附件。图片需要支持视觉输入的模型，整段对话的图片总大小最多 9 MB；浏览器历史只保存附件名称，不保存图片正文。

## 当前状态

已实现：

- React + TypeScript + Vite 基础入口。
- Ant Design 主布局、菜单、顶部栏、面包屑、通知入口和用户菜单。
- Browser History 路由、权限路由、403/404 状态页；旧 `/#/...` 链接自动转换为不含井号的路径。静态部署需将页面深路径回落到 `index.html`，当前 Nginx 已配置。
- API Client 与统一错误结构。
- Loading、Empty、Error 状态组件。
- 表格和表单基础封装。
- 设计 token 和桌面/移动响应式布局。
- 考试管理页面对接真实考试与组织 API，支持列表、筛选、新建/编辑表单、详情、状态推进和归档入口。
- 试卷与 Rubric 配置页面对接真实文件、试卷、题目和 Rubric API，支持上传登记、题目编辑、Rubric 表格和完整性检查。
- 答卷采集页面对接真实考试、文件、submission、OCR task 和 answer segment API，支持批量上传、质量检查、页文件查看/重传、OCR 任务和切分入口。
- 阅卷工作台对接真实 review_task、answer_segment、OCR、AI grade、Rubric、证据校验和 human_grade API，支持原图查看、评分面板、快捷键和提交后自动进入下一份。
- 双评仲裁页面对接真实 arbitration_task、final_grade、题目/Rubric、当前用户和 audit_log API，支持我的任务过滤、分配、最终分提交、结果展示和审计记录。
- 成绩管理与发布页面对接真实 final_grade、submission、quality、confirm、publish、CSV export、org 和 audit_log API，支持发布前质量门禁、二次确认、导出审计与水印提示。
- 学情报告页面对接真实 overview、classes、questions、grading-quality 和 report export API，支持 Recharts 图表、空状态、导出审计与水印提示。
- 申诉中心页面对接真实 appeals、statistics、review、close、org/exam 和 audit_log API，支持证据快照、处理说明、改分二次确认和审计记录。
- 审计日志页面对接真实 audit_log list/export API，支持多条件筛选、只读详情、敏感字段脱敏、租户隔离说明和导出水印。

注意：

- 当前登录已对接真实 `/api/v1/auth/login` 与 `/api/v1/auth/me`，不再使用模拟会话放行。
- 当前 Web 会话使用后端下发的 HttpOnly cookie；前端不再读取或写入 `localStorage` token。
- Dashboard 使用真实学校工作台汇总或平台学校与系统状态 API；质量看板使用考试质量、回评、漂移和种子策略 API。页面的数据来源与可见操作仍取决于当前用户权限及后端配置，未配置、空结果和请求失败会明确显示。
- 真实 API 页面依赖有效登录会话；会话失效时显示后端认证错误，不回退 mock 数据。

## 命令

```powershell
npm.cmd install --workspace apps/web-admin
npm.cmd run typecheck
npm.cmd run build
npm.cmd run dev -- --host 127.0.0.1 --port 5173
```

开发服务默认地址：

```text
http://127.0.0.1:5173/
```
