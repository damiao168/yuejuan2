# A01 数据、冻结、授权与 API 设计笔记

日期：2026-10-19。状态：设计提案，未修改业务代码、未运行测试、未验证真实模型效果。
整合结论以 [详细主方案](../../architecture/a01-reference-materials-design-2026-10-19.md) 为准：首期复用考试锁、不建 head 表；合法物化的考试使用授权与个人题库浏览权分开；显式撤回、通用下载旁路、软删除/共享对象 pin 和最终资产锁已在主文补齐。本文保留设计推导与备选项。
本文只审读下列调用链，不代表已审读全仓。行号是本次工作区定位记录，后续以符号名为准。
核心建议：在现有 paper 资料、files、准备度确认和 A02 之上增加版本化材料清单，不新建上传、对象存储或审批系统。

## 1. 已实现的能力与准确缺口

| 位置与当前行号 | 已核实行为 | A01 增量 |
| --- | --- | --- |
| `services/api-gateway/internal/paper/types.go:81` `PaperImportSourceRef` | 已有 source/file/document/page/block/bbox 追溯；bbox 为 `any` | 定义参考图角色、明确坐标域与裁图版本；不能直接把任意 bbox 当可执行裁图 |
| `services/api-gateway/internal/paper/types.go:323` `PaperImportDecodedPage` | 已有解码页 file、SHA、宽高与来源 | 把可选页面经考试作用域暴露为材料页，不复制页面存储 |
| `services/api-gateway/internal/questionbank/types.go:448` `Scoring`、`:456` `Asset` | 评分附件已有 file/SHA/name/content_type | 附件不等于模型参考图；需显式角色、用途和必需性 |
| `services/api-gateway/internal/questionbank/publication_postgres.go:358` `materializeQuestions` | 复制已发布题库版本，核验附件 active/hash，传入 paper | 复用此入口，不再造“参考图库” |
| `services/api-gateway/internal/paper/materialize_bank.go:24` `MaterializeBankQuestionTx` | `BankContent` 同时保存 content/assets/template 来源事实 | 从冻结附件选择或派生参考图，保留来源，不运行时追最新题库 |
| `services/api-gateway/internal/paper/readiness_snapshot.go:27`、`:57` | 配置快照和导入伴随快照均携带 BankContent | 两者加入同一份 manifest 身份与摘要 |
| `services/api-gateway/internal/paper/store_postgres_configuration.go:361` `ConfirmReadiness` | 持考试锁重新计算，比较配置 hash，绑定真实评估快照后提交 | 在同一事务核对材料 manifest、文件引用与授权状态 |
| `services/api-gateway/internal/paper/store_postgres_shared.go:45` `ensureExamPaperMutableTx` | 只允许 draft/configured 修改，考试行锁与确认串行 | 材料变更走同一可变性检查 |
| `services/api-gateway/internal/assessment/snapshot.go:38` `ExamQuestionSnapshot` | 已有 source_snapshot、content_hash 与学科/规则冻结事实 | 新图像事实应进入新版快照摘要 |
| `services/api-gateway/internal/taskmaterials/question.go:18` `QuestionForSnapshot` | 取指定快照之前的答案/解析；调用方负责对象授权 | 增加同快照材料 DTO；不能把该 Reader 当文件授权器 |
| `services/api-gateway/internal/files/store_postgres.go:133` `GetScopedTx` | 同事务文件共享锁；文件 scope 后还检查题库 ACL | 选来源时复用；任务只能读取冻结裁图，不获得整页来源权限 |
| `services/api-gateway/internal/files/downloads.go:31` `ReadDownload` | 读取受 scope 和 lifecycle 限制的私有文件 | 复用二进制响应和存储读取，不接受外部 URL |
| `services/api-gateway/internal/subjective/grading_reference.go:25` | 参考上下文只解析冻结的文字答案/解析 | 增加冻结 reference media manifest，与学生 evidence 分开 |
| `services/api-gateway/internal/subjective/trial_rules.go:78`、`:183` | A02 指纹冻结版本/模型政策/样本答案；不同则失效并取消批次 | 材料内容和当前使用授权代次纳入指纹 |
| `services/api-gateway/internal/subjective/trial_postgres.go:35` `Scope` | 从准确匹配的 readiness 伴随快照构造版本事实 | 避免另查可变材料清单替换冻结材料 |
| `services/api-gateway/internal/subjective/worker_completion_atomic.go:173` | 结算事务重新核对 run/lease/当前快照与答案版本 | 同事务核对材料与授权代次，拒绝撤回后的迟到结果 |

