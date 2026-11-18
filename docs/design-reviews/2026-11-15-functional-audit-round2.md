# 2026-11-15 功能复审

上一批的混合重复上传组恢复、聊天 PDF 文字层校验和发布路由校验已修复并部署到本地。见 [首轮功能扫描及修复记录](2026-11-15-functional-scan.md)。下列编号和复现保留修复前的行为。

## 修复记录

17 项均已实施修复。独立复审发现的第 6、11 项两处 P2 边界缺口也已补齐。本地 API 和管理端已更新到 `local-functional-followup-20261115`，主观题 Worker 保留上一批已验证版本。数据库已备份并迁移至 `000233`，18 个原有服务继续运行，健康检查与主要入口通过，运行产物哈希与最终构建一致。 [本次部署结果](../../output/functional-fixes-followup-20261115/deployment-summary.json) · [运行产物核对](../../output/functional-fixes-followup-20261115/runtime-verification.json)

### 修复后复审

- **P2 · 第 6 项草稿恢复后的采纳事实丢失，已修复。** 原实现只靠内存记录采纳，保存并刷新后可能误交 `accepted:false`。现在复用本地评分草稿及服务端 `viewer_state.grading_ai_adoption`，保存任务、模型建议 ID、建议分和明确的采纳决定；单题、整区保存及交分均携带有效事实，撤销恢复原事实，冲突合并随分数选边。手工改分清除旧来源，缺失、旧 null、损坏或模型换版记录保留未知。当前答案及建议版本继续由既有展示和交分门禁核验。真实 PostgreSQL 往返、版本冲突与实际 hook 回归通过。 [实现](../../apps/web-admin/src/features/grading/workbench/aiAdoptionDraft.ts) · [逐文件复核](../../output/functional-fixes-followup-20261115/adoption-coverage.json) · [数据库回归](../../output/functional-fixes-followup-20261115/adoption-postgres-tests.txt)
- **P2 · 第 11 项二维码可能覆盖底图说明，已修复。** 缺少专用二维码区时停止自动选位，必须在真实原页预览上逐页确认可覆盖位置。位置与源文件哈希冻结在打印批次，不修改锁定模板；签发、补打和下载使用同一几何与底图预检。旧批次可首次确认，相同内容允许重放，不同内容拒绝；已扫描、冲突、作废或扫描持锁时拒绝改位。预览沿模板归属及 `exam:manage` 读取经哈希校验的底图，不依赖通用文件管理权限。切换模板、来源或撤回权限后，迟到请求不会继续下载或沿用旧确认。原页标题、题目均保留，二维码已成功解码。 [修复后 PDF](../../output/functional-fixes-round2-20261115/confirmed-source-header.pdf) · [浏览器证据](../../output/functional-fixes-followup-20261115/browser-qa.json) · [真实路由与 PG 回归](../../output/functional-fixes-followup-20261115/print-source-backend-tests.txt)

本次阅卷前端 97 项、打印及预览前端 130 项回归通过；最终小改另补跑 23 项阅卷用例，未重复计入 97 项。capture 最终全包、打印生命周期 PostgreSQL E2E、两个新增入口各 6 个实际路由权限场景通过。管理端生产构建（含 TypeScript）、API Linux 构建、SDK、路由覆盖、授权契约及数据库版本检查通过。全仓 lint 无错误，保留其他既有修改中的 37 条警告；本批文件定向 lint 通过。 [验证索引](../../output/functional-fixes-followup-20261115/verification-summary.json) · [后端逐文件复核](../../output/functional-fixes-followup-20261115/print-layout-backend-coverage.json) · [前端逐文件复核](../../output/functional-fixes-followup-20261115/print-layout-frontend-coverage.json)

其余 15 项在本次关键调用链复核中未发现确定的遗留缺陷。本次重新运行题库与材料的 69 项测试、主观题 Worker 的 46 项测试，均通过。已核对部署产物哈希，运行中的 API 二进制、管理端首页和 Worker 代码与本轮构建一致。9 模块 Go 与 3 条端到端结果沿用上一批已保存日志，本次未重跑。 [本次前端回归](../../output/functional-fixes-review-20261115/questionbank-materials-tests.txt) · [本次 Worker 回归](../../output/functional-fixes-review-20261115/worker-tests.txt)

Windows 原生安装包及实体打印回扫继续保留待验收状态。以上结论覆盖本次修复及明确记录的调用链，未审读的全仓文件仍为 pending，不代表所有功能均无缺陷。

