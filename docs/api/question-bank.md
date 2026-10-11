# 题库、发布与治理 API（STORY-069A / 069B / 069C）

此实现是考试上游的私有题库。Bank 归属租户与学校；Item 是稳定身份，不保存题干；Version 固定内容、schema 版本、metadata、答案、解析、Rubric、附件摘要与来源版本。迁移为 `000140_question_bank_core.sql`、`000141_question_bank_publication.sql` 和 `000142_question_bank_metadata_acl_search.sql`。当前已支持审核发布、指定版本复制到草稿考试、受控 metadata、完整用户/组 ACL、授权优先的组合检索和题目退役；蓝图组卷由后续切片实现。

## 授权与命令

请求经过现有 session/Bearer 认证与 BrowserCSRF。租户、actor 和学校访问范围来自服务端 AccessScope。每个题库另有当前有效的用户/组动作绑定；创建者获得 read/create/edit/manage，其他账号没有默认内容绑定。全局 `question_bank:*` 许可只允许使用对应接口，不授予其他题库内容。学校归属还必须在当前组织范围内。

内容 create/edit/review/publish/retire/statistics 同时需要对应动作；内容读接口另需 read。manage 可以查看 Bank 身份与结构、修改题库名称/说明/状态、schema 和完整 ACL，不因此获得题目、版本、附件或搜索结果。作者默认不能批准自己的版本。每次请求都从服务端重新解析用户和组 membership，撤权后旧页面缓存或已知资源 ID 不能继续读取内容。

除只读的 metadata 校验外，所有变更型 POST/PATCH/PUT 必须携带 `Idempotency-Key`（沿用现有格式、最多 128 字符）。业务变更、audit、outbox 和持久命令回执在同一个事务提交。相同租户、actor、key 与请求重放返回原结果；同 key 改变操作、目标或输入返回 409。该入口不重放 HTTP 响应缓存，每次重放仍检查当前学校范围和题库动作绑定；授权撤销后返回 404。

## 接口

| 方法与路径 | 全局动作 | 返回 |
| --- | --- | --- |
| GET `/api/v1/question-banks` | read 或 manage | banks/total/limit/offset |
| POST `/api/v1/question-banks` | create | 201，bank |
| GET `/api/v1/question-banks/{bankId}` | read 或 manage | bank |
| PATCH `/api/v1/question-banks/{bankId}` | manage | bank |
| GET `/api/v1/question-banks/{bankId}/metadata-schema` | read 或 manage | schema |
| PUT `/api/v1/question-banks/{bankId}/metadata-schema` | manage | 新 schema |
| POST `/api/v1/question-banks/{bankId}/metadata-schema/validate` | read 或 manage | valid/field_errors |
| GET `/api/v1/question-banks/{bankId}/acl` | manage | acl |
| PUT `/api/v1/question-banks/{bankId}/acl` | manage | acl |
| GET `/api/v1/question-banks/{bankId}/items` | read | items/total/limit/offset |
| POST `/api/v1/question-banks/{bankId}/items` | create | 201，item + version |
| GET `/api/v1/question-bank/items` | read | 授权过滤后的组合搜索页 |
| GET `/api/v1/question-bank/items/{itemId}` | read | item |
| GET `/api/v1/question-bank/items/{itemId}/versions` | read | versions/total/limit/offset |
| POST `/api/v1/question-bank/items/{itemId}/versions` | edit | 201，version |
| POST `/api/v1/question-bank/items/{itemId}/retire` | retire + read | item |
| GET `/api/v1/question-bank/versions/{versionId}` | read | version |
| PATCH `/api/v1/question-bank/versions/{versionId}` | edit | version |
| PUT `/api/v1/question-bank/versions/{versionId}/scoring` | edit | version |
| GET `/api/v1/question-bank/versions/{versionId}/reviews` | read | reviews |
| POST `/api/v1/question-bank/versions/{versionId}/submit-review` | edit | version |
| POST `/api/v1/question-bank/versions/{versionId}/approve` | review | version |
| POST `/api/v1/question-bank/versions/{versionId}/return-to-draft` | edit/review | version |
| POST `/api/v1/question-bank/versions/{versionId}/publish` | publish | version |
| POST `/api/v1/question-banks/{bankId}/reviewers` | manage | bank |
| GET `/api/v1/question-bank/rubric-templates` | read | items/total/limit/offset |
| POST `/api/v1/question-bank/rubric-templates` | create | item + version |
| POST `/api/v1/exams/{examId}/questions/materialize-from-bank` | read + exam manage | exam revision + questions |

