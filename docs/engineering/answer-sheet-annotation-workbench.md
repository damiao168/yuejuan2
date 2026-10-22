# 答题卡标注工作台交付说明

答题卡模板编辑以画布为中心，直接显示在考试设置页面中，并支持“框一个选择题大区，解析内部真实填涂格”的流程。识别结果是候选；题号、选项方向和可复用范围仍由教师核对。草稿采用显式保存，未实现自动保存或自动发布。

## 工作台与实际操作

进入“答题卡设置”后使用普通页面布局，保留左侧导航和顶部考试步骤；全屏模式、侧栏收起入口和常驻“区域属性”栏已移除。工作区只有题目列表和画布，点击题目只定位对应题框。画布工具区提供“个人信息”和“识别选择题”；模板名称、本场确认、OMR 模式、精确坐标及校准打印设置由顶部“模板设置”按需打开，不会因选题自动展开。

| 操作 | 当前行为 |
| --- | --- |
| Ctrl + 滚轮 | 鼠标位于答题卡画布滚动区内时，仅缩放画布，并围绕鼠标位置调整视口；到倍率上限或下限仍阻止浏览器整页缩放。画布以外保留浏览器行为。 |
| 缩放按钮、倍率按钮 | 支持 50%～400%；减号、加号按 10 个百分点调整，点击倍率恢复 100%。100% 是工作台基准显示尺寸，不代表源文件像素一比一显示。 |
| 手形工具 + 按住左键 | 连续拖动画布；松开停止。平移受实际滚动范围约束，纸张小于视口时不能拖出额外空白。 |
| Space + 左键 | 临时切换为平移；松开空格或窗口失焦后结束临时模式。文本输入和弹窗中的空格保留原用途。 |
| 选择工具 | 单击题区选中，按住题区拖动可调整位置。 |
| 框选工具 | 先在左侧选题，再在纸张上拖出题区；同题重新框选会替换原题区。 |
| 边缘、四角控制点 | 仅选中题区显示控制点；移动和缩放限制在纸张范围，宽高最低为页面的 1%。 |
| 点击左侧题目 | 已配置题区时跳转对应页并定位到题区。 |
| 双击题区 | 根据可用视口调整倍率并居中，仍受 50%～400% 限制。 |
| 全部标注 / 仅当前区域 / 隐藏标注 | 控制题区、身份区和候选显示，方便核对原图；不删除已配置内容。 |
| Esc | 在编辑区域内取消未完成框选或拖动，恢复该次拖动前的框并清除当前选择。不是撤销历史操作。 |
| Ctrl + S | 焦点在编辑器内、可编辑且没有保存或识别任务阻塞时保存当前模板草稿；弹窗及其他页面区域不接管此快捷键。 |

未选中题区采用透明底色和细边框；选中时才增加浅色高亮、控制点及选项标签。待关联候选采用虚线，默认隐藏重复的问号标签。百分比输入位于按需打开的模板设置。缩放和平移只改变显示视口，不修改持久化坐标。

草稿未保存时会提示；离开、换文件或换版本前保留原有的未保存修改确认。待关联候选只保存在当前编辑会话中，关联为正式题区后才可随草稿保存。

## 上传、重复提示与删除

“上传答题卡”按钮只选择文件，当前文件名后面的垃圾桶图标用于“删除当前文件”，不再提供上传下拉菜单、单独的合并或手工排序操作。文件操作区按可用宽度排列，文件名优先省略；窄屏允许换行，不使用横向滚动隐藏按钮。重复检查以本场考试、答题卡用途和内容指纹为准，已上传的原图或整套来源会提示重复；文件同名但内容不同可以上传。服务端对同场同用途的内容哈希再做幂等检查，防止两个浏览器同时登记出两份来源。

多个文件上传先解码、检查格式及重复，再生成最长边 1000、单页不超过 128 KiB 的 JPEG 缩图，交给学校配置的答题卡多模态模型分析。页序优先依据印刷页码；没有页码时使用实际题号范围、版面与跨页衔接，不要求每页都有印刷总页数。模型每批最多分析 8 页，跨批次只合并页码或题号范围能明确衔接的结果；不能用批次或文件名顺序打破歧义。全批返回的页 ID 必须完整、唯一，然后按返回顺序实际重排 PDF 页面，不同文件中的 PDF 页面也可以交叉重排。

模型调用失败、输出格式错误、重复页、不同卷面或证据不足分别提示原因。确实无法判断时停止本批上传，可换清晰图片重试或上传页序正确的单个 PDF，不提供手工排序列表。单个 PDF 保留原有页面顺序，不额外调用排序模型。能够排列已选页面不代表已确认总页数完整，缺少总页数证据时标记 `PAGE_COUNT_NOT_VERIFIED`；模板题区覆盖仍由后续确认流程检查。页序结果也不代表已核验学生身份。

删除需要明确确认，只有服务端成功才清理当前选择并重载列表。删除会同时软删除本场来源登记及其可编辑模板草稿；已有锁定模板、学校库、考试绑定、正式题目或导入引用时阻止删除。共享文件资产不会被直接删除。考试冻结或请求作用域变化时不能继续删除；失败保留当前来源和草稿。

