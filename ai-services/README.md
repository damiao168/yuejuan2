# 智能阅卷 Agent 服务

这是供平台内部调用的主观题辅助阅卷与试卷解析服务。服务调用需要认证；主观题结果仅作为教师建议，不负责确认或发布最终成绩。服务不接收租户或学生身份信息，会校验模型响应、核对答案中的证据、按 Rubric 采分点重新计算建议分数，并记录不含敏感答题内容的请求遥测。

主观题推理支持本地 llama.cpp 和阿里云百炼 DashScope 原生生成接口。试卷解析还支持学校在平台中配置的 OpenAI 兼容接口或 DashScope 原生接口；解析模型的选择不改变主观题辅助阅卷的治理规则。

## 本地测试

在仓库根目录运行：

```powershell
$env:PYTHONPATH = "ai-services"
python -m pytest -q ai-services/tests
```

TLS 握手测试使用本机已安装的 OpenSSL 签发临时 CA 和带 `IP:127.0.0.1`、`DNS:grading-agent` SAN 的短期证书。可用 `EDUGRADE_TEST_OPENSSL` 指定 OpenSSL 的绝对路径，临时文件遵循 `TMPDIR`、`TEMP` 等系统临时目录配置。缺少 OpenSSL 时测试失败，不跳过。测试经过真实 `serve()`、TLS 上下文和网络请求，只用合成适配器替换外部模型就绪探测；通过不代表真实模型已连通。

## 内部接口

| 接口 | 用途 |
| --- | --- |
| `GET /health` | 检查服务是否存活。 |
| `GET /ready` | 检查当前配置的模型运行环境是否可用。 |
| `GET /grading/prompts/current` | 经认证后只读查看运行时实际加载的提示词文件快照。 |
| `POST /grading/grade` | 经认证后生成主观题阅卷建议。请求必须带 `Authorization: Bearer ...` 和 `Idempotency-Key`。 |
| `POST /grading/grade-v2` | 经认证后生成数学题采分点候选映射，不输出最终分数；同样需要上述两个请求头。 |
| `POST /paper/parse` | 经认证后解析试卷；设置 `Accept: application/x-ndjson` 可接收进度和结果流。 |
| `POST /answer-sheet/analyze` | 经认证后执行单页 OpenCV 线框检测、多模态语义关联和边界精修，返回可编辑候选；满足条件的区域自动对应。 |
| `POST /answer-sheet/match` | 经认证后以整套页序、特征配准和静态结构验证学校库候选，不调用外部模型。 |

调用 `/paper/parse` 时，网关会解析学校的默认模型配置，通过内部连接传入仅对本次请求有效的 `managed_model`。构造文档提示词前会移除凭据，解析结果也不会返回凭据。学校未设置默认模型时，使用已配置的本地解析模型。外部模型连接仅接受公开可访问的 HTTPS 地址，校验 TLS 主机名且不跟随重定向。

### 答题卡混合分析

`/answer-sheet/analyze` 复用现有服务 Bearer 认证和请求级 `managed_model` 供应商适配。它是独立于试卷解析、主观题评分的操作，不接收 `agent_role`、正式题目 ID 或发布指令，也不使用阅卷接口的幂等缓存。模板 revision、学校资源范围及最终收录门禁由网关负责；当前分析候选没有持久化缓存，保存到模板后才能恢复。