列表 `limit` 默认 50、范围 1～100，`offset` 范围 0～1000000。Bank 的 `q` 搜索名称/说明；Item 的 `q` 搜索编号；版本历史按 version_no 倒序分页。总数与当前页使用同一读事务，先过滤授权再计数。

创建题库输入 school_id/name/description。修改题库输入 expected_revision/name/description/status；学校归属不在修改输入中。

创建题目输入 item_code、question_type、assessment_archetype、stem、options、default_score、knowledge_points、metadata 和 custom_metadata。metadata 必须含 subject_code、education_stage、grade_scope；难度、认知、版权、语言、建议用途、建议用时与来源年份采用系统字段约束。空选项/知识点数组会规范化为 `[]`。default_score 为正、最多两位小数，当前使用原分值。

创建新版本只输入 source_version_id，必须来自同一 Item，复制源草稿内容。版本号在 Item 行锁下分配；与该版本内容的乐观锁 revision 分开。草稿 PATCH 输入 expected_revision 与完整内容，成功 revision 加一；陈旧 revision 不覆盖其他编辑。

## 受控 Metadata 与 schema 版本

每个 Bank 指向一个 `metadata_schema_version`。schema 的 `fields` 支持 enum/string/number/boolean/taxonomy、required、数值范围、字符串长度、稳定 enum value 和 taxonomy term ID；系统字段名不能被自定义字段覆盖。`knowledge_points` taxonomy 存在时，知识点只接受其中 active 的稳定 term ID。非法值返回字段路径到错误列表的 `field_errors`，创建、草稿更新、提交审核和发布都会校验版本绑定的 schema。

PUT schema 总是追加新版本并增加 Bank revision，不原地修改旧定义。新建或从最新内容派生的草稿绑定当前 schema；已有 Version 继续读取其原 schema 版本，旧内容与 hash 不随 schema 更新变化。`GET .../metadata-schema?version=N` 可读取指定历史定义。

## ACL 预设与组

ACL 文档按 Bank revision 乐观锁替换可编辑的预设职责。历史组合权限与创建者、当前操作者的直接授权由服务端原样保留。用户绑定与题库本地组都引用当前租户的既有用户身份；组成员不由客户端作为请求身份声明，而由服务端持久 membership 在每次访问时解析。预设展开如下：

| 预设 | 动作 |
| --- | --- |
| Viewer | read, statistics |
| Author | read, create, edit |
| Reviewer | read, review |
| Publisher | read, publish, retire |
| Manager | manage |

返回的用户绑定与组均可用 `Custom` 表示不完全匹配预设的既有动作集合；更新输入只接受五个命名预设。更新事务在题库行锁内读取旧授权，不删除 Custom 用户直接授权或 Custom 组的名称、动作、成员；提交同一 Custom 用户、组 ID 或组名的预设行会拒绝整次更新，不能借普通表单改写历史组合权限。此保留不依赖客户端回传动作或历史成员的当前启用状态，也不授予新组合权限。其他预设职责可正常维护；移除可编辑用户或组成员后，后续列表、版本、审核历史、搜索和附件读取立即按剩余权限计算。Manager 可以继续管理 Bank 结构，但没有 read 时看不到内容。

## 组合检索与统计可用性

`GET /api/v1/question-bank/items` 先应用租户、学校与有效 Bank read 授权，再应用 q、bank_id、subject_code、knowledge_point、question_type、archetype、workflow_status、difficulty_band、cognitive_level、copyright、intended_use、use_policy、单个 custom metadata key/value 和 statistics_available 筛选。授权谓词同时约束 count 与 page；两者在同一 repeatable-read 事务中读取，并使用确定的次级排序键避免翻页漏项或重复。