| 问题 | 修复后的处理 |
| --- | --- |
| 1–3 收卷 | 人工确认在同一事务同步正式页号和答卷质量。重确认保留有效产物，也能修复旧的占位页、失效后仍显示就绪的页面。实际改号作废旧配准及其租约。v1 条码补齐同场冻结模板的哈希，并拒绝冲突证据。 |
| 4–6 申诉和报表 | 原仲裁教师纳入回避名单。申诉复评创建持久记录所属申请，重复请求在同一申请、同一来源版本下回到原任务。AI 采纳率只使用可核实的模型建议交分记录，既有记录缺少事实时显示不可评估。 |
| 7–10 上传、题库、账号和统计 | 桌面上传从读取加密原件前开始占用队列项，跨账号后旧批量命令终止。题库预览只接受当前选择的结果。设备撤销以事务返回值决定是否清除当前 Cookie。仪表盘按组织管理范围汇总，个人任务范围不参与组织查询。 |
| 11–14 打印、材料和独立评分 | 打印 PDF 导入冻结底图，签发及补打在入账前校验版式、底图与总页数。材料恢复按实际停止阶段提供重新识别或继续解析。重评、回标在没有可用答题图和答案文字时停用交分并提供重试。 |
| 15–17 成绩和 Worker | 成绩页刷新同时复查同场科目发布状态。旧汇总只重算确实出现新终分的已确认答卷。主观题 Worker 在执行预算内成功续租时刷新健康标记，超时或续租失败后标记正常过期。 |

打印验证使用双尺寸合成 PDF，含第二页的旋转与裁切。已检查选项和原页说明，并从渲染图中解码两个二维码。实体打印和扫描设备的验收继续单独记录。 [合成打印样本](../../output/functional-fixes-round2-20261115/student-print-sample.pdf) · [打印依赖许可](../../services/api-gateway/third-party-licenses/print-pdf/README.md)

最终代码的 9 个 Go 业务模块回归通过，测试连接了迁移后的隔离 PostgreSQL。真实路由的收卷恢复、学生纸张生命周期、多页打印包三条端到端用例通过。 [业务模块回归](../../output/functional-fixes-round2-20261115/final-api-regressions.txt) · [端到端回归](../../output/functional-fixes-round2-20261115/story060-integration.txt)

桌面端 93 项、题库与材料 69 项、主观题 Worker 46 项定向测试通过。阅卷相关 114 项通过，补强申诉恢复后重跑的 9 项通过。账号和同场导出 39 项、打印界面 5 项通过。管理端与桌面端生产构建通过，正式生成的接口契约与数据库版本检查通过。桌面端本轮交付源码及网页构建，Windows 原生安装包需走桌面发布流程。


## 收卷和答题卡

1. **P2 · 人工确认页码后，完整答卷仍无法结束导入。** 无条码双页答卷以 `1000001/1000002` 占位归组，两次人工确认只把 `capture_page.assigned_page_no` 设为 1 和 2。收卷关联还依赖 `submission_page.page_no`，所以实际配准完成后仍显示 `0/2`、`missing_pages`，禁止完成导入。同步页号和重算答卷质量需要在同一事务完成，并校验正式页号是否被占用。位置 `capture/matching_store_postgres.go:198`。 [真实 PostgreSQL 复现](../../output/audit-functional-20261115/manual-page-capture-result.txt)
2. **P2 · 已就绪页面确认原页码会让题块失效。** 操作允许提交相同页码，后端仍失效配准和切片，但页面状态保留为 `ready`，导致重新配准返回 `capture resource not found`。同号应保持现有产物，真正改号时需要让页面进入可重处理状态。位置 `capture/matching_store_postgres.go:203`。 [真实 PostgreSQL 复现](../../output/audit-functional-20261115/manual-page-capture-result.txt)
3. **P2 · 仍可签发的通用 v1 条码通过核验后无法排队配准。** v1 没有学生纸张序列号，配准能读到已核验的模板 ID，却取不到只从打印纸张记录读取的内容 hash，自动和手动配准都被拒绝。补齐并核对签名对应的冻结模板 hash。位置 `capture/registration_store_postgres.go:185`。 [真实 PostgreSQL 复现](../../output/audit-functional-20261115/generic-v1-capture-result.txt)

## 阅卷、报表和申诉

