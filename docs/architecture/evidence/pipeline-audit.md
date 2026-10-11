# 阅卷流水线、AI 与失败恢复审计证据

审计日期：2026-10-10。范围：图 05、06、07、09 的业务、图像、评分及任务恢复边界。结论来自当前工作区的静态源码，不代表生产使用统计、模型效果测试或数据库故障注入。只生成架构文档，未修改业务源码。逐文件实际审读范围见 `pipeline-reviewed-files.json`；检索命中与文件枚举不等于完整审读。

## 1. 已实现流程与启动边界

1. 考试配置、准备确认、开始采集是用户命令；开始采集重新读取配置 hash，就绪确认不能跨配置变化复用。证据：`services/api-gateway/internal/paper/configuration_handlers.go:101`、`:138`、`:167`。
2. 上传注册资产与启动采集批次分开；批次创建 `capture_file_decode`。解码后的逐页 PNG 上传、条码观测发生在 page worker，后续领域结果创建质量任务。证据：`services/api-gateway/internal/capture/store_postgres.go:80`、`:180`、`:270`，`services/page-processing-worker/page_processing/runner.py:182`。
3. 条码观测只包含 text、格式、polygon、orientation 等原始结果，不等于可信模板或学生身份。解码与 zxingcpp 提取见 `services/page-processing-worker/page_processing/decoder.py:44`、`barcode.py:12`；受控身份优先级见 `services/api-gateway/internal/capture/registration_store_postgres.go:240`。未经确认的身份/质量状态会阻塞自动配准。
4. 配准完成不会在这一回调里调用主观 AI。OCR、数学理解、评分运行和主观建议各有命令或队列入口。证据：`services/api-gateway/internal/ocr/runtime_coordinator_postgres.go:16`、`grading/scoring_run_start.go:56`、`subjective/handlers_worker.go:76`。

## 2. 图像质量、模板与切题的真实顺序

质量前处理与整页配准是不同步骤。`image_quality/engine.py:43` 在像素处理前检查尺寸上限，处理 EXIF、测量焦点/照明/遮挡/噪声/边缘/透视，按策略有限 deskew；anchor 与 curvature 为 `not_evaluated`。配准输入为通过质量的 normalized 页和锁定模板页，不能把质量测量画为已完成模板 homography。

模板路由顺序：已验证实体纸张/条码模板 → 考试绑定 → 同考试唯一锁定模板；没有直接身份时才排已有模板候选匹配，最多 8 个锁定模板/页。无候选时直接记录 unknown 并转复核，不学习新模板。证据：`services/api-gateway/internal/capture/registration_store_postgres.go:240`、`template_match_store_postgres.go:15`。

候选匹配将静态 guard 分与几何置信度按 30%/70% 合成，默认最低分 0.70、第一/第二名 margin 0.06；几何失败的候选不能仅凭相似布局胜出。静态 guard 遮去答案 ROI，使用 96×96 灰度 pHash 与边缘 Dice，检查方向和比例；比例差不超过 0.10，guard 分不低于 0.68。证据：`services/page-processing-worker/page_processing/registration.py:77`、`:159`。

对 `bound_auto`/`locked_with_guard`，guard 失败后允许少量候选重匹配以诊断；若选中的 template ID/hash 不等于任务冻结的考试模板，报 `template_conflict_with_exam_lock`。fallback 不能悄悄替换考试锁定模板。证据：`services/page-processing-worker/page_processing/runner.py:243`、`:306`。

自动配准：相同尺寸且像素差均值不超过 1 时使用 `pixel_identity`；其余先 ORB(5000、fastThreshold=7)，再 AKAZE，Hamming kNN ratio<0.75，至少 12 个匹配，RANSAC 阈值 4px。可接受几何结果至少 8 个 inlier、比例≥0.35、平均重投影误差≤8px、coverage≥0.55。这里的误差是内点欧氏距离的均值，不能标为 RMSE；coverage 是变换后角点多边形面积/模板面积，并非交集覆盖率。证据：`services/page-processing-worker/page_processing/registration.py:234`、`:283`、`:428`、`:432`。

切题在 homography 后进行：`warpPerspective` 到模板尺寸，再按模板 `question_regions` 的归一化矩形 ROI 裁切，输出逐题 PNG、normalized bbox、pixel bbox、资产 hash。并不存在先切题再配准的主路径。证据：`services/page-processing-worker/page_processing/runner.py:211`、`:224`，`registration.py:369`。