模板操作的 DashScope 原生适配按[官方视觉协议](https://www.alibabacloud.com/help/en/model-studio/vision)使用 `multimodal-generation/generation` 和 `image/text` 消息；既有试卷解析和评分适配不因此改变。两种供应商接口均拒绝截断或异常结束的结果，已返回的 token 用量仍保留在失败分析中。

请求使用 `schema_version: "answer-sheet-analysis-v1"`，并提供 `request_id`、`page`、`candidates`、`question_labels` 和 `writing_state`。`page` 包含从 1 开始的 `page_no`、实际像素 `width/height`、`media_type`、`data_base64` 和解码字节的 `sha256`。单页最多 8 MiB、4000 万像素，接受 PNG/JPEG/WebP 单帧图片；须先按浏览器预览方向渲染，未应用的 EXIF 方向或尺寸不符会被拒绝。本操作不做透视校正或旋转，坐标始终为来源显示页左上角起算的 `x/y/width/height ∈ [0,1]`。

每个可选候选包含 `candidate_id`、`bbox` 和 `evidence_refs`；最多 256 个，服务端同时检查印刷线框。`question_labels` 是可为空的印刷题号清单，`writing_state` 为 `blank/written/unknown`，不能据此推断学生某题是否空白。有学校指定的多模态模型时，无论是否检测到线框，都对整页执行一次语义分析；模型可忽略误检框，也可发现无框或漏检的作答区。没有线框不等于没有答案，仍会标记 `NO_GEOMETRY_CANDIDATES` 说明缺少几何证据。

网关从正式题型提供 `objective_question_labels`（单选、多选、判断题），旧内部调用可省略。连续选择题返回覆盖完整题号与选项格的 `objective_group` 大区；v7-evidence 同时返回 `objective_items`，包含每题印刷题号、单题边界和带字母标签的选项边界。大区自身保持未关联，子项经白名单、唯一性、选项及几何校验后自动对应正式题目，转成题区相对选项坐标。缺项或矛盾时保留大框供纠错，不按顺序猜题号、不均分坐标。原网格识别保留为纠错工具。

兼容协议的响应固定为 `status: "review_required"`，表示可编辑且尚未锁定的分析结果，不再表示所有结果都要逐框人工审核。`semantic_status` 分别记录 `completed/not_configured/failed/not_applicable`，每区 `semantic_check` 决定能否自动使用；内部诊断未传 `managed_model` 时只给未关联的几何候选，不借用默认评分模型。教师入口必须先由平台管理员在学校模型库分配答题卡模型，不要求额外测试图片检测。供应商失败、未知候选/页码、无效坐标或证据会丢弃整份语义输出并保留未关联几何框；成功时不把模型忽略的 CV 误检框重新补回。同题多框标为 `ambiguous`，不合并成大框。

模型发现的新区域由服务端生成稳定 `model-*` 编号，标记 `MODEL_DISCOVERED_REGION` 和复核原因；只有可靠印刷边界支持时才吸附精修，没有印刷边界、疑似手写布局和区域冲突都会明确提示。模型坐标仍只是候选，不把单张学生笔迹范围当成全体学生的公共模板。兼容消息使用图片 `detail=high`，模板专用输出预算至少 8192 tokens，不修改评分模型配置或增加调用轮数。

响应逐框返回 `page_no/candidate_id/printed_question_label/role/source_bbox/proposed_bbox/association_status/evidence_refs/reason_codes`，同时返回来源 hash、坐标空间、算法/提示词/schema 版本、非密钥模型身份和已知 token 用量。失败时仅返回安全错误码，外部超时可能仍已计费；本接口不承诺供应商调用恰好一次。

合成线框、已写页、无框回退、坐标、供应商适配及 HTTP 认证回归使用：

```powershell
$env:PYTHONPATH = "ai-services"
python -m pytest -q ai-services/tests/test_template_analysis.py
```

OpenCV、NumPy 和 Pillow 在 `requirements.txt` 中固定版本，与页面处理 worker 对齐；本服务直接包含算法，不跨服务目录导入。部署基础镜像需能安装这些图像依赖。

### 模板提示词对照与离线回放

部署变量 `EDUGRADE_TEMPLATE_ANALYSIS_PROMPT_VARIANT` 可选 `v6`、`v7-text`、`v7-evidence`，缺省为 `v7-evidence`。它只影响答题卡分析，不改变学校模型选择或增加供应商调用轮数。未知值返回固定 503，不静默选用另一组。`v7-text` 沿用旧输出 schema；`v7-evidence` 要求内部观察题号、有限视觉结构枚举和选择题子项，用于清理前检查矛盾。观察一致、正式题号/题型和几何有效的结果可自动使用；OCR 缺失不单独否定有效多模态结果，明确反证仍降级。模型观察不等于独立验证，内部观察不公开，校验通过的选择题子项公开供自动对应。

`v8-layout` 是另行验收的分阶段模式，默认值保持不变。整页先发现用途与粗框，再裁图读取题号和选项，最后用原图及编号图检查覆盖；正式考试题号清单只在服务端核对。局部选项坐标仅在实际选项像素、独立题号锚点和唯一行列对应同时成立时校准；缺格或矛盾不会按题数补框。模型按单选、多选拆组时，只有逐组无反证、全组选项和独立题号一致、连续实测框不跨其他用途，才合成一个完整选择题大区。身份字段只在独立静态标签与完整横线或闭合编号格唯一对应时校准；补读最多两个静态窄带，共享分析截止时间且总计最多四秒，不读取填写值作为类别证据。局部裁边说明片段只有在有效旧说明和唯一完整实测框共同佐证时去重，未知用途继续保留；裁边片段略带框侧白边时，还须原图证实白边没有印刷或笔迹才可去重。模型调用共用分析时限，局部修复至多三次、末轮收尾至多一次；失败保留原问题。普通诊断区分供应商、schema 和坐标恢复失败，schema 仅记录有限字段路径与校验关键字，原始响应仍须显式开启受控采集。单元测试、真实模型识别效果与部署状态分别验收。

恢复裁图坐标时，大区框和单题框仍严格校验；仅越出裁图的选择题选项保留原始坐标并标记为必须重测。只有独立像素对整组选项校准成功才可应用，否则保留旧区域，不能裁剪模型坐标来通过校验。

模板提示词单独登记在 [`prompts/template-analysis/manifest.v7.json`](prompts/template-analysis/manifest.v7.json)，与评分提示词登记分开。参考 schema 的摘要固定使用空候选编号和第 1 页；每次请求的实际 schema 摘要另记在诊断中。修改提示词或观察枚举后，在仓库根目录执行以下检查；有意更新登记时先用同一工具的 `--write` 正式生成，再评审差异。

```powershell
python scripts/evals/check_template_analysis_prompt_manifest.py
python -m pytest -q scripts/evals/test_template_analysis_prompt_manifest.py scripts/evals/test_replay_template_analysis_v7.py
```

普通诊断只保存 hash 和有限状态：`request_contract` 表示调用方构造的提示词/schema，`provider_request` 表示兼容转换后准备发送的模式及系统消息摘要。后者的 `transport_prepared` 不证明供应商已收到或支持请求。当前兼容路由实际为 `json_object`，原生视觉路由将 schema 放入系统消息；本地仍校验完整 schema。不能把“调用方传了 schema”描述成供应商已启用 strict。

[`scripts/evals/replay_template_analysis_v7.py`](../scripts/evals/replay_template_analysis_v7.py) 接收受控的 `{schema_version: 1, cases: [...]}` JSON；每例包含 `case_id`、`prompt_variant`、`request` 和 `model_output`。至多 16 例、文件至多 16 MiB；图片仍需满足原请求的 hash 和尺寸校验。它不调用供应商、OCR、队列或数据库，关闭继承的原文诊断，仅输出固定字段与白名单原因。使用方式见[回放说明](../scripts/evals/README.template-analysis-v7.md)。

v7 的源码默认值、运行部署和真实模型效果分别验收。离线回放只验收自动对应契约与错误处理，不提供识别准确率或框选质量结论；尚无真实模型 A/B 准确率结果，按需裁图与提高输入分辨率仍为独立实验。研究依据、实验分组及验收指标见[优化设计](../docs/architecture/answer-sheet-multimodal-optimization.md)。

### 学校模板库视觉匹配

`/answer-sheet/match` 使用 `schema_version: "answer-sheet-match-v1"`，接收网关已校验学校范围和摘要的 `source`、`candidates`。至多 8 个候选，每份文件不超过 8 MiB，总计不超过 24 MiB；支持 PNG/JPEG 和不超过 100 页的 PDF。候选声明的页数必须与实际文件一致，所有页面按原页序验证，不自动重排。

ORB/RANSAC 配准通过后，还会验证内点分布、投影覆盖及边缘结构；运行时提供 AKAZE 时才尝试该回退。结果为 `matched/ambiguous/unknown/unsupported`，并返回每个候选的证据分数和原因。分数不是准确率；候选未完整检查、格式不支持或服务失败时，网关不能把它当作 `unknown` 自动创建模板。此接口不读取模型凭据，不保存、锁定或绑定模板。

外部页面分析入口采用更小的 1 MiB/400 万像素限制，由浏览器缩放后提交；这些限制不同于上述内部单页分析协议。完整接线、验证记录及尚未实现的场景见[首批实施记录](../docs/engineering/answer-sheet-hybrid-implementation.md)。

## 配置与变更

模板静态禁区复核可选使用 `Dockerfile.template-ocr` 追加隔离 OCR 依赖层。先构建包含本次模板代码的常规 grading-agent 镜像，再分别通过 `GRADING_AGENT_IMAGE` 和 `TEMPLATE_OCR_IMAGE` 提供该镜像及兼容 OCR worker 镜像；两者均须为 Python 3.11 / x86_64，发布时使用不可变摘要并记录来源。该层不运行 pip、不修改主进程的 NumPy/OpenCV，Paddle 仅在 `-I -S` 子进程加载。

默认 `EDUGRADE_TEMPLATE_LOCAL_OCR_ENABLED=false`。完成目标镜像的真实字框、超时及非 root 烟测后才可启用。Compose 将 `EDUGRADE_TEMPLATE_LOCAL_OCR_MODELS_DIR` 只读挂载至 `/opt/template-ocr/models`，目录须包含 `PP-OCRv5_mobile_det` 和 `PP-OCRv5_mobile_rec` 各自的 `inference.json`、`inference.pdiparams`、`inference.yml`。若提供 `manifest.json`，其内容须为这六个相对文件路径到 SHA-256 的完整映射，加载前逐文件校验。缺文件、校验失败或运行时异常只会保留待核对区域；不会下载权重。

静态复核的边界、来源状态和测试证据解释见[区域审核说明](../docs/architecture/answer-sheet-region-review.md)。服务的 `/ready` 继续表达原有模型就绪状态，不代表这个可选子进程已经预热。

运行参数见仓库根目录的 [`.env.example`](../.env.example)。`EDUGRADE_GRADING_AGENT_TOKEN` 少于 32 个非空白字符时，服务拒绝启动。

监听环境缺省为 `production`。配置 `EDUGRADE_GRADING_AGENT_TLS_CERT_FILE` 和 `EDUGRADE_GRADING_AGENT_TLS_KEY_FILE` 时，两者必须同时提供，服务以 TLS 1.2 或更新版本监听。证书加载或 socket 包装失败会中止启动，通知就绪探测线程退出，最多等待一秒后关闭监听；已经进行中的模型探测仍受其自身超时约束。直接调用 `serve(Settings(...))` 也会在加载应用和绑定端口之前校验 token 与监听配置。

无证书 HTTP 仅供显式本机开发：`EDUGRADE_ENV` 为 `local`、`development`、`dev` 或 `test`，`EDUGRADE_GRADING_AGENT_ALLOW_INSECURE_LOOPBACK_HTTP=true`，且 `EDUGRADE_GRADING_AGENT_HOST` 是 `127.0.0.1` 等 IPv4 字面回环地址。三项缺一即拒绝启动；未知或空环境、`localhost` 等 DNS 名称、wildcard、远端地址和 IPv6 都不开放此例外。opt-in 环境变量仅接受小写 `true` 或 `false`，缺省 `false`；直接构造 Settings 时仅接受实际布尔值。配置完整证书后，opt-in 不会使服务回落 HTTP。

跨容器调用仍需绑定容器可达地址并配置服务证书，证书 SAN 应包含调用 URL 使用的 DNS 名称，调用方须信任签发 CA 并验证主机名。主 Compose 的证书挂载、私钥读取权限和旧环境配置迁移见 [部署说明](../infra/docker-compose/README.md)。`/health` 检查进程，`/ready` 另外检查模型就绪；两者都应使用带 CA 验证的 HTTPS 请求诊断。

使用 DashScope 原生模式时，将 `EDUGRADE_GRADING_ADAPTER_TYPE` 设为 `dashscope_native`，`EDUGRADE_GRADING_MODEL_BASE_URL` 设为 `https://dashscope.aliyuncs.com/api/v1`，并通过现有密钥或环境变量注入 `EDUGRADE_GRADING_MODEL_API_KEY`。提示词查看接口是只读的；变更提示词须创建新的 manifest 版本，经过评测和审批后再部署。