4. **P2 · 原仲裁教师能复核自己决定的终分。** 题级申诉在分派和提交意见时只检查人工阅卷记录，遗漏正式终分所关联的已提交仲裁。用发布终分的确切来源补齐回避判断。位置 `appeal/published_question_store_postgres_records.go:89`。 [原查询的 PostgreSQL 复现](../../output/audit-functional-20261115/appeal-arbitrator-recusal-pg-repro.log)
5. **P2 · 申诉复评创建成功但关联失败，重开后可能重复创建。** 恢复逻辑只匹配草稿、空快照和零阈值，服务端实际创建的是待批准、已冻结快照并有默认阈值的任务。重开页面后无法回认原任务，再创建会留下仍阻断发布的旧任务。恢复应使用服务端归一化后的创建身份，保留学校、考试、来源发布和答卷等关联约束。位置 `apps/web-admin/src/components/questionAppealRegradeRecovery.ts:7`。 [按真实响应形状运行的复现](../../output/audit-functional-20261115/regrade-recovery-repro.log)
6. **P2 · AI 采纳率把规则评分算进分子。** 两道规则题和一道有 AI 建议的人工确认题，让界面显示 200%，导出也写入错误比率。分子应限于有效 AI 建议的同一批样本，并根据真实采纳事实计数。位置 `report/store_postgres.go:343`。 [内存 Store 及 CSV 复现](../../output/audit-functional-20261115/report-ai-adoption-repro.log)

## 桌面端、题库和账号

7. **P2 · 桌面端恢复加密原件时，同一项能并发上传两次。** 并发标记在读取磁盘文件之后才设置，恢复期间两个点击均能进入上传。一个请求完成后另一个请求迟到失败，触发 `succeeded -> failed` 非法状态。应在第一次异步读取前占用队列项，且只允许该次操作释放占用。位置 `apps/desktop-client/src/features/upload/useUploadSync.ts:142`。 [复现](../../output/audit-functional-20261115/desktop-upload-race-result.txt)
8. **P2 · 题库预览迟到时，会把已取消勾选的题重新带入导入。** 预览进行中可取消勾选，旧结果仍写回草稿，确认使用旧草稿发请求。需要按考试、目标题库和当前选择核对预览结果是否仍适用。位置 `apps/web-admin/src/features/question-bank/HistoryQuestionImporter.tsx:122`。 [组件复现](../../output/audit-functional-20261115/questionbank-preview-race-result.txt)
9. **P2 · 撤销其他设备时，列表查询短暂失败会清除当前会话。** 撤销成功后，后续查询的错误被忽略，空结果被当作当前会话已失效并删除浏览器 cookie。只有确认撤销对象为当前会话，或查询成功且确认当前会话失效时，才应清理 cookie。位置 `auth/handlers_session.go:269`。 [请求处理器复现](../../output/audit-functional-20261115/identity-session-revoke-repro.txt)
10. **P2 · 仅有个人任务权限的角色会拿到整租户组织计数。** `teacher/grader/arbitrator` 的任务范围没有学校、年级或班级列表，组织 SQL 将空列表作为不加过滤，返回同租户所有学校的学生、年级和班级计数。无组织管理范围的个人任务角色应省略该统计，显式组织范围继续按范围过滤。位置 `dashboard/service.go:67`、`dashboard/organization_postgres.go:75`。 [合成双学校的 PostgreSQL Store 复现](../../output/audit-functional-20261115/identity-dashboard-scope-repro.txt)

## 打印、材料恢复和独立评分

11. **P2 · 学生答题卡 PDF 丢失原底图。** 已有规格要求将条码叠加在底图，实际台账读取只带版面坐标，渲染器从白页重画矩形和二维码，预印题号、选项字母和说明都无法保留。应从冻结模板对应的原页叠加条码。位置 `capture/print_package_store.go:24`、`capture/print_package_pdf.go:80`。 [规格与生产调用链](../../output/audit-functional-20261115/print-recovery-findings.md)
12. **P2 · 无法生成 PDF 的批次仍能签发。** 二维码实际尺寸与总页数在签发入账后才检查。二维码区小于 24 毫米，或者签发 1,800 人且每人 6 页等超过 10,000 页的批次，能先产生有效纸张，再在下载时失败。签发与补打应在写入前复用渲染限制，并提示冲突的页面或总页数。位置 `capture/barcode_store.go:162`、`capture/print_package_geometry.go:103`。 [签发与下载调用链](../../output/audit-functional-20261115/print-recovery-findings.md)
13. **P2 · 在页面解码时停止材料识别，默认恢复操作无法继续。** 取消时尚未生成解析任务，页面仍把“继续处理本次资料”作为主操作，调用只能恢复已有解析任务的接口，收到冲突。此阶段真正可用的重新识别入口在折叠区。应依据服务端可恢复阶段展示主操作。位置 `apps/web-admin/src/features/paper-import/PaperImportResults.tsx:46`、`paper/paper_import_parse_retry_postgres.go:43`。 [组件复现](../../output/audit-functional-20261115/paper-import-cancel-recovery-audit.txt)
14. **P2 · 重评与回标在没有可见答题证据时仍可交分。** 答题图请求失败且原始答案、OCR 文字均为空时，页面提醒联系管理员，却仍允许提交输入的分数。应在图或文字证据可用时开放提交，并在证据加载失败时提供重试。位置 `apps/web-admin/src/components/RegradeQueue.tsx:199`、`apps/web-admin/src/components/BackmarkQueue.tsx:212`。 [两个组件的复现](../../output/audit-functional-20261115/independent-review-no-answer-result.txt)

