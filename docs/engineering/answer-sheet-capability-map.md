# 答题卡技术能力与接入地图

核对日期：2026-10-16。本文解释模板建立、学生答卷处理和本次图像入口修复的关系，依据下列函数体、调用处和定向测试。未列出的文件不因此视为已审读；本文不是全仓验收声明。

项目已经具备图像质量检测、几何配准、模板匹配、按题裁剪、条码身份和 OMR。截图中的问题来自模板编辑器的入口限制与文件组织方式，不能据此判断项目没有这些技术。

## 1. 两条流程必须分清

| 场景 | 输入与结果 | 当前实现边界 |
| --- | --- | --- |
| 建立模板 | 一套示例答题卡 → 各页题目候选框 → 人工核对 → 保存、锁定、用于本场考试 | 本次补齐多文件合并、图片/扫描 PDF 的印刷框检测和题号 OCR。输出仍是候选，不能自动等同确认完成。 |
| 处理学生答卷 | 已建立模板 + 批量学生答卷 → 质量检测 → 模板路由 → 几何配准 → 复用已确认 ROI 裁题 | 已有 Python worker 和 Go 队列、结果提交、租约及版本校验。正常同版答卷不需要重新逐题画框。 |

模板编辑器只负责建立、核对几何区域。学生答卷运行链负责把后续页面对齐到该几何坐标。新入口保存相同的 `layout.pages[].page_no/question_regions`，不另建一套评分裁剪协议。

## 2. 已有且已接入业务链的能力