配准领域提交的 confidence<0.75 时页面/片段需要人工复核；算法置信度是实现中的评分，不是验证过的错误概率。人工确认要求 completed+needs_review、配准资产与原因，改变页面状态/修订号；该函数本身没有同步把所有 segment 的复核标记改为 generated。证据：`services/api-gateway/internal/capture/registration_store_postgres.go:430`，`registration_review_store_postgres.go:8`。

手工四点校正确有实现：点须 finite、互异、归一化范围内、凸且有一致方向，面积≥0.05；矩阵 det>1e-10、condition≤1e8、coverage 在 0.70–1.15。预览由 worker 生成配准页与 ROI 裁片；应用要求 source_page_revision、base run、expected revision 一致，保存旧裁片/页面快照并使旧配准失效；撤销要求未出现后续页面变化并恢复快照。不能据此宣称所有 OCR/AI/发布证据均已级联失效。证据：`services/page-processing-worker/page_processing/registration.py:307`，`services/api-gateway/internal/capture/correction_apply_store_postgres.go:14`、`:111`。

手工绑定只接受同考试 locked 模板，首次/后续绑定采用 expected revision；不是把任意未知图片保存为新模板。证据：`services/api-gateway/internal/paper/store_postgres_template_binding.go:25`。

### 未发现能力的检索依据

待确认：YOLO 备用版面识别、未知版面自动创建/保存/复用模板。对 `services`、`ai-services` 的 Go/Python 源执行 `rg -n -i 'yolo|ultralytics|unknown.template|未知模板|auto.template|自动.*模板' services ai-services -g '*.go' -g '*.py'`，未命中 YOLO/ultralytics 实现或未知版面建模板入口；`unknown template` 仅为 OMR 测试用例，自动模板注释指已有 locked 候选。对 `docs` Markdown 检索也未发现 YOLO 开发计划的明确设计依据。检索只能支持“本次范围未发现”，不能证明所有未审读文件均不存在。

已读取 `docs/stories/STORY-055-capture-batch-registration-segmentation.md:59`–`:74`：V1 支持锁定模板、固定版式和人工确认，明确不承诺任意未知试卷自动理解或无模板切题；`docs/stories/STORY-054-exam-configuration-template-readiness.md:127` 亦排除任意版式/无模板阅卷。图中保留为待确认，不能标为已实现，也不能擅自标为规划中。

## 3. 三类“空白”不能合并成自动零分

| 检测位置 | 真实事实/阈值 | 后续边界 |
|---|---|---|
| 整页质量 | `blank_probability = clamp(1 - ink_coverage / 0.012)`；>0.97 产生 `blank_page` 质量问题 | 是整页墨迹启发式，印刷线框/文字也算墨迹；不是逐题“学生未作答”检测，进入质量复核 |
| 选择题 OMR | 最高选项 strength<ambiguous_threshold，默认 0.10，decision=`blank` | `needs_human_review = decision != selected`；blank 不直接零分 |
| TEXT OCR | 无 blocks 时 reason=`empty_text_result` | 路由人工判断；识别为空不证明未作答 |

证据：`services/image-quality-worker/image_quality/metrics.py:31`，`policy.py:84`，`services/page-processing-worker/page_processing/omr.py:86`、`:148`，`services/ocr-worker/ocr_worker/recognition_router.py:122`。OMR 可使用绑定的空白参考图做 template_difference，worker 下载后核对 SHA256；印刷模板差分不等于任意主观题空白分类器。证据：`services/page-processing-worker/page_processing/runner.py:409`，`omr.py:42`。

本次没有确认一个统一的逐题空白检测器，可以把任意主观裁片判为自动零分；不得把整页 blank_probability 或 OCR 空文本改画成这项能力。v1/v2 请求构造均要求非空 AnswerText，空文本如何由业务修正/人工形成有效答案需要继续按具体入口核查。

## 4. 评分方式、单 adapter 与三智能体内核

`scoring_mode` 真实枚举是大写 `RULE_AUTO`、`AI_ASSIST`、`AI_FAST_CONFIRM`、`HUMAN_PRIMARY`、`DUAL_HUMAN`、`MANUAL_ONLY`，不是图中假设的小写。证据：`services/api-gateway/internal/assessment/policy.go:17`。