## 导出、旧版成绩汇总和后台状态

15. **P3 · 刷新成绩页面无法刷新同场其他学科的发布状态。** 另一位管理员发布最后一科后，成绩页刷新仍使用旧状态禁用同场 Excel 下载。整页重载或切换学科可恢复。页面刷新应同步更新其他学科发布状态。位置 `apps/web-admin/src/features/score-management/hooks/useSessionPublication.ts:42`。 [真实 hook 复现](../../output/audit-functional-20261115/score-export-sibling-refresh-result.txt)
16. **P3 · 旧版成绩汇总遇到部分答卷晚到终分时会拒绝整场。** 同场两答卷均已确认，只有一份出现合法的新终分时，旧 guard 把另一份仍有效的确认成绩计为不合法，阻断旧版重新汇总。现行一步正式发布使用不同的准备路径，可继续处理。位置 `score/store_postgres_finalize.go:31`。 [原查询的 PostgreSQL 复现](../../output/audit-functional-20261115/legacy-score-finalize-mixed-pg-repro.log)
17. **P3 · 正常运行的长主观题任务会被健康探针判为故障。** 任务执行等待预算默认为 810 秒，健康标记却只在整个处理循环返回后写入，120 秒过期。按容器 30 秒间隔采样，150、180、210 秒三次都失败，此后任务仍可正常完成。健康探针应能区分仍在续租的有效任务与真正卡住的任务。位置 `services/subjective-grading-worker/subjective_grading_worker/__main__.py:19`。 [用实际 Runner 和模拟探针时间复现](../../output/audit-functional-20261115/subjective-long-execute-health-result.txt)

后端简写路径均以 `services/api-gateway/internal/` 为起点。复现日志里，断言现有错误行为的用例会通过，断言应有保护的用例会失败。

## 验证与覆盖范围

- 已有桌面端 196 项、题库与模型配置 242 项、阅卷与发布相关 134 项、导出与复评相关 28 项测试通过。各组数量来自独立测试命令。打印 Go 用例也已重跑通过，覆盖页数、布局与二维码可放置条件。 [打印测试日志](../../output/audit-functional-20261115/print-existing-tests.txt) · [导出与复评测试日志](../../output/audit-functional-20261115/score-export-regrade-existing-tests.txt)
- Python 已有测试中，OCR 378 项及 80 个子测试、页面处理 252 项、图像质量 95 项、数学验证 156 项通过。主观题 worker 42 项通过，1 项在 Windows 读取刚写入的健康标记时出现亚微秒级时钟差，17 次即时误判均在 20 毫秒后恢复（200 次采样）。第 17 项使用合成任务单独复现，涉及的时间范围为数分钟。
- 已执行的测试、读完的文件和待审文件分别记录。 [考试与材料](../../output/audit-functional-20261115/exam-materials-coverage.json) · [打印与材料恢复](../../output/audit-functional-20261115/print-recovery-coverage.json) · [账号与组织](../../output/audit-functional-20261115/identity-coverage.json) · [平台与组织统计](../../output/audit-functional-20261115/identity-platform-followup-coverage.json) · [统计范围](../../output/audit-functional-20261115/dashboard-assigned-org-counts-coverage.json) · [阅卷与发布](../../output/audit-functional-20261115/grading-release-coverage.json) · [导出与复评](../../output/audit-functional-20261115/score-export-regrade-followup-coverage.json) · [桌面端、题库、后台任务](../../output/audit-functional-20261115/root-followup-coverage.json)
- 尚待完成的验收包括真实扫描设备、外部模型和实际数据规模的端到端流程，识别引擎的准确率评估，以及覆盖记录中标为 `pending` 的文件。通用 v1 双页同号的纯函数分类异常已经记录，实际归组影响待验证，未计入上面的缺陷列表。