## 姓名、学号与个人信息区域

整页多模态分析分别返回题目区域与身份区域。姓名、学号、准考证号、考号、班级、学校、条码、二维码及无法细分的身份信息，通过 `identity_label` 传递字段类别；不返回学生实际填写值作为字段标签。只识别本页确实存在的字段，身份框覆盖完整填写空间或编号格，不能只圈标签或当前学生笔迹。身份框不使用题号引用，不计入题目覆盖率，也不作为待关联题目展示。

识别出的身份区域保存在各页的 `identity_regions`，与题区一起随草稿保存、克隆和复用。画布工具区的“个人信息”可定位、添加、调整及删除身份框；画布支持与题区相同的移动和控制点调整。再次识别保留已有身份框，教师可先删除不适用的旧框，再补充新框。“清空旧题区”不删除身份区域。

这些字段定义模板上的身份信息位置，不代表已经读出或确认某位学生的学号。正式学生关联仍遵循既有条码、考试名单和人工复核流程；没有识别到身份区时不猜测学生身份。

## 选择题大区的处理流程

1. 在画布工具区点击“识别选择题”，拖出包住真实选项的一个大框。设置每题选项数（2～12）及选项横排或竖排；选项数不同的题目分区处理。
2. 点击“解析网格”。OpenCV 检测轮廓、大小、共线关系及间距，返回实测题组和选项框。开放的 `[A]` 格型在闭合轮廓没有可靠结果时，回退到成对括号笔画与真实填黑连通域组合；每组至少有两个可证括号格。
3. 查看叠加预览，逐组核对题号及选项顺序。可选的本地 OCR 只提供题号提示，不自动关联，也不请求多模态模型。
4. 全区没有漏检标记时，可按所选题目和阅读顺序生成初始映射，再人工核对。有漏检标记时禁止顺序批量映射，必须为每个完整题组单独选择正式题号，避免漏掉一题后整列错号。
5. 勾选核对确认后“应用到草稿”。部分检测还须确认只应用完整题组、漏题另行补框。已有同题区域被替换前会要求确认；范围外题区、身份区与新框冲突时阻止应用。
6. 漏检题在主画布选题并手工框选，再在“高级设置 · 单个选项”添加、调整每个选项。手工添加给出的初始位置只是编辑起点，不是识别证据。最后点击“保存草稿”或 Ctrl + S。

解析不根据题数等分大框，不生成缺失选项，不推断被遮挡格子的坐标。裁断、重叠或组内不完整等阻塞原因需要重新框选或手工配置。题目数一致也不能证明映射正确；系统校验只能补充人工核对。

## 保存、锁定与学校模板库

“应用到草稿”只更新当前编辑状态。“保存草稿”持久化布局；“确认答题卡设置”按原有确认流程保存、锁定并设置本场使用。锁定后需要克隆新版本才能继续改框。

复用通过页面上方的学校模板库完成：先准备经过核对的空白来源，确认无学生姓名、学号、条码或手写答案，并确认各页题区适用于同版式答题卡，然后“核对并收录模板库”。收录会保存并锁定草稿；收录与本场考试启用分别确认。收录失败时保留已经完成的锁定结果，可继续重试收录。

后续上传按已有模板匹配流程寻找适用版式，也可从学校模板库选择“用于本场考试”。复用时创建本场草稿并映射当前考试题目；仍需核对来源、题号和区域。已填写答题卡可以辅助生成候选，其个人笔迹分布不能直接作为全校通用模板发布。

## 工程边界与坐标兼容

- 题区与解析响应中的框均为来源显示整页上的 0～1 坐标。应用时将选项坐标转换为所属题区内的 0～1 坐标，并保存 `option_coordinate_space: "question_relative"`，避免同一组坐标被重复换算。
- Go 模板校验只接受空标记或 `question_relative`；锁定事务将标记与选项框一起同步到正式题目的 `answer_area`。就绪快照也包含标记；学校模板复用保留该坐标含义。
- 评分启动优先识别显式标记，直接把题区内选项交给既有 OMR Worker；无标记的历史数据保留原有兼容换算逻辑。本次没有重写历史模板或新增数据库迁移。
- 网格接口独立于学校视觉模型配置，不调用聊天或多模态接口。网格候选不能授予正式 OMR 自动确认资格；既有人工复核、参考图、阈值及校准审批规则继续生效。
- 服务端核对租户、考试访问范围、可编辑状态、模板版本和来源哈希；请求前后重新核对快照。前端换页、换来源、换版本或失去权限时取消请求并丢弃旧候选。浏览器取消会终止 Go 等待，Python 当前同步 CV 运算仍会完成其有界任务。
- 网格专用页面图最长边 2300、最多 400 万像素，优先使用不超过 1 MiB 的无损 PNG，超限才尝试 JPEG 压缩；与整页多模态输入分别生成，哈希对应最终发送的字节。服务端最多返回 500 个组、总计 2000 个选项，返回坐标须位于人工大框及所属题组内。