默认 ScoringRun 根据冻结模式分流：人工模式创建 ReviewTask；`RULE_AUTO` 的 fill_blank/numeric 在已发布规则+已有确认文本答案时走确定性规则，选择题须规则与选项 ROI 才排 OMR；不支持/缺规则/不满足自动确认条件转人工。`AI_ASSIST`/`AI_FAST_CONFIRM` 的 short_answer/calculation/essay/discussion 直接建教师确认任务，没有自动排 A/B/C Panel。`DUAL_HUMAN` 在本入口创建两张人工任务，会话阈值为 0、合分策略 average；其他创建入口可以冻结不同阈值，不能将这一默认值推广到所有人工双评。证据：`grading/scoring_run_routing.go:36`、`:59`，`scoring_run_start.go:384`，`scoring_run_process.go:42`。

普通主观 worker 执行：Prepare → 准入 → buildAdapterInput → 一次 activeAdapter.Grade → 服务端 GradeSettlementPipeline → 返回建议 → worker 完成回调。`server/module_grading.go:108` 装配的是单个 newSubjectiveAdapter；handler 的 panel 工厂是库构造入口，当前普通路由未调用该工厂。证据：`subjective/handlers_worker.go:76`、`:110`，`subjective/handlers.go:66`、`:96`。因此三智能体内核“已存在”与“默认生产链已经调用三模型”是两种不同结论。

Panel 内核：冻结答案、rubric、policy 版本和阈值；A/B goroutine 并行独立盲评，输入深拷贝；服务端依据 criterion/evidence/required point/confidence/hard risk 比较。只有精确共识直接采用 A 的结果，不将未解决分歧简单平均。分歧时 C 使用原始材料，不接收 A/B 输出；C 必须 StrengthRank 更高且 ModelVersion 与 A/B 不同，高置信度、无硬风险且 rubric 加权一致率满足冻结策略才 resolved，否则 human_review。证据：`subjective/panel_orchestrator.go:140`、`:185`、`:206`、`:260`、`:267`、`:281`、`:448`，`panel_disagreement.go:22`。

批准策略入口验证学段/学科/archetype、approved、evaluation/readiness 引用、policy/model set 版本以及三角色有效模型批准。影子入口用于观测分歧。Panel resolved 不是 final_grade 或 score_release；不应连线表示直接公开成绩。证据：`subjective/panel_orchestrator.go:78`，`panel_shadow_runner.go:80`。

## 5. 已实现多模态与尚未验证的提供商边界

普通 Go HTTPAdapter v1 发文本、题干、rubric、版本；领域 AdapterInput 虽有 AnswerImageRef，v1 wire request 不带裁片图像。数学 v2 有真实生产构造和调用：取当前有效裁片、核对 tenant/exam/资产 owner、PNG 尺寸/大小/SHA256；读取前后检查当前元数据未改变，绑定 answer version、math artifact/revision、scoring version 和 crop hash。不能把元数据读取注释画成数据库行锁保证。证据：`subjective/adapter_http.go:291`，`active_crop_resolver.go:59`，`math_grading.go:53`，`grading_agent_v2_builder.go:106`、`:186`。

`/grading/grade-v2` 实际调用 `ProductionMathV2Application`；fixture seam 与生产类共存，不能只读 fixture 注释就判无生产实现。`math_candidate_messages` 将文字+一张 PNG data URI 发给 `request_structured`，真实多模态输入协议已实现。模型返回 candidate_mapping，禁止模型 final score；Go 从冻结 rubric+有效数学事实确定性结算，数学歧义、diagram/uncertain、关键低质量转人工，成功仍需教师确认。证据：`ai-services/grading_agent/app.py:132`，`app_v2.py:321`、`:365`，`subjective/math_grading.go:120`。

提供商配置与视觉模型运行效果待验证：Local adapter 将 messages 送 `/chat/completions`；DashScopeNativeAdapter 的 `request_structured` 仍将同一 messages 送 native text-generation 路径，未见本函数转换 image_url 为专用原生视觉格式/端点。这里是源码中的接入边界，不能从“有 image_url”推断每种已配置模型都能正确消费图片，也未实际调用服务商测试。证据：`ai-services/grading_agent/model.py:553`、`:684`，`provider_adapter.py:53`。

## 6. 版本、幂等、重试与成本

主观 run 与结果核验 answer/rubric/model/prompt；数学增加 math artifact version、correction revision、scoring version，执行后重新读有效事实防止中途修订。Panel 冻结策略/阈值，不应运行中读取新阈值重结算。证据：`subjective/worker_execution_service.go:119`、`:136`，`math_grading.go:85`、`:139`，`panel_orchestrator.go:185`。