mode 默认为当前 published 版本加调用者自己的 draft；`published`、`my_drafts`、`all` 可显式收窄或展开到调用者已获授权的版本。排序支持 updated_desc、created_desc 和 item_code_asc。069F 尚未提供观测统计，因此当前返回 `statistics_available=false`，筛选 true 得到空结果；教师难度标签仍是 metadata，不能冒充观测统计。

题目退役以 Item revision 做乐观锁并写 audit。退役不修改已复制进考试的冻结事实，但拒绝后续 materialize。

## 评分 bundle、模板与状态机

`PUT .../scoring` 以完整替换方式写入 `answer`、`solution`、`rubric`、`assets`、`use_policy` 和可选的 `template_version_id`。答案支持标准答案、等价答案与绝对/相对容差；Rubric 保存 max_score、评分点、required、evidence_requirements、deductions 和 examples。发布校验按 assessment_archetype 检查必需事实，并要求题目默认分、Rubric max_score 和评分点合计一致；首版不隐式缩放分值。

Rubric 模板也是稳定 Item + Version，`kind=rubric_template`。题目选择一个已发布模板版本时，服务复制该版本的 Rubric 到题目自己的评分 bundle，并保留 `template_version_id` 追溯。模板的新版本不会刷新已保存或已发布题目。

Version 状态机为 `draft → reviewing → approved → published`。reviewing、approved、published 的内容、评分子表和附件绑定均锁定。reviewing/approved 可退回 draft；退回会增加内容 revision，旧审批不再匹配。每条 review 记录保存 decision、reviewer、comment、content_revision 与 bundle_hash；发布只接受当前 revision/hash 的独立批准。published 版本只能派生新 draft，不能原地编辑或删除。

`bundle_hash` 的分域为 `question_bank_bundle_v2`。规范输入包含题干、题型/archetype、分值、选项、知识点、版本 metadata、答案、解析、Rubric、用途策略与附件内容摘要；不含 revision、时间、附件存储路径或数据库行 ID。Go Memory Store 与 PostgreSQL 函数对同一事实生成相同 SHA-256。

## 指定版本复制到考试

`materialize-from-bank` 输入考试 `expected_revision` 与 1～100 条 `{version_id, question_no, sort_order}`。来源必须是 active Bank/Item 下、用途允许正式考试的 published question 版本；调用者必须同时处于目标考试范围且可读取每个来源 Bank。目标考试只允许 draft/configured 状态。

单个事务复制 Question、AnswerKey、Solution、Rubric、默认 Assessment config 和来源追溯，随后增加考试 revision 并提交 audit/outbox/命令回执。任一步失败全部回滚；解除故障后可用同一命令 key 重试，成功重放返回相同 Question ID。复制后的 `source_type=question_bank`、source item/version/hash 固定，题库新版本、退役或模板更新不会改变考试事实。AnswerArea 和目标试卷坐标不从题库复制，缺失时既有完整性检查继续返回 not_ready。

readiness 快照对手工题保持 schema v1；题库来源题使用 schema v2 并增加 source snapshot。旧快照继续按原 schema/hash 读取。实际评分只读取考试内冻结的 AnswerKey/Rubric，不在阅卷时回查题库。

## 状态与哈希边界

Item 的 current_published_version_id 只指向本 Item 的 published 版本。归档 Bank 可读，拒绝新增题目、派生、发布和 materialize；退役 Item 也不能再进入考试。版本身份及来源不可修改，版本不能删除。

`content_hash` 仍是 069A 的题干内容摘要，hash_scope 为 `draft_content_v1`；发布、审核和 materialize 使用完整 `bundle_hash`，两者不能互换。

题库附件不发放独立的长期签名 URL。文件被题库版本引用后，通用文件读取入口会在每次请求重新校验当前 Bank read；即使调用者保存了 file ID，撤权后也返回资源不可见。已复制进考试的附件生命周期继续由考试冻结事实保护。


## 精选历史考试题（069D）

POST /api/v1/question-bank/imports/preview 接受 1～50 条 selections，每条固定 question_id、target_bank_id、source_snapshot_id 与 mapping。响应逐题返回冻结内容、评分、Profile/archetype/scoring policy、来源身份/hash、发布前待补问题以及获授权目标题库中的查重摘要。查重分 exact_bundle、same_content_different_scoring、similar_content；最后一种当前仅比较忽略大小写/重复空格的相同题干，不是语义相似度模型。

