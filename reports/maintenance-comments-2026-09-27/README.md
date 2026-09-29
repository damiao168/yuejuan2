# 全仓维护注释覆盖清单

更新时间：2026-09-28T23:00:15.738397+08:00

本清单按开始任务时的工作区保存基线，合并已跟踪文件与未跟踪的维护源码；排除依赖、缓存、临时输出和历史报告目录。工作区已有修改被保留。

完成审读仅包括 annotated、existing_sufficient 和 self_explanatory。protected、documentation、excluded 是范围分类，不算已审读源码；pending 必须后续继续处理。

标准：[代码注释与维护说明标准](../../docs/engineering/commenting-standard.md)。逐文件状态、负责人和具体原因见 [coverage.json](coverage.json)。

| 状态 | 文件数 |
| --- | ---: |
| annotated | 746 |
| documentation | 381 |
| excluded | 47 |
| existing_sufficient | 398 |
| protected | 476 |
| self_explanatory | 538 |

| 分工 | 已审读 | 待审读 |
| --- | ---: | ---: |
| ai-workers | 142 | 0 |
| backend | 203 | 0 |
| backend-misc | 45 | 0 |
| backend-modelgovernance | 45 | 0 |
| backend-review-comments | 6 | 0 |
| backend-server | 85 | 0 |
| backend-subjective | 59 | 0 |
| clients | 385 | 0 |
| clients-pages-css | 82 | 0 |
| redo-capture | 25 | 0 |
| redo-clients | 25 | 0 |
| redo-grading | 23 | 0 |
| redo-mathunderstanding | 27 | 0 |
| root | 530 | 0 |

受保护文件的处理依据见 [protected-files.md](protected-files.md)。注释过程中发现的现有逻辑问题另记在 [总发现](findings.md)、[客户端发现](findings-clients.md)、[页面发现](findings-pages-css.md) 和 [后端发现](findings-backend.md)，未混入本次修改。