当前最大迁移为 `000197_subjective_run_snapshot_binding.sql`，新迁移在实施合入前重新编号。
该迁移已为新 subjective run 保存 `exam_question_snapshot_id`；旧行保持 NULL，A01 不得回填当前材料冒充历史输入。

## 2. 最小领域结构

建议清单按题目追加版本，内容不可改；有删图、换图、改角色时创建下一版。
每题以最新草稿版本作为准备度输入；ready 后固定确切 manifest_id，不能运行时取 MAX(revision)。
材料语义以此清单为权威；BankContent 保留原附件来源，避免出现两套可分别编辑的参考图清单。
首期每张已选图均为 required；任一无法发送就阻断该次 AI，不能静默少送一张。纯文字题显式保存 `text_only` 确认。
`text_only` 与历史 NULL 不同：前者是人工确认不需参考图的当前事实，后者是历史未记录，不能互相补造。

### 2.1 `question_reference_manifest`（新增）

| 字段 | 约束/含义 |
| --- | --- |
| id、tenant_id、exam_id、question_id | UUID；同租户复合 FK；题目必须属于本考试 |
| revision、previous_manifest_id | 每题单调递增；上一版同题；首版 revision=1 |
| schema_version、content_hash | 规范化 manifest 内容 SHA-256；摘要不包含时间、存储 key 或显示用签名 URL |
| command_id、request_hash、created_by、created_at | 幂等命令与审计；同 command 换内容返回冲突 |
| use_state、authorization_revision、revoked_at、revocation_reason | 运行时使用状态；不改历史内容；撤回使授权代次递增 |

唯一索引：`(tenant_id,question_id,revision)`、`(tenant_id,exam_id,command_id)`。
查询索引：`(tenant_id,exam_id,question_id,revision DESC)`。
内容列只追加；允许修改的运行时授权列必须有窄函数/CAS，不能开放通用 UPDATE。
初版可不建 head 表：材料写入与 ready 确认共用考试行锁，在锁内查询最新 revision。
若未来出现题目级高并发，再引入 head 指针，不预先增加一套修订体系。
若主方案采用 head，建议只保留 question 作用域 revision/current_version_id；version/item 保持不可变，唯一 current 指针走 CAS。
两种方案二选一，不能同时维护“latest revision”“question 指针”“head 指针”三套可独立修改的权威来源。

### 2.2 `question_reference_manifest_item`（新增）

| 字段 | 约束/含义 |
| --- | --- |
| id、tenant_id、manifest_id、ordinal | manifest 复合 FK；顺序是模型输入事实，不能不经版本变更重排 |
| role、required、caption | role 仅 `question_diagram` / `reference_solution`；caption 为人工核对说明 |
| source_kind、source_reference_json | `paper_import_page` / `bank_asset`；记录导入/题库版本、页码等 provenance |
| source_file_asset_id、source_sha256 | 来源文件身份及摘要；通过 files 注册表引用 |
| page_file_asset_id、page_sha256 | 原资料页；直接 PNG/JPEG 时可等于 source；PDF 为解码页 |
| pixel_bbox_json、source_width、source_height | 原点左上，整数像素 x/y/width/height；严格位于规范化页内 |
| derived_file_asset_id、derived_sha256、width、height、size_bytes | 真正发送的 PNG；服务端生成并校验，禁止客户端自报摘要 |
| transform_version、human_checked_by、human_checked_at | EXIF/旋转/裁图等确定处理版本；老师确认内容后才进入可冻结清单 |

