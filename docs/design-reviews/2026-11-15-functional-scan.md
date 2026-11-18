# 2026-11-15 功能扫描

初次扫描确认下方两处 P2 功能缺陷，随后已按用户要求修复并更新本地。初次复现文件保存在 `output/audit-functional-20261115`；这些错误现状测试只保留为历史证据，修复验收以正式回归为准。本轮是重点流程审计，不是全仓所有页面与状态的完整验收。

## 修复结果

- 混合重复上传组：全场核对会直接识别现存无法归组的混合组，提供整组“不计入／上传替代答卷”。不自动跳过重复文件，不跨组凑页；此前成功组的文件和答卷保留。MemoryStore 和隔离 PostgreSQL 回归验证了旧组退出、已排队任务取消、迟到回调拒绝、完整替代答卷进入质检。相关前端两文件 60 项测试通过。
- PDF：每页都必须能提取文字；首个无文字页会明确报页码，拒绝整份附件，避免只发送页码或部分页面。扫描页可改传 PNG/JPEG 或带文字层 PDF；真实空白页需先删除。36 项 PDF、附件及上传界面相关测试通过，包含使用真实 PDF 字节的提取回归。更新后浏览器已验证第 2 页无文字时的提示，附件未残留、空输入不能发送；截图位于 `output/playwright/functional-fixes-20261115/pdf-rejected.png`。
- 路由门禁：复用 TypeScript AST 核对预览 helper 的调用接线和原图权限分支，保留禁止教师原图访问约束。增加六种绕过反例；33 项脚本测试与生产路由门禁通过。
- Docker：常规重启超时后，限定重启 Docker 安装目录下的自身进程，引擎恢复。原有 18 个项目服务重新运行，未清空容器、卷或数据库。
- 本地已发布 `local-functional-fixes-20261115-5b797440e52e`，数据库保持 `000232`，API/Web 健康，管理端、学生端、聊天、会员及许可入口检查通过。生产构建通过。部署摘要位于 `output/functional-fixes-20261115/deployment-summary.json`。临时 PostgreSQL 仅用于合成数据回归，测试后已删除。

## 确认问题

### P2：混合重传把新答卷一起卡在待归组状态

触发条件：同一采集批次已导入文件 A，再次选择 A 和新的文件 B 作为同一上传组。例如每人一页、这次选择两个文件。

注册时 A 被标记为 `duplicate`。[归组条件](../../services/api-gateway/internal/capture/upload_group_store_postgres.go)把它计入已登记文件数，却要求组内所有文件均为 `completed`；[处理队列](../../services/api-gateway/internal/capture/store_postgres_batches.go)只选择 `uploaded/failed`。重复文件无法完成，导致 B 虽已解码，仍停在 `decoded / waiting_files`，不能进入后续质检与阅卷。

再次处理返回 `invalid capture transition`。[页面提示](../../apps/web-admin/src/features/capture/CaptureBatchPanels.tsx)让用户重新选择完整答卷，但再次选择 B 又会命中已完成文件的哈希去重。该组登记数量齐全，[全场材料问题查询](../../services/api-gateway/internal/capture/collection_store_postgres.go)既不列出重复文件，也不把该组列为 `upload_group_incomplete`；[材料处置界面](../../apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx)因此没有整组“不计入／上传替代答卷”入口。

影响边界：只有组中同时存在重复文件和新文件时会卡住新的有效材料；纯重复单文件只会去重。管理员直接调用已有 `ExcludeFile` API 可以退出整组，正常页面目前没有相应入口。单独删页不能释放文件哈希或完成上传组。

建议先把这类无法归组的组纳入材料问题，复用已有整组排除及替代流程。不要直接忽略重复文件，否则多页答卷可能错误跨学生拼组。