| 能力 | 实际技术与调用依据 | 重要限制 |
| --- | --- | --- |
| PDF、图片、多帧文件解码 | [`decoder.py`](../../services/page-processing-worker/page_processing/decoder.py) 的 `decode_document` 使用 PDFium/Pillow；[`runner.py`](../../services/page-processing-worker/page_processing/runner.py) 的 `_process_capture` 遍历全部解码页，上传页面与条码观测；`_process_paper_import_decode` 遍历资料和页面 | 有页数、每页像素、累计像素限制。运行链能遍历多页，不代表原模板上传控件支持一次选多张图片。 |
| 图像质量、方向与有限纠偏 | [`engine.py`](../../services/image-quality-worker/image_quality/engine.py) → [`geometry.py`](../../services/image-quality-worker/image_quality/geometry.py)：EXIF 方向、Canny、Hough 长线倾斜、轮廓四边形、边框完整度及透视风险；[`runner.py`](../../services/image-quality-worker/image_quality/runner.py) 上传标准化图后提交结果 | 自动纠偏只在角度与证据满足阈值时执行；不是任意透视矫正或弯曲展平。报告中的 `anchor_detection_status`、`curvature_status` 仍是 `not_evaluated`。纸张外边框也不是每题答题框。 |
| 已知模板几何配准 | [`registration.py`](../../services/page-processing-worker/page_processing/registration.py) 的 `register_page`：ORB，失败后 AKAZE；Hamming 匹配、比率筛选、RANSAC 单应矩阵、`warpPerspective`；记录正逆矩阵、内点、误差和覆盖指标 | 内点不足、误差过大或覆盖不足时失败；人工四点是恢复入口。`coverage` 是变换四角面积比，并非对目标页面实际交集的完整性证明。 |
| 静态结构守卫及已有模板候选匹配 | 同文件 `inspect_template_guard` 遮掉已配置答题 ROI 后比较 pHash/Canny 静态结构；`match_template_candidates` 结合结构与配准证据，保留 `matched/unknown/ambiguous`。worker 的 `_guarded_registration`、`_process_template_match` 已调用 | 守卫遮罩降低已填写答案的影响，不能保证所有手写、遮挡都可匹配；候选是已经存在的模板，不能凭此从零生成未知版面模板。 |
| 配准后复用 ROI 裁题 | worker `_process_registration` → `crop_regions`，使用模板的归一化坐标裁出 `answer_segment_crop` 并回传题目 ID、像素框和文件哈希 | 裁题依赖先有正确模板与题目关联；不会凭手写答案内容猜题号。页面没有对应模板页时不能套用另一页坐标。 |
| 模板路由与多页排队 | [`registration_store_postgres.go`](../../services/api-gateway/internal/capture/registration_store_postgres.go) 的 `QueueSubmissionPagesInTx`、`queueRegistrationPagesTx`、`queueRegistrationPageTx`：逐页排队，`assigned_page_no` 对应模板 `page_no`，携带模板内容哈希 | 优先使用已验证纸张模板身份，其次考试绑定，再考虑唯一锁定模板。质量通过且有标准化图的页面才进入这条配准队列。 |
| 首张答卷匹配与考试绑定 | [`template_match_store_postgres.go`](../../services/api-gateway/internal/capture/template_match_store_postgres.go) 查询同考试最多 8 个已锁定模板，只取同一页码候选；匹配成功在事务中绑定并继续排配准 | 未命中、歧义或与已有绑定冲突转复核。不能称作支持任意乱序分页或未知模板自动学习；候选比较不自动改写已锁定考试模板。 |
| 可信页码与学生纸张身份 | [`barcode.py`](../../services/page-processing-worker/page_processing/barcode.py) 用 ZXing 提取观测；Go [`barcode.go`](../../services/api-gateway/internal/capture/barcode.go) 验证 HMAC 声明；[`store_postgres.go`](../../services/api-gateway/internal/capture/store_postgres.go) 的 `applyDecodedPageTx` 优先采用已验证页码，否则从文件内 `SourceIndex` 起步 | 普通现成条码不自动成为项目可信身份。模板级声明与学生级声明有不同版本；二维码检测不等于学生身份已确认。无可信页码时，文件顺序只是起点。 |
| 版本锁定及并发保护 | [`store_postgres_template_binding.go`](../../services/api-gateway/internal/paper/store_postgres_template_binding.go)：只有 locked 模板可绑定，校验修订游标；解绑后保留游标，避免旧请求复活。模板匹配使用同一锁顺序 | 锁定模板是已确认事实。保存草稿不会自动使区域核查和本场使用设置完成；后续更改需要新版本。 |
| 客观题 OMR | [`omr.py`](../../services/page-processing-worker/page_processing/omr.py)：阈值二值化、框内填涂比例、强度间隔；`template_difference` 模式去除空白参考前景。worker `_process_omr` 校验参考 SHA、提交 overlay 与决策 | 需要选项 ROI 和有效 profile。空白、多选、歧义保留复核；规则置信值不是校准正确率。填写过的示例卡不能未经检查当作空白差分参考。 |
| 服务端文字与公式识别 | [`engine.py`](../../services/ocr-worker/ocr_worker/engine.py) 使用 PP-OCRv5 mobile/server；[`runner.py`](../../services/ocr-worker/ocr_worker/runner.py) 有页面/区域 OCR 和试卷导入 OCR 任务；[`__main__.py`](../../services/ocr-worker/ocr_worker/__main__.py) 按配置装配运行器 | 这是后台识别任务，原模板编辑器没有直接调用它生成图像题框。代码存在和单元测试通过不证明当前部署已启动 worker 或已加载真实模型。 |
| 公式版面与公式扩裁 | [`math_layout.py`](../../services/ocr-worker/ocr_worker/math_layout.py) 通过 Paddle `LayoutDetection` 使用配置的 PP-DocLayout 模型，**只接受 label 为 `formula` 的框**，随后自适应扩裁和合并；`__main__.py` 接入数学理解/试卷公式流程 | 不是每题答题框检测器，更不能因模型名带 Layout 就说已实现 YOLO 答题卡检测。数学与试卷公式 runtime 受独立配置开关控制；默认关闭不等于缺少代码。 |

## 3. 截图为何只允许 PDF 文字层辅助