Python 普通/v2 成功缓存是进程内 TTL/LRU，同键 in-flight Event 共享结果，重启或多副本不能据此保证全链路 exactly-once；同键不同请求摘要为 409。模型 session、本地并发槽/排队超时、max_output_tokens、输出字节上限存在，但未确认全租户统一成本硬预算。证据：`ai-services/grading_agent/app.py:56`、`:125`、`:176`、`:190`，`model.py:482`，`app_v2.py:365`。

内层重试有界：Python 修复列表含无效输出、证据失败、暂不可用、限流，不含 model_timeout；Go v1 MaxRetries 限为 0–1，100ms 可取消等待，model_timeout 明确不重试。外层 subjective worker 将 APIError 报为 Retryable=true，由 runtime attempt/backoff 再排，可能再次进入模型请求。不同层级不能相加理解为统一“只调用一次”，也不能把 timeout 的内层规则推广到整个队列。证据：`subjective/adapter_http.go:157`，`services/subjective-grading-worker/subjective_grading_worker/runner.py:51`。

Runtime 真实状态：queued → leased → running → succeeded；可重试失败回 queued 并设置 not_before，非重试失败 failed，次数耗尽 dead_letter；lease_expired 是 attempt 记录，不是任务状态。Claim 用 SKIP LOCKED 接管过期 leased/running 并更换 token；普通耗尽任务收敛，paper import 来源留给业务 reconciler。显式 Requeue 只接受 failed/dead_letter，不重置 attempt_count，扩大 max_attempts 至至少再给一次尝试。证据：`workerruntime/store_postgres.go:143`、`:212`、`:255`、`:452`、`:530`。

服务身份回调在入口 TaskScope 校验当前 token/service/instance、来源及访问范围；最后 runtime.Complete 在任务行锁+数据库事务时间下再次检查租约。两次校验有不同作用，入口成功不能替代领域结果同事务的最后校验。Store.Complete 对 succeeded 支持相同 token、schema、结果 hash 的重放，但服务 HTTP TaskScope.AuthorizeLease 只接受 leased/running，故 Store 能幂等不等于外部完成端点在所有已成功状态均可重放。证据：`workerruntime/scope_middleware.go:93`、`:143`，`store_postgres.go:381`、`:563`、`:717`。

## 7. 已确认的提交与恢复风险

### P1-PIP-01：配准领域提交与最终租约校验分离

`capture/handlers.go:760` 先调用 ApplyRegistrationResult，完成独立领域事务；`:766` 再调用 runtime.Complete。ApplyRegistrationResult 锁 registration run 并写裁片/页状态，但事务内未锁 task/核验租约。触发条件为回调通过入口 AuthorizeLease 后，租约在领域写入前/期间过期或被接管，或者 runtime 最后提交出现存储故障。领域裁片和 page.ready/needs_review 已提交，后续拒绝不能回滚；处理中的旧执行者可能先提交有效裁片。不是“完全没有入口鉴权”，也未声称已在生产复现。证据：`capture/registration_store_postgres.go:410`–`:480`，`workerruntime/scope_middleware.go:143`。

建议在来源结果与 CompleteTaskInTx 的共同事务中锁来源/task、校验租约及冻结版本后一次提交；模板匹配和质量结果已有这种事务形态可参考。证据：`capture/template_match_store_postgres.go:210`，`imagequality/coordinator_postgres.go:49`。

### P1-PIP-02：主观完成末步失败后缺少正常重放恢复

`subjective/worker_completion_service.go:49` 先幂等查/建 ai_grade，`:57` 完成 runtime，`:64` 最后 UpdateRun succeeded。若最后 UpdateRun 失败，task 已 succeeded、grade 已存，但 run 仍 processing。再次 CompleteWorker 先 Prepare；`worker_execution_service.go:91` 只接受 leased/running，已成功 task 被拒绝，且服务 HTTP 入口亦拒绝已成功 capability。当前调用链没有在这条完成命令中补写 run 的完成收据/同事务保障。响应丢失也不能直接依赖此端点重放；它不意味着已存 ai_grade 被删除。静态代码证据，未运行 DB 故障注入。

建议将 grade/task/run 成功提交合并事务，或提供基于相同 requestID、结果 hash 的专门完成恢复路径，不重复付费推理。

