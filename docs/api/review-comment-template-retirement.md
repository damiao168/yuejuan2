# 个人常用评语模板接口移除记录

2026-10-27，按用户明确提出的“阅卷评语功能删除干净，不要只删前端”要求，移除个人常用评语模板功能。该模块的运行消费者是阅卷界面的模板管理与快捷码插入入口；它们随本次界面调整一并移除。

本次有意接受的契约变化仅包括以下六个操作和五个专用 schema：

- `GET/POST /api/v1/review/comment-templates`
- `GET/PUT/DELETE /api/v1/review/comment-templates/{templateId}`
- `POST /api/v1/review/comment-templates/{shortcut}/use`
- `ReviewCommentTemplate`、`CreateReviewCommentTemplateRequest`、`UpdateReviewCommentTemplateRequest`、`ReviewCommentTemplateResponse`、`ReviewCommentTemplateListResponse`

后端删除模板 handler、Store 接口、内存及 PostgreSQL 实现和错误映射；不保留可调用模板路由。OpenAPI 是权威源，SDK 和路由覆盖清单通过 `npm run generate:sdk`、`npm run generate:route-coverage` 正常生成。核对破坏性检查仅报告上述移除后，使用 `npm run openapi:baseline` 接受当前契约，再运行完整契约门禁；不修改或绕过检查脚本。

批注 CRUD、归一化图像坐标、修订并发控制及发布后学生可见范围继续保留。`comments`、`student_feedback`、`private_note` 等成绩、申诉、仲裁和模型输出共享字段不属于本次模板模块移除范围。

不新增破坏性数据库迁移。历史迁移 `000077_storyA06_review_annotations.sql` 及其创建的 `review_comment_template` 表和数据原样保留；新后端没有访问该表的业务路径。本次无需数据库升级，只需正常构建并替换 API 与前端镜像。
