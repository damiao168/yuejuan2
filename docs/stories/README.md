# EduGrade Enterprise Stories

本目录用于按 Story 分步交付 EduGrade Enterprise。

## 执行规则

1. 每次只执行一个 Story。
2. Story 未完成验收前，不进入下一个 Story。
3. 每个 Story 必须按固定闭环推进：规划 Story、审阅规划、实现、审阅实现、修改实现、自审审批。
4. 每个 Story 完成后由 Codex 按验收标准自审自批，并留下审批记录。
5. 每个 Story 必须包含范围、非范围、修改文件、测试方式和验收标准。
6. 不允许用 mock/stub 冒充真实能力；需要 mock 时必须明确标记。
7. 每个 Story 完成后必须输出：
   - 修改摘要
   - 新增文件
   - 修改文件
   - 运行命令
   - 测试结果
   - 剩余风险
   - 下一步建议

## Story 内部闭环

每个 Story 的文档或审批记录必须能回答：

1. `Plan`：本 Story 要做什么，不做什么，验收证据是什么。
2. `Plan Review`：计划是否越界、是否遗漏显式需求、是否符合当前仓库实际。
3. `Implementation`：实际改了哪些文件，为什么这样改。
4. `Implementation Review`：逐条检查验收标准，指出不足。
5. `Fixes`：针对实现审阅发现的问题做修正。
6. `Approval`：修正后再次检查并给出批准/不批准结论。

## Story 顺序

| Story | 名称 | 状态 |
| --- | --- | --- |
| STORY-000 | 总控审阅与实施计划 | Done |
| STORY-001 | 企业级项目骨架 | Approved |
| STORY-002 | 企业级 PRD | Approved |
| STORY-003 | 企业级技术架构文档 | Approved |
| STORY-004 | 数据库模型设计 | Approved |
| STORY-005 | 后端基础服务骨架 | Approved |
| STORY-006 | 认证与权限系统 | Approved |
| STORY-007 | 组织、学校、班级、学生管理 | Approved |
| STORY-008 | 考试管理模块 | Approved |
| STORY-009 | 试卷与题目配置模块 | Approved |
| STORY-010 | 文件上传与对象存储 | Approved |
| STORY-011 | 答卷采集与 Submission | Approved |
| STORY-012 | OCR 服务接口与任务队列 | Approved |
| STORY-013 | 答题区域切分模块 | Approved |
| STORY-014 | 多智能体 Orchestrator | Approved |
| STORY-015 | 客观题与填空题判分 | Approved |
| STORY-016 | 主观题 AI 评分接口 | Approved |
| STORY-017 | 证据校验 Agent | Approved |
| STORY-018 | 人工复核与阅卷工作流 | Approved |
| STORY-019 | 双评与仲裁模块 | Approved |
| STORY-020 | 最终成绩与成绩发布 | Approved |
| STORY-021 | 申诉流程 | Approved |
| STORY-022 | 学情报告 | Approved |
| STORY-023 | Web 管理后台基础框架 | Approved |
| STORY-024 | Web 考试管理页面 | Approved |
| STORY-025 | Web 试卷与 Rubric 配置页面 | Approved |
| STORY-026 | Web 答卷采集页面 | Approved |
| STORY-027 | Web 阅卷工作台 | Approved |
| STORY-028 | Web 双评仲裁页面 | Approved |
| STORY-029 | Web 成绩管理与发布页面 | Approved |
| STORY-030 | Web 学情报告页面 | Approved |
| STORY-031 | Web 申诉中心 | Approved |
| STORY-032 | Web 审计日志页面 | Approved |
| STORY-033 | Windows EXE 客户端骨架 | Approved |
| STORY-034 | EXE 扫描工作站功能 | Approved |
| STORY-035 | EXE 离线阅卷基础功能 | Approved |
| STORY-036 | UI 设计规范文档 | Approved |
| STORY-037 | Docker Compose 私有化部署 | Approved |
| STORY-038 | 可观测性与系统诊断 | Approved |
| STORY-039 | 安全加固 | Approved |
| STORY-040 | AI 评估集与评分质量测试 | Approved |
| STORY-041 | 全链路 E2E 测试 | Approved |
| STORY-042 | 企业级验收文档 | Approved |
| STORY-043 | 上线前代码审查 | Reviewed - blockers found |
| STORY-044 | 严重与高风险问题修复 | Approved |
| STORY-045 | 生产化差距清单与上线路线图 | Approved |
| STORY-046 | 生产管理员 Bootstrap 与会话安全加固 | Approved |
| STORY-047 | 数据库级多租户约束硬化 | Approved |
| STORY-048 | Web mock 页面退场与生产菜单收敛 | Approved |
| STORY-049 | 真实 OCR Worker 接入 | Approved |
| STORY-050 | 答卷图像质量检测与页面标准化 | Approved |
| STORY-051 | Agent Worker Runtime | Approved |
| STORY-052 | 生产部署 Runbook 与预生产验收 | Approved |
| STORY-053 | 产品外壳、角色首页、机构启用和考试工作区 | Approved |
| STORY-054 | 考试配置、试卷模板、题目、Rubric 和开考准备 | Approved |
| STORY-055 | 采集批次、扫描导入、页面处理、模板配准和自动切题 | Approved |
| STORY-056 | 客观题评分引擎和专业人工阅卷工作台 | Approved |
| STORY-057 | Grading Agent 生产契约（lab 智能体接入线） | Approved |
| STORY-058 | Grading Agent 服务实现（lab 智能体接入线） | Approved |
| STORY-059 | 平台接入与评分结果落库（lab 智能体接入线） | Approved |
| STORY-060 | 客观题自动化产能兑现与答卷完整性保障 | Planned |

## 编号语义说明（2026-07-26）

`docs/deployment/production-readiness-roadmap.md` 中 V1.0 总控计划的 STORY-057～059（主观题 AI/质量中心/学生端）与实际实施的 STORY-057～059（lab 智能体接入三部曲）存在历史错位。**以本索引为准**：057～059 已被 lab 接入线占用并完成；总控计划中对应的能力（阅卷质量中心、学生端/申诉/报告）顺延至 STORY-061 之后重新编号。规划文档中引用旧编号处以本表为准，不再回改历史文档。

STORY-053 之后按 V1.0 正式产品交付总控任务继续推进，不提前宣称 Production Ready。