### P2-PIP-03：Panel 人工 handoff 终态短路不补任务

`subjective/panel_orchestrator.go:493` 先 UpdatePanel human_review，再 RoutePanelHumanReview 建任务，最后关联 ReviewTaskID。第二/第三步失败后重试 Grade，`:193` 的终态分支只 loadTerminalPanelResult，不补建/关联人工任务，可能留下人工状态却无可领取任务或缺关联。该风险属于已实现内核；普通单 adapter 路由未接入，不能夸大为所有默认评分都受影响。建议在同事务下创建与关联任务，或终态读取时显式修复缺失关联。

## 8. 旧发布与不可变发布并存及 P1 边界

生产组装仍同时挂载两组 API，未发现只有新路径的保证，也未调查生产哪条路径被调用：`server/routes_release.go:18` 是旧 `/api/v1/exams/{examId}/publish`，`:31` 挂新 score-release，`:41` 挂旧 student grade；`server/module_release.go:68` 旧 ScoreHandler 未接 PublicationCoordinator，`:69`–`:72` 只有新 ScoreReleaseHandler 接入。

| 校验/结果 | 旧 ScoreHandler.PublishGrades | 新 ScoreReleaseHandler.Publish |
|---|---|---|
| 认证与作用域 | score:manage、近期认证、考试作用域 | score:manage、近期认证、release→exam 作用域 |
| 人工任务/OCR失败/已有片段终评 | qualityReportTx 检查 | gateTx 检查；终评按官方题目范围 |
| 名单匹配/页数/总分确认 | 有；允许 confirmed/pending_publish 等旧状态 | 有；允许 confirmed/published/locked |
| 官方题集合完整、各题满分、总分与终评核对 | 未见这些精确检查 | question_coverage_incomplete、score_integrity_mismatch |
| 质量看板 backmark/R3 Gold/校准/Seed 等 | 未调用 dashboard | dashboardIssues 纳入阻断 |
| 活跃重评、策略警告批准、只追加门禁证据 | 未调用 releasegate | PublicationCoordinator → RecheckForPublish |
| 公开事实 | 锁 final_grade、submission_grade published、exam.status=published | published immutable release + gate_snapshot + score_release_current 指针，同事务；不靠改 exam 状态代表该快照 |
| 学生读取 | published+locked submission_grade，读取 final_grade；不读 release/visibility | 当前 release items/questions，遵守 VisibilityPolicy |

逐项证据：`score/store_postgres.go:333`、`:398`、`:587`–`:740`、`:1034`；`scorerelease/store_postgres.go:259`、`:1126`–`:1231`；`releasegate/coordinator.go:29`、`service.go:131`；`server/server.go:44`；`scorerelease/student_detail_postgres.go:10`。

### P1-PIP-04：旧发布入口允许绕过新增完整性门禁与快照

具体可达条件：某考试官方两题，答卷页数与名单匹配已合格，但只生成一题 answer_segment；该片段已有 final_grade 和 confirmed submission_grade，无未完人工/仲裁或 OCR 失败。旧 missing_final_grades 只遍历已经存在的片段，不检查另一官方题没有片段，因此此条件可以通过旧门禁并写 published；新 question_coverage_incomplete 会拒绝同一来源。类似地，旧入口不读取 backmark/R3/重评或策略警告阻断。旧路径有自己的基本质量检查和权限，不能描述成“没有任何门禁”；差异是新增检查可被合法有权用户选择旧路径绕过。

旧学生接口随后可能返回成绩，而新 `/api/v1/student/exams/{examId}/result` 因没有 score_release_current 返回 not found，形成公开来源分裂。旧接口读取全部 final_grade，不引用新 VisibilityPolicy；已有旧发布成绩与新隐藏题分策略并存时，应进一步核查是否允许继续读取旧题分。本次未操作 API、未确认用户实际选择旧接口，也未声称泄露已发生。

建议统一发布到不可变 release/coordinator，再通过兼容适配返回旧响应；兼容学生接口使用当前发布快照和可见性策略。若决定保留独立旧发布，必须显式标识与补齐同等门禁，不能在架构图宣称全系统所有发布都已经受新门禁控制。

## 9. 验证范围与未完成项

已按四份 spec 的每页节点核对实现入口与证据，纠正枚举大小写、重投影误差定义、配准校正失效范围、入口租约与提交租约的区分，以及两条发布路径；新增空白边界、数学多模态、发布并存页。JSON 与图形最终结构/渲染验证由主任务统一完成。