唯一索引：`(tenant_id,manifest_id,ordinal)`；所有文件列有同租户 FK，拒绝删掉仍被引用的文件登记。
文件引用索引覆盖 source/page/derived，供删除保护与 reconciliation 查询使用。
不得用 JSON 数组独自承担文件引用：否则数据库无法可靠限制删除和跨租户关联。
角色、顺序、required、caption、来源摘要、坐标、处理版本与派生摘要均进 content_hash。
caption 不应承载新评分规则；评分细则继续由现有 rubric 编辑和冻结。

### 2.3 运行绑定与发送事实

`subjective_grading_run` 追加 nullable `reference_manifest_id`、`reference_manifest_hash`、`reference_authorization_revision`。
这些是 run 的固定输入身份；不能只在成功后的 raw_output 中记录，失败/超时也需要知道原计划。
建议新增窄表 `subjective_request_material_receipt`，关联已有 run、runtime task/attempt，不新建执行任务系统。
最少字段：tenant/run/task/attempt_no、request_digest、manifest_id/hash、authorization_revision、stage、items_json、recorded_at。
唯一约束：`(tenant_id,run_id,task_id,attempt_no,request_digest,stage)`；重复同事实可重放，异内容冲突。
stage 为 `prepared` / `provider_request_attempted` / `provider_acknowledged` / `failed_before_send` / `unknown_after_attempt`。
每项仅存 role/ordinal/derived_sha/dimensions/status/reason；不存 base64、对象 key、原始令牌或完整模型正文。
`provider_request_attempted` 表示调用方发起请求；网络超时不能证明供应商已收到，页面必须显示发送结果未知。
已有 ExecutionDiagnostics 承担用量/耗时；扩展其公开 DTO 引用材料 receipt，不把材料审计混进不受约束的 error_detail。

## 3. 裁图与 PDF 路径

直接 PNG/JPEG：从本考试已有资料选择 → 服务端取原图 → 实际解码/尺寸/摘要核验 → EXIF 归一 → 按像素裁图 → 重新编码 PNG。
前端归一化坐标只作为输入建议；服务端换算为整数框并返回实际框，教师核对实际裁图再保存。
不把原页 Base64 或客户端任意 URL 作为评分接口输入，不信任 MIME/扩展名与自报宽高。
数量、字节、单边像素、总像素限制在 Go/Python/前端共用契约；具体阈值由模型链路设计统一，本文不另定冲突值。
允许全页框，但必须由老师明确核对；不自动把整页附件送给 AI。

PDF 能复用底层，但不能宣称已有可直接使用的参考图 API：

- `services/page-processing-worker/page_processing/decoder.py:24` 的 `decode_document` 已用 pypdfium2/Pillow 解码并限制页数/像素。
- `services/page-processing-worker/page_processing/runner.py:142` 的 `_process_paper_import_decode` 已把 PDF 页保存为现有 files PNG 资产。
- `services/api-gateway/internal/paper/paper_import_runtime_postgres.go:156` 将页引用持久化到 `paper_import_parse_input.pages`，绑定 run/generation/source_revision。
- `services/api-gateway/internal/paper/document_visual_loader.go:41` 校验来源与页号；`:89` 读取并核对页 SHA 和文件 SHA。
- 该 loader 以 tenant-only `files.Get` 读取，是已授权内部链路组成；不能直接暴露为新教师端下载能力。

首期只承诺直接 PNG/JPEG 闭环；若同批已有有效 decoded page，可通过来源解析器列为可选来源。
旧 PDF 没有持久化页时显示“需先解析资料页”，复用既有 decode runtime；不要另写浏览器 PDF 渲染并信任客户端截图。
解码页来源必须匹配已接受的解析代次；不能拿历史失败/已替换代次的同页号混入本题。