修复前的 `useRegionSuggestion` 只接受 `pdfRef`，读取每页 `getTextContent`，按题号文字的纵坐标估计一个整页宽候选；没有像素边框检测或图片 OCR。`TemplateWorkspaceView` 又以 `preview.textLayerAvailable` 控制显示按钮，PNG 和扫描 PDF 因没有文字层进入“手动设置区域”的分支。

原代码实际上已经遍历 PDF 全页；问题包括图片无法进入该路径，以及每张图片被登记成独立 `exam_paper` 来源，导致两张图片显示为不同版本而不是同一套的第 1、2 页。左边选择的是来源版本，右边选择的是区域模板版本，属于两种实体。二者同时出现“版本/草稿”但缺少说明，造成截图中的重复草稿观感；并非同一文件天然需要两个独立草稿。

这与后台已有配准不矛盾：配准消费**已确认模板**，不能替代第一次模板的区域建立。原编辑器也没有把其示例文件送进学生提交的质量检测、配准队列，因此后台算法不会自动出现在该页面上。

## 4. 本次图像入口怎样复用现有领域结构

| 修复位置 | 工作区中的实现与边界 |
| --- | --- |
| 批量来源组织 | [`mergeAnswerSheetSources.ts`](../../apps/web-admin/src/features/answer-sheet-template/editor/mergeAnswerSheetSources.ts) 按所选顺序合并 PDF 页和图片为一个 PDF 来源；图片不重新压缩，JPEG EXIF 方向转为页面坐标。当前限制 50 个文件、100 页、100 MiB；单张图片保持原文件。任一文件失败不登记部分结果。 |
| 上传与失败恢复 | [`useAnswerSheetSourceUpload.ts`](../../apps/web-admin/src/features/answer-sheet-template/editor/useAnswerSheetSourceUpload.ts) 将合并、上传、登记作为一个受身份/考试/权限保护的批次。上传成功而登记失败时保留资产 ID，只补登记；不会重传整批。 |
| 像素框候选 | [`answerSheetImageRegions.ts`](../../apps/web-admin/src/features/answer-sheet-template/editor/answerSheetImageRegions.ts) 在有界栅格中提取水平/垂直长线及闭合框，检查四边支撑、消除粗线重复和大外框。它是浏览器规则算法，**没有调用 Python OpenCV，也没有加载 YOLO**。明显倾斜、透视或缺失整边时宁可不建议。 |
| 题号证据 | [`answerSheetOCR.ts`](../../apps/web-admin/src/features/answer-sheet-template/editor/answerSheetOCR.ts) 使用随应用打包的本地 Tesseract Worker 和英文数字资源；文字层优先。OCR 用来读题号，不读学生作答来猜题号；[`answerSheetRegionMatching.ts`](../../apps/web-admin/src/features/answer-sheet-template/editor/answerSheetRegionMatching.ts) 将框内、靠近上缘的题号与本场题目匹配。裸手写数字和重复歧义不会直接得到确定关联。 |
| 连续页候选生成 | 点击“开始识别区域”后，[`useRegionSuggestion.ts`](../../apps/web-admin/src/features/answer-sheet-template/editor/useRegionSuggestion.ts) 顺序遍历整份 PDF 的全部页，逐页报告进度；上传、打开草稿及翻页不启动识别。有框时优先按框处理，缺框且有文字层时保留旧题号锚点回退。某页失败继续后页，最终明确列出失败页；已配对部分可先保存，不覆盖已有人工区域。没有可靠题号的框以独立候选暂存，手动关联后才进入可保存的布局；离页不持久化这些未关联框。 |
| 正反面与重复来源 | 预览按文件真实页数分页，两页文件提供正面、反面入口；历史漏页草稿补充空页，已锁定版本保持不变。分开上传的历史图片可由用户明确选择正反面并组合为新来源，原文件保留。上传以 SHA-256 拦截本批重复和已有相同内容；同名不同内容不误判。新合并 PDF 使用固定元数据，使相同整套内容具有稳定哈希；历史动态元数据产物无法仅凭旧哈希追溯配对。 |
| 保存与后续答卷复用 | [`useTemplateLifecycle.ts`](../../apps/web-admin/src/features/answer-sheet-template/editor/useTemplateLifecycle.ts) 建立同一来源的多页 layout；确认入口依次保存、锁定、绑定考试。三步并非跨接口事务，部分成功要保留事实并提示重试。后续学生答卷仍由既有 Go/Python 配准链复用 ROI。 |