## 本次验证与已知限制

本地真实样本为一页含手写及红色批注的数学答题卡，共 11 道选择题：单选 1～8，多选 9～11。采用 Poppler 渲染的 2300 × 1628 PNG 和人工大框，原闭合轮廓算法返回 0 组；加入开放括号回退后返回 9 个完整题组、36 个实测选项框，已检查叠加图，第 2、5 题需手工补充。正式浏览器使用 PDF.js 渲染的 2300 × 1627 图像，JPEG（quality 0.85）和最终无损 PNG 均返回 8 组，第 2、5、9 题需手工补充。最终 PNG 为 498840 字节，自动聚焦为 345%。不同渲染、编码及扫描质量会改变细小括号证据；这些结果仅对应各自实测输入，不是所有上传文件或所有格型的准确率承诺。

AI 定向测试共 102 个通过，运行命令为：

```powershell
# 在 ai-services 目录运行
python -m pytest tests/test_choice_grid.py tests/test_template_analysis.py tests/test_template_matching.py -q
```

其中覆盖开放括号、空心及涂黑格、缺格、红色批注、粘连不补格、横竖排列、坐标边界、输入拒绝及不调用模型。Go 定向测试另覆盖网格端点权限、前后快照变化、响应边界与请求取消。

## 定向核对的源码范围

下表记录本文实际核对的实现或相关片段，不表示全仓或这些文件全部业务已完成审计。

| 核对内容 | 源码（仓库相对路径） |
| --- | --- |
| 工作台入口、未保存提示 | `apps/web-admin/src/pages/AnswerSheetTemplatePage.tsx` |
| 上传去重、危险删除与实际页序重排 | `apps/web-admin/src/features/answer-sheet-template/editor/useAnswerSheetSourceUpload.ts`、`useAnswerSheetSourceDelete.ts`、`mergeAnswerSheetSources.ts`、`orderAnswerSheetSourcePages.ts`（同目录） |
| 身份区识别、保存及独立编辑 | `apps/web-admin/src/features/answer-sheet-template/editor/analyzeTemplatePage.ts`、`useRegionSuggestion.ts`、`templateLayoutReducer.ts`、`identityRegionFields.ts`（同目录），`view/IdentityRegionPanel.tsx`（同 feature 下） |
| 工具、快捷键、属性与手工编辑 | `apps/web-admin/src/features/answer-sheet-template/view/TemplateWorkspaceView.tsx`、`editor/useRegionEditor.ts`（同 feature 下） |
| 局部缩放、边界平移 | `apps/web-admin/src/features/answer-sheet-template/editor/useTemplateCanvasZoom.ts`、`useTemplateCanvasPan.ts`（同目录） |
| 普通布局、独立滚动与标注样式 | `apps/web-admin/src/features/answer-sheet-template/view/template-confirmation.css`、`apps/web-admin/src/styles/features/exam-preparation.css` |
| 网格状态、部分应用、原子更新 | `apps/web-admin/src/features/answer-sheet-template/editor/useObjectiveGrid.ts`、`objectiveGridGeometry.ts`、`templateLayoutReducer.ts`（同目录） |
| 网格 UI 与专用图像 | `apps/web-admin/src/features/answer-sheet-template/view/ObjectiveGridDrawer.tsx`、`apps/web-admin/src/features/answer-sheet-template/editor/objectiveGridPageImage.ts` |
| 显式保存、锁定与收录 | `apps/web-admin/src/features/answer-sheet-template/editor/useTemplateLifecycle.ts`、`apps/web-admin/src/features/answer-sheet-template/library/useTemplateLibrary.ts`、`TemplateLibraryPanel.tsx`（同 library 目录） |
| 网格协议、轮廓与开放括号 | `ai-services/grading_agent/choice_grid.py`、`choice_grid_schema.py`、`choice_grid_detection.py`、`choice_grid_brackets.py`、`server.py`（同目录），`ai-services/tests/test_choice_grid.py` |
| Go 端点、调用与快照 | `services/api-gateway/internal/paper/template_objective_grid.go`、`template_objective_grid_client.go`、`template_objective_grid_test.go`、`template_page_analysis.go`（同目录），`services/api-gateway/internal/server/routes_paper.go` |
| 删除引用保护、多模态页序与有界请求 | `services/api-gateway/internal/paper/answer_sheet_sources.go`、`answer_sheet_page_order.go`（同目录），`ai-services/grading_agent/answer_sheet_page_order.py` |
| 坐标持久化与 Worker 输入 | `services/api-gateway/internal/paper/template_validation.go`、`store_postgres_configuration.go`、`readiness_snapshot.go`（同目录），`services/api-gateway/internal/grading/scoring_run_start.go` |
| 保留的 OMR 复核及校准入口 | `apps/web-admin/src/features/answer-sheet-template/view/OMRCalibrationView.tsx`、`apps/web-admin/src/features/answer-sheet-template/calibration/useOMRCalibration.ts` |