## 4. API 草案与复用位置

全部路径是提案，需在权威 OpenAPI 后正式生成 SDK；不是当前可调用接口。
表内路径统一省略现有 `/api/v1` 前缀。

| 方法与路径 | 输入/输出重点 | 授权 |
| --- | --- | --- |
| GET `/exams/{examId}/reference-material-sources` | 列本考试资料页及已物化题库附件；page_ref、hash、宽高、可用原因 | 复用考试管理/读取 scope；逐项来源过滤 |
| POST `/exams/{examId}/questions/{questionId}/reference-material-crops` | page_ref、expected_source_sha、pixel_bbox、command_id → 私有派生裁图 token/元数据 | 管理本考试；来源当前可读；题目同考试；draft/configured |
| GET `/exams/{examId}/questions/{questionId}/reference-materials` | 当前草稿 revision/hash/items，或显式 snapshotId 的冻结清单 | 管理范围；阅卷人员走任务端，避免泛化考试读取 |
| PUT 同路径 | expected_revision、command_id、完整有序 items → 新 manifest | 管理本考试；锁内 CAS；只接受本题裁图句柄/合法来源 |
| GET `/review-tasks/{taskId}/reference-materials/{itemId}/image` | 仅返回任务快照清单中的派生 PNG | 当前有效任务/分派权限；材料有效；不会授权原始整页 |
| GET `/scoring-runs/{runId}/reference-materials/{itemId}/image` | 负责人在 A02 核对本 run 冻结图 | 现有 grading manage + run 考试对象检查 |
| GET `/subjective-grading-runs/{runId}/material-receipts` | 每次尝试的计划/发送事实摘要 | 现有 run/任务授权；不接受任意文件身份 |

裁图句柄必须绑定 tenant/exam/question/source_sha/实际 bbox/transform/hash，不能仅为短期签名 URL。
裁图请求可做同步有界工作；无法在请求预算完成时复用 worker runtime，而不是新增独立队列体系。
渲染对象先走 pending→写对象→核验→active；manifest 保存只接受 active，沿用 files lifecycle。
新增来源/派生用途需同步 files owner-type 校验、scope 解析、删除保护和 reconciliation，不只增前端枚举。

## 5. 授权与隐私边界

读取来源必须同时满足 tenant/考试归属/当前来源 ACL；files 的题库 ACL 不能被“同校管理员”静默跳过。
题库 `exam_allowed` 是可用于考试的业务前提，不等于任意教师拥有源文件下载权。
把有权复制的题库附件派生为考试裁图，应记录明确考试使用授权事实；任务仅获得派生图查看权，不继承题库浏览权。
该考试使用授权的撤回通过 use_state/authorization_revision 执行；源文件隔离/删除/内容漂移亦阻断使用。
个人题库读取角色被移除是否撤回已合法复制的考试使用权，需要明确产品政策；默认保守停用待重授权，不能臆测授权永久有效。
不可把授权选择器只写成 created_by：员工离职与学校保有考试材料是不同事实。
worker 依服务端冻结清单与有效模型使用授权读图；浏览器传入的可见性布尔值不是许可依据。
同样的图在界面可预览，不代表获准发给外部模型；模型准入、部署区域和有效凭证检查继续执行。
来源限定为试卷/标准解答资料，拒绝 submission/student 身份页作为参考来源；不能仅靠人工标题判定来源类型。
“有权限”“裁过图”都不保证匿名；图内姓名、水印、二维码仍需人工核对与必要的脱敏处理，未实现不能宣传自动匿名。
冻结裁图接口使用私有缓存策略；撤权后不能依赖长期公网签名 URL 继续读取。

## 6. 冻结、并发、幂等与恢复

