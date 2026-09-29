# 不直接修改的文件

全仓注释工作以 `docs/engineering/commenting-standard.md` 为标准；以下分类不等于已完成代码审读。

- `services/api-gateway/migrations/`：Compose 迁移入口对原始 SQL 计算 SHA-256 并核对数据库记录。即使只增加 SQL 注释，也会改变校验值。现有文件保留原文，新增迁移在首次应用前按标准写说明。不能通过改历史哈希来完成注释任务。
- `packages/sdk/src/generated/`：由 `scripts/generate-sdk.mjs` 和契约源生成；`scripts/ci/check-generated-contracts.mjs` 重新生成并比较字节。日后需要生成接口说明时，单独修改生成模板或权威契约并验证产物。
- `ai-services/prompts/`、`lab/src/prompts/*.md`：正文是模型输入，修改可能影响输出或哈希证据。本批保留正文；注释只加到读取和校验逻辑附近。AI 提示词 manifest 保存冻结哈希；实验注册器计算读取内容的哈希，二者约束不同。
- JSON、JSONL、数据集、冻结结果、锁文件：保留机器读取格式和数据内容。解释写在相邻维护文档，不添加虚构字段。
- 二进制、静态图像、许可证、缓存及临时文件：没有源码注释需求，不修改。

本说明不包含真实配置值或凭据；本地私有 `.env` 未作为待注释源码读取。