验证：通过 Go overlay 调用当前 `MemoryStore`，确认 B 卡住、重试失败、重新选择再次重复。PostgreSQL 分支已按 SQL 与调用顺序核对，运行环境不可用，未做数据库集成复现。证据为 `capture-duplicate-group-audit_test.go`、`capture-overlay.json`、`capture-duplicate-group-result.txt`；使用 `go test -overlay <绝对overlay路径> -p 1 -vet=off ./internal/capture -run '^TestFunctionalAuditDuplicateGroup$' -count=1 -v`，工作目录为 `services/api-gateway`。测试断言的是错误现状，PASS 不表示缺陷已修复。

主要位置：`upload_group_store_postgres.go:41–45`、`store_postgres_files.go:37`、`store_postgres_batches.go:70–71`、`CaptureCollectionPanel.tsx:241`。

### P2：扫描 PDF 上传成功，但模型实际只收到页码

[PDF 提取](../../apps/web-admin/src/features/school-assistant/conversationPdf.ts)在没有文本层时仍向结果加入“第 N 页”。因此两页扫描 PDF 会返回 `第 1 页\n\n\n第 2 页`，通过[附件读取](../../apps/web-admin/src/features/school-assistant/conversationAttachments.ts)的非空检查。服务端[消息转换](../../services/api-gateway/internal/modelgovernance/managed_chat_images.go)把这串页码当附件全文发送，原 PDF 页面与图像并未发送。

触发：上传无文本层的扫描 PDF、图片 PDF 或空白 PDF，要求助手阅读其中内容。界面显示上传成功，但模型没有收到实际内容。部分页有文本、部分页只有图像时，也存在静默缺页内容的风险；本轮已实测确认的是整份无文本层场景。

建议按真实提取文本判断是否可读。首版可明确拒绝无文本层 PDF，并提示改传 PNG/JPEG 或带文字层 PDF；OCR 可按后续需求接入。不要用页码包装文本作为读取成功依据。

验证：`node node_modules/vitest/vitest.mjs run --config output/audit-functional-20261115/vitest-chat-audit.config.mjs`，1 项复现断言通过。复现断言当前错误现状，不代表已修复。

主要位置：`conversationPdf.ts:29–34`、`conversationAttachments.ts:63`、`managed_chat_images.go:56`。

## 独立的运行与维护问题

- 本轮检查时，本地 `127.0.0.1:8088` 的健康、AI 对话、会员和学生入口均无法连接，8080/8088 未发现监听。Docker Desktop 进程存在，但引擎容器查询返回 500。该状态阻断真实运行验收，不能据此认定为业务源码缺陷；本轮没有重启 Docker 或更改其他项目服务。
- `check:production-routes` 在“教师不得请求答卷完整原图”处失败。检查仍匹配内联三元表达式；当前实现已经提取为 `answerPreviewPath`，其原图分支仍受 `canViewOriginalImage` 约束。属于已有静态门禁与重构不同步，未据此报告原图访问失控。需要更新检查，防止发布流程持续误失败。
- 会员中心目前明确为参考报价页，订阅、到期、权益计量与在线支付未实现。本轮按既有交付范围记录，不将其混同为新发现的代码缺陷。

## 验证与覆盖范围

- 24 个相关前端测试文件、257 项现有测试通过；另有 PDF 与采集组两个错误现状复现通过。
- OpenAPI 兼容检查与权限契约结构检查通过。权限检查的 PostgreSQL 实际授权验证不在本轮可执行范围。
- 新增实报输入、估算回退、单次输入与整轮累计用量隔离、平均速率、线程切换等链路已审读，本轮未确认新的 P1/P2。
- 前端完整审读学校助手目录的非测试 TS/TSX、README，以及 AI API、历史消息规范化、会员页面与相关路由挂载。其余测试和 CSS 未逐文件完整审读，保留 pending。
- 后端审读对话主流程、SSE、模型调用、上下文预算与记忆、用量解析、公开搜索与引文校验、缓存及请求合并。核心业务抽查考试创建草稿、成绩草稿与正式发布、采集文件去重与归组、全场材料问题恢复。
- 其他子系统、真实供应商调用、全部角色的浏览器操作和完整 PostgreSQL 流程仍为 pending，不据本轮测试通过宣称全仓没有问题。
