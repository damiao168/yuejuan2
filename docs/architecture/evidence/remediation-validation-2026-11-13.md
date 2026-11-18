# 本轮整改验证记录

2026-11-13，基于 `main` 的 `5b797440e52e0dd356e41901adfca3564c385391` 和当前工作区。既有未提交内容保留，整改后的源文件与验证范围见 [本轮文件记录](remediation-reviewed-files.json)。

- 兼容发布、正式快照、学生可见性和并发幂等通过真实 PostgreSQL 17 回归。历史迁移回填包含归档考试，未发布学生保持原有可见性范围。明细新增、移动、删除与并发发布守卫均经过验证。
- 考试和学籍的范围外键、指标语句刷新、跨租户更新、首次及已有投影的并发刷新均通过隔离 PostgreSQL 回归。100 名学生批量写入只产生一次学生指标刷新。
- 500 人名册的存储与 HTTP 同步分别测得 530 ms 和 372 ms，满足该用例各 5 秒的预算。测试规模仅支持这组预算结论。
- Web 题库、学校助手、模板识别和复评相关 86 个文件、1055 项测试通过。桌面扫描队列 35 项测试通过。
- 各前端工作区和 SDK 类型检查、Web 生产构建通过，架构门禁通过。保留的 4 项告警来自测试文件长度。
- 契约 CI、生成产物一致性、破坏性变更检查和 Go `apicontract` 包通过。605 条注册中 567 条匹配 OpenAPI，38 条登记项为内部 Worker、运维和书签分派。原有 ExamPage、AppealPage 列表项保留宽类型兼容，新详情和命令记录已明确字段。
- 图纸由本地运行的 draw.io EditorUi / mxGraph 导入，10 份、73 页全部通过悬空边、文本溢出和连线遮挡检查。渲染器源码为本地临时工具目录中的上游 draw.io。原生 XML 与 SVG 指纹见 [原生渲染](native-render-validation.json)。
- 图纸工具的 31 项回归通过。数据库目录交叉校验和本地 SQLite 内存 Schema 校验通过。

日志分别位于仓库 `output/architecture-release-final.log`、`output/architecture-database-complete.log`、`output/architecture-database-scale.log`、`output/architecture-web-final.log`、`output/architecture-contract-check-final.log` 和 `output/architecture-apicontract-final.log`。最终文件级检查见 [交付校验](delivery-validation.json)。

验证使用隔离测试库，专用 PostgreSQL 已停止。合成临时数据库目录保留在仓库外。新迁移编号为 000230、000231、000232，生产数据库在部署时按正式迁移流程应用。完整远端 CI、真实供应商评分效果和现场扫描设备仍需相应环境验收。