新 readiness 确认在同一事务保存内容 companion，并绑定 ready 时的精确 Assessment Snapshot ID/hash。既有 hash-only readiness 不回填，返回 source_snapshot_unavailable；没有任何 live Question/Rubric 的导入回退。旧 readiness 的配置 hash 算法保持原样。来源快照不包含学生身份、学生作答或 AnswerArea；题号只保存为 provenance。

POST /api/v1/question-bank/items/import-from-question/{questionId} 创建 Draft；Idempotency-Key 必须与正文 command_id 一致。正文必须固定 target_bank_id、source_snapshot_id、assessment_snapshot_id、scoring_source=original_exam、dedup_decision、expected_target_revision、expected_target_schema_version 与 mapping。来源 hash 不接受客户端声明，服务器从冻结记录核验。new_item 的 expected_target_revision 是 Bank revision，mapping.item_code 必填；new_version 指定 existing_item_id 并使用 Item revision，不传 item_code。目标题库 schema 必须匹配请求版本。

POST /api/v1/question-bank/imports/confirm 接受 items（每条在单题输入上增加 question_id），返回 HTTP 207 和逐题 status/result/error_code/retryable/command_id。每题各自提交事务和持久回执；一个失败不回滚其他题。相同命令与 payload 重放返回原 Item/Version；同 key 不同 payload 冲突。客户端应先保存逐题命令与完整请求，结果未确认时重试原请求；取得失败回执后才重新映射失败项。

来源考试范围与目标题库 read/create 均在服务端核验，关联新版本另需 edit，重放同样复查当前授权；路由还检查全局 question_bank:create/edit 与考试管理/复核/阅卷权限。预览先通过目标内容授权，再计算摘要与命中数，不泄露未授权库。

导入仍遵循独立审核与发布状态机。grade_scope 缺失为 unmapped、copyright 缺失为 unknown，允许留在 Draft，但送审/发布前必须修正，目标 schema/taxonomy、答案/选项/Rubric 发布校验继续生效。传统手工 Question 没有冻结选项字段，需要在 mapping.options 补齐。当前只支持一致的原考试评分来源；不能选择尚无完整冻结证据的重评分版本。

版本详情提供 import_provenance（来源 readiness/Assessment 身份与 hash、Profile/archetype/scoring policy、mapping、目标 schema、关联决策与 command）；派生版本可沿 source_version_id 查回来源。发布后用于新考试，通过 source_bank_item_version_id 读取版本详情可追溯到原考试 snapshot。导入不修改来源考试。

## 错误

| HTTP | code | 条件 |
| --- | --- | --- |
| 400 | question_bank_invalid_input | 内容、枚举、分值、分页无效，未知 JSON 字段或多值 JSON |
| 400 | question_bank_metadata_invalid | schema 或值校验失败；响应含 field_errors |
| 400 | idempotency_key_required / invalid_idempotency_key | 命令标识缺失或格式无效 |
| 401/403 | 现有认证/权限错误 | 未登录、无有效数据范围或全局动作许可 |
| 403 | question_bank_access_denied | 平台 actor 或请求学校不在授权范围 |
| 404 | question_bank_resource_not_found | 不存在、跨租户、学校范围或题库内容动作未授权 |
| 409 | question_bank_revision_conflict | 陈旧 revision、重复编号或命令 key 被不同输入复用 |
| 409 | question_bank_content_locked | 已归档、退役或非 draft 内容 |
| 409 | question_bank_import_source_unavailable | 单题导入缺少完整冻结 readiness/Assessment 来源，或两者事实不一致 |
| 207 | 逐题 `error_code` | 批量确认的来源不可用、资源不可见、目标冲突、锁定、命令 payload 冲突或暂时失败；查看每题 `retryable` |
| 503 | question_bank_unavailable | 存储失败 |

精确输入/输出以 [OpenAPI](../../services/api-gateway/openapi/edugrade-api.openapi.json) 和[生成 SDK](../../packages/sdk/src/generated/types.ts)为准。错误和事件 payload 不携带题干或评分答案。