这条浏览器路径用于第一次建立模板，避免为了数字题号检测而伪造一份学生提交。后续若把现有 Python 质量或 OCR 复用于编辑器，应新增清楚的模板来源分析任务与权限、资源、版本边界；当前不能把浏览器入口描述成已经调用了后台 worker。

## 5. 已有底层但尚未直接接到模板建立入口的能力

- 图像质量 worker 能检测倾斜、透视和纸张外轮廓，但模板编辑器当前的浏览器图像分析没有先跑它；轴对齐长线候选不能自动继承后台的纠偏保证。
- PaddleOCR 已有整页及区域文字块能力，但后台任务协议面向提交/试卷导入。当前模板题号 OCR 走本地 Tesseract，不等于后台 OCR 已迁移到该入口。
- ORB/AKAZE 配准可以复用已确认模板的坐标，但输入必须有目标模板页面。不能把“匹配了一个已有模板”和“从未知页面理解各题结构”合并成同一能力。
- PP-DocLayout 接入当前明确筛选公式类；其输出若要用于答题区域，必须另有适用类别、数据验证及题号关联规则，不能直接改名复用。

## 6. 仍缺少或需要专门验证的能力

1. 在本次检索的 Go/Python/TypeScript 业务源及 worker 依赖声明中，没有找到 `YOLO`/`ultralytics` 的答题框推理、适配器、模型权重配置和类别定义。此结论限定检索范围，不能推广成项目没有相关图像技术。现有 PP-DocLayout 公式模型不能凭名称算作答题卡 YOLO 实现。
2. 若引入 YOLO，需要可用的答题卡标注数据、真实模型、类别/坐标协议、模型版本和真实样本评测；下载通用目标检测模型或写一个空适配器不构成交付。本次没有宣称这条路径已经实现。
3. 无边框、弯曲页面、严重透视、印刷题号被遮挡、跨页同题、多题共享不规则区域，不能由当前闭合框启发式可靠解决。后端当前还限制一个 `question_id` 只有一个模板 ROI；跨页同题需要领域模型和裁剪/评分协议一起设计。
4. 无可信条码的任意乱序多学生扫描、学生身份自动分册、自动判断两张现存独立来源属于同一套，不能仅靠多图上传或按页遍历解决。新上传批次按选择顺序建立一套模板，不自动重组历史来源或替用户推断学生归属。
5. 已填写的学生示例卡可用于确定印刷边框与几何布局，但真实照片、手写覆盖、圆角边框等成功率须用经授权的代表性样本测量。合成图单元测试不是实际试卷准确率证明。

## 7. 本次定向验证及阅读范围

2026-10-16 在现有本机 Python 3.11 环境串行运行以下既有测试，未安装新依赖，未启动全仓测试或真实 Paddle 模型：

| 工作目录 | 命令 | 实际结果 |
| --- | --- | --- |
| `services/page-processing-worker` | `python -m pytest -q tests/test_registration.py tests/test_decoder.py tests/test_runner_template_match.py tests/test_omr.py tests/test_runner_omr.py` | 33 passed；覆盖合成图配准与真实像素裁剪、解码、已有候选模板匹配、OMR及 worker 装配。 |
| `services/image-quality-worker` | `python -m pytest -q tests/test_engine.py tests/test_quality_measurement_boundaries.py tests/test_runner.py` | 31 passed；覆盖质量/几何边界及 worker 流程。 |
| `services/ocr-worker` | `python -m pytest -q tests/test_engine.py tests/test_recognition_router.py tests/test_runner.py` | 44 passed，15 subtests passed；OCR 引擎适配和路由使用测试替身，不证明真实模型已部署或识别准确率。 |