仍待验证：真实服务商图片输入格式/模型视觉能力与效果；生产配置实际启用分支；空白与 OMR/模型阈值效果；数据库锁竞争/故障注入；运行时、Panel、发布兼容路径的端到端测试。未审读测试、配置和其余模块明确 pending；本报告没有“全仓逐文件审计完成”声明。

## 10. 审计期间变化文件的当前快照复核

`workspace-preservation.json` 记录相对任务开始基线的 30 个已有文件变化。这里不回滚这些并行工作，也不将新哈希自动视为已经审读。当前 pipeline 账本的既有源码哈希核对没有失效项；本节额外重审关联前端文件，读取开始/结束哈希一致后才记 complete，限定到本节所列文件快照。此处证明当前事实，不把并行改动归功于本架构任务，也不宣称所有 30 文件均已审读。

| 当前文件 | 已核实入口/边界 | 对图 05/06/07/09 的影响 |
|---|---|---|
| `AssessmentProfileEditor.tsx` | 六个大写模式见 :81；已有 snapshot 或离开 draft/configured 则只读见 :291；保存使用 expected_revision 见 :457 | 仍读取冻结策略和版本，未接入 Panel/worker；不推翻图05/07 |
| `ExamPreparationPage.tsx` | 核对逐题落库 scoring_policy.mode；404→UNCONFIGURED 并拒绝冻结；读取前后与确认前比较 configuration_hash，POST 传核对 hash，见 :229–:289 | 前端明确使用后端已实现的冻结版本边界，图05不需重排；旧客户端省略 hash 的兼容边界仍以后端源码为准 |
| `TemplateWorkspaceView.tsx` | PDF文字层可触发“候选区域（待核对）”，图像/扫描需手工，见 :231；locked 模板的考试绑定与版本锁定相互独立，见 :549–:597 | 未出现 YOLO/未知版面自动建模板路径；不把文字层候选功能误称通用未知版面识别，图06结论保持 |
| `CaptureBatchPage.tsx` | 处理批次 :443、处理答卷页面 :501、人工确认/重试 :577、四点工作区 :603；页旋转带 revision :552；批次创建原幂等载荷/恢复 :401 | 仍是采集/页面处理/人工修正控制台，没有增加前端 worker lease 或主观 Panel 执行；配准最终提交风险须以后端事务判断 |
| `useScoreWorkspaceData.ts` | :125 同时加载旧成绩/quality 与新 gate/releases/current；:45 仅把明确404当无当前发布；代次保护前端读取快照 | 此文件只有读取，不构成旧发布迁移/统一门禁；图05旧新路径并存、P1-PIP-04与两类学生来源分裂结论保持 |
| `GradingWorkbench.tsx` | :296–:312 续租 `renewReviewTask` 是人工阅卷任务；采分/rubric/question_snapshot 来自服务端上下文；动作委托既有 hooks | 人工续租不等于 Worker Runtime capability；现有 AI 建议或“第二意见”提示不证明 A/B/C Panel 默认接入 |
| `useGradingKeyboard.ts` | :41 判断按键/弹层/编辑条件，:65 只分派回调，提交与按钮共用人工提交动作 | 没有新增 API、worker lease、Panel 或公开成绩路径 |
| `CalibrationDrawer.tsx` | :98 使用样本 rubric_snapshot；:129、:159 带 expected_revision 保存策略；:205 提交人工校准分 | 是前端传递服务端快照/版本，不替代后端资格门禁或事务冻结 |
| `SchoolAssistantResult.tsx` | :43 展示发布版本，:45 说明来源读取时间；:65、:71 只调用 onNavigate | 展示/导航不执行发布、不构建快照，不保证聚合来源强一致 |

指定 9 文件均全文分段审读且开始/结束 SHA256 一致；后 4 文件由协作子审执行，详细逐行依据与额外依赖的有限读取范围见 `pipeline-current-ui-subread.md`。以上文件完整路径、当前 SHA256、行数与审读状态写入 `pipeline-reviewed-files.json`；组件 props/导入依赖未被自动升级为完整审读。前端请求代次只能防止旧响应覆盖当前展示，不能代替 PostgreSQL 版本、租约和发布门禁。没有发现需要改变 05/06/07/09 已核实后端事实的证据，规格和图布局保持本轮渲染版本。