材料写锁序：考试行锁 → 本题 manifest revision → source/page/derived 文件按 ID 排序共享锁。
ready 确认沿用考试 advisory+行锁，重读 manifest 与授权 revision；不在锁前计算摘要后直接冻结。
对象读写不在长数据库事务内等待：先读/生成派生对象，再在短事务重验来源 revision/hash/active 后绑定。
对象生成成功而绑定失败：保留可重放句柄；没有被 manifest 引用的派生对象按现有 reconciliation/保留期清理。
同 command 同规范化请求返回同结果；同 command 换题/换框/换角色返回 409；响应丢失用原 command 恢复，不能生成新对象事实。
PUT 丢失响应时 GET 当前版本并按 command 查询；未知结果不自动降级成新的修订。
ready/scoring 状态拒绝新增/换图；需要修改时走合法的准备修订流程或新考试，不能后台覆盖冻结快照。
安全撤回允许冻结后发生，但只改变可用性；历史图片身份、原发送事实和原成绩均不被重写。
来源对象丢失/摘要漂移属于不可用，不自动用同名文件替换；恢复必须核对同 SHA，再恢复可用状态。

## 7. 快照兼容和 A02 衔接

无参考图的既有考试保留旧 hash 算法和快照解释；不能因新增空数组使所有已确认配置失效。
含参考图的新冻结路径提升明确 schema_version，把 manifest_id/hash/有序清单纳入配置与 import 伴随快照。
评估快照 source_snapshot 的新分支引用相同材料身份，并纳入 content_hash；保留已有 schema 1/2 原义，历史迁移不改写。
不能只在 BankContent 加图：普通导入题也要同样冻结，且 A02 的 Scope/模型上下文/教师任务读取必须命中同一身份。
`taskmaterials.QuestionForSnapshot` 扩展为返回指定快照材料描述，不直接返回可绕授权读取的存储地址。
新 run 的 request ID、builder binding hash、idempotency digest 都纳入 manifest hash、图像角色/顺序/摘要和授权 revision。
A02 `trialFingerprint` 纳入冻结材料与授权代次；缺图、撤回、同 ID 内容漂移均使旧批准不再可扩批。
`refreshTrial`、`prepareTrialEnqueue`、`authorizeTrialBatch` 在原服务端门禁处复用同一检查；不能只给页面加 disabled。
worker prepare、真正发送之前和 atomic complete 均校验材料依据；发送后撤回只能拒绝结果提交，不能声称撤销已发生的外部传输。
结算仍锁 run→runtime task，再检查 material revision/use_state；撤回操作不得反向持有 task 锁等待 run，需统一锁序或异步取消。
迟到结果保留失败/冲突与发送 receipt，不能写当前 AI 建议，也不能覆盖人工已提交分数。
同版学生答卷已启动过 AI 后继续属于 A02 Covered，材料变更不能洗掉其曝光事实；不足 5 份独立样本则人工处理。
旧 run 的材料身份 NULL 显示“历史请求未记录”；不可由当前题库附件倒推出“当时已发送”。

## 8. 必须交付的针对性验证

1. PNG/JPEG 带图题从资料到 frozen manifest 到模型请求的 role/order/hash 一致；真实请求结构断言，不只预览截图。
2. 并发换图与确认准备度只允许一个版本胜出；响应丢失同 command 重放不会新增事实。
3. 跨租户/跨校/跨考试、题库 ACL 撤回、未分派任务、任意 file_id、任意外部 URL 均无法读图或让模型发送。
4. 文件删除/隔离、对象丢失、PNG 冒充、解压炸弹、超像素/超尺寸、错误 PDF 代次在发送前被拒绝。
5. A02 创建后撤回、批准扩批后撤回、worker 发送后撤回、旧 lease 回包等时序拒绝继续入队或结算。
6. 历史文字题/题库附件冻结 hash 回归不变，NULL 材料身份无伪造发送记录；人工阅卷继续可用。
7. 合成图可证明契约和状态边界，不能证明真实准确率、匿名性或费用节省；这些仍需获授权样本另测。