测试时仅在各子目录为当前进程设置 `PYTHONPATH`。这些结果证明上述受测行为，不能替代真实图片 OCR、浏览器整链、生产数据库事务或实际部署连通性测试。本次浏览器入口的新测试由对应实现批次记录，不计入上述既有后台测试数量。

逐文件研究记录如下。`完整`只表示本次实际审读全文；`部分 / pending`保留没有全文审读的边界，不计入全仓完成数。

| 文件 | 实际阅读范围 |
| --- | --- |
| `services/page-processing-worker/page_processing/registration.py` | 完整：guard、候选匹配、自动/人工配准、裁剪与坐标辅助。 |
| `services/page-processing-worker/page_processing/runner.py` | 完整：任务路由、全部处理器、失败报告与心跳。 |
| `services/page-processing-worker/page_processing/decoder.py`、`barcode.py` | 完整：页面/帧循环、资源限制与条码观测。 |
| `services/page-processing-worker/page_processing/omr.py` | 部分 / pending：`extract_marks`、profile、测量/选择、参考图裁剪；未全文审读剩余图像辅助函数。 |
| `services/image-quality-worker/image_quality/engine.py`、`runner.py`、`geometry.py` | 完整：质量/纠偏调用、报告、几何与 worker 提交。 |
| `services/ocr-worker/ocr_worker/math_layout.py` | 完整：模型调用、formula 标签筛选、扩裁和合并。 |
| `services/ocr-worker/ocr_worker/engine.py` | 部分 / pending：初始化、readiness、Paddle 模型装配与文字结果适配入口；未全文审读尾部辅助函数。 |
| `services/ocr-worker/ocr_worker/runner.py`、`__main__.py`、`config.py` | 部分 / pending：提交/区域与试卷 OCR 路由，数学/公式运行器装配，模型及开关配置；未全文审读其余辅助和主循环。 |
| `services/api-gateway/internal/capture/registration_store_postgres.go` | 部分 / pending：入口至配准任务创建、页面筛选与模板路由；未全文审读后半结果落库代码。 |
| `services/api-gateway/internal/capture/template_match_store_postgres.go` | 部分 / pending：候选排队、匹配结果及事务绑定；未全文审读失败聚合辅助。 |
| `services/api-gateway/internal/capture/barcode.go` | 完整：模板/学生声明版本、签名、校验。 |
| `services/api-gateway/internal/capture/store_postgres.go` | 部分 / pending：`applyDecodedPageTx` 与 `assigned_page_no` 来源；其余只检索相关调用。 |
| `services/api-gateway/internal/paper/store_postgres_template_binding.go`、`template_binding.go` | 完整：绑定、解绑、修订游标和模式规范化。 |
| `services/api-gateway/internal/paper/template_validation.go` | 部分 / pending：页码/区域/题目跨页判重及就绪检查入口。 |
| 浏览器 `editor/answerSheetImageRegions.ts`、`answerSheetRegionMatching.ts`、`answerSheetOCR.ts`、`mergeAnswerSheetSources.ts`、`useAnswerSheetSourceUpload.ts`、`useRegionSuggestion.ts`、`useTemplatePreview.ts`、`useTemplateLifecycle.ts`、`saveTemplateDraft.ts` | 完整：本文所述当前工作区函数体；实现仍可由并行修复继续更新。 |
| `editor/useTemplateEditor.ts`、`view/TemplateWorkspaceView.tsx` | 部分 / pending：候选生成接线、旧版本文字层门槛与来源/模板选择控件。 |
| 三个 worker 的 `pyproject.toml` | 完整：运行依赖与测试配置。 |
| `page-processing-worker/tests/test_registration.py`、`test_runner_template_match.py` | 完整：合成配准、ROI、守卫、歧义与 worker 结果断言。其余上述已运行测试未因此标为全文审读。 |

