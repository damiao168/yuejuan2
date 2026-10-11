# 当前前端文件补充审读：05 / 06 / 07 / 09 边界

审读结束时间：2026-10-10T22:47:31+08:00。仅审读当前工作区，不修改业务文件、图规格或审读账本。下面的事实只覆盖指定前端文件；不能替代网关、领域事务、worker 或模型服务的证据，也不能据此改变已核实的后端冻结、租约、Panel 接入及成绩发布边界。

## 逐文件范围与结束哈希

四个指定文件均按段全文读取，开始与结束 SHA256 相同。`full` 表示本次全文审读完成，不表示运行测试完成，也不表示其所有依赖均已审读。

| 文件 | 本次状态 | 实际读取范围 | 读取结束 SHA256 |
| --- | --- | --- | --- |
| `apps/web-admin/src/features/grading/workbench/GradingWorkbench.tsx` | full | 1–190、191–380、381–569 | `0F528B61752AD61683E8ABE42015B33B3B0472ADF36AE0516D2899D2F7A59EC0` |
| `apps/web-admin/src/features/grading/workbench/hooks/useGradingKeyboard.ts` | full | 1–108 | `726DE41EEE9AFDA95881178FD0458C8D5A9F4D3BD5A53A6F0174085493E32F71` |
| `apps/web-admin/src/features/calibration/CalibrationDrawer.tsx` | full | 1–150、151–301 | `7543D61C5D8C1CE1A593D4EFA1170504FDEBD14A77603652E1E8C993967FCF36` |
| `apps/web-admin/src/features/school-assistant/SchoolAssistantResult.tsx` | full | 1–74 | `ABFBB0076D793B18CB6BCEA54DCD2B7B5623D1F2667ED64DF2ADDED7B767CA64` |

## 指定文件中的实际调用与限制

### GradingWorkbench.tsx

- `GradingWorkbench.tsx:69` 建立阅卷上下文查询作用域，`:73` 检查当前作用域；`:196` 草稿键使用用户与任务，`:202` 拒绝显示不属于固定考试的任务。`:262` 在用户、租户、考试或查询作用域变化时清除队列、上下文及旧异步动作的 UI 状态。这些是前端回填保护，不能表述成数据库锁或事务冻结。
- `GradingWorkbench.tsx:124` 将服务端阅卷上下文中的 `question_snapshot.id` 传给数学证据 hook；`:162` 与 `:163` 从上下文获取满分和 rubric。提交动作委托给 `useGradingReviewActions`（`:381`、`:399`），该文件本身不创建评分策略快照。
- `GradingWorkbench.tsx:172` 委托 `useExamScoring` 管理考试评分，`:436` 将其传给 `ExamScoringPanel`。本文件不定义或赋值 `scoring_mode` 枚举；不能据此断言其它前端文件没有策略配置入口。
- `GradingWorkbench.tsx:296` 仅在服务端上下文允许续租、任务属于当前用户且状态为 `assigned`、`in_progress` 或 `returned` 时设置续租；`:306` 实际调用 `renewReviewTask`，`:311` 立即续租，`:312` 每五分钟重复。这里处理的是人工阅卷任务续租。
- `GradingWorkbench.tsx:158` 选择现有 AI 建议，`:159` 计算是否允许采纳；`:400` 委托数学建议动作，`:526` 在可评分、可编辑且不需要显式第二意见时提供重新生成入口。前端选择已有建议不是创建 A/B/C Panel。
- `GradingWorkbench.tsx:534` 以已有建议 ID 调用 `verifyEvidence`；`:536` 与 `:537` 限制迟到结果只写回仍显示同一任务和上下文版本的证据面板。该检查不证明证据提交和 worker runtime 完成处于同一后端事务。

### useGradingKeyboard.ts

- `useGradingKeyboard.ts:41` 将键盘、输入法、弹层及编辑权限状态交给 `gradingShortcutIntent`；`:65` 只把动作分派给 `options` 回调。
- 提交实际是 `useGradingKeyboard.ts:67` 的 `options.onSubmit()`；采纳建议是 `:76` 的 `options.onAdoptAi()`。工作台在 `GradingWorkbench.tsx:417`、`:420` 将它们绑定到与按钮共享的 `submitGrade`、`adoptAiScore`。
- `useGradingKeyboard.ts:93` 至 `:107` 注册和清理组合输入及按键监听。本文件没有 API 请求、评分模式赋值、worker 租约字段、Panel 构造或发布操作。快捷键保护只影响前端触发条件，不建立新的评分或发布通道。

### CalibrationDrawer.tsx

- `CalibrationDrawer.tsx:52` 以打开状态、考试、题目、阅卷员与策略管理权限建立组件作用域；`:89` 至 `:93` 在卸载时取消旧请求的回填资格。
- `CalibrationDrawer.tsx:108`、`:109` 读取题目校准策略与阅卷员资格；`:112` 丢弃当前作用域旧加载结果。`:129` 将返回的策略 revision 放入 `expected_revision`；`:159` 保存策略，`:162` 接受新 revision。这里是前端传递乐观版本字段，后端是否拒绝冲突仍须后端证据。
- `CalibrationDrawer.tsx:180` 创建校准会话；`:205` 提交当前标准卷 ID、人工得分与 rubric selections。`:211` 丢弃旧作用域的提交响应，`:217` 只对返回的 qualified 资格触发回调。
- `CalibrationDrawer.tsx:98` 从当前样本的 `rubric_snapshot` 提取采分点，`:269` 显示样本题目和 rubric snapshot；`:287` 才显示已提交样本的参考得分。这证明该 UI 使用服务端返回的样本快照与提交后结果，不能证明服务端没有提前返回参考分。
- 文件没有 `scoring_mode`、worker lease token、Panel 配置或成绩发布请求；校准 UI 的作用域保护不能修改评分运行快照或后端资格门禁的架构结论。

### SchoolAssistantResult.tsx

- `SchoolAssistantResult.tsx:19` 对 unavailable 或空值事实显示“暂不可用”，零值仍可展示。
- `SchoolAssistantResult.tsx:43` 展示服务端返回的发布版本与 release ID；`:45` 告知各来源按各自时间读取、正式发布在业务页重新检查。这是展示文字，不是发布门禁实现。
- `SchoolAssistantResult.tsx:52` 用 `assistantActionPath` 校验可识别的导航目标；`:65`、`:71` 仅调用 `onNavigate(path)`。本文件不调用发布 API，也不生成不可变成绩快照，因此不能把助手展示的版本当作发布执行或强一致读取。

## 为辨别实际 API 所做的补充定位

这些依赖只为本次边界定位读取，不纳入上述四个文件的完成数。未全文审读的依赖保持 partial，若映射到仓库维护状态仍为 pending。

| 依赖 | 读取范围 | 状态 | 已定位的有限事实 |
| --- | --- | --- | --- |
| `apps/web-admin/src/api/review.ts` | 370–389、444–465 | partial | `:381` 的续租路径是 POST `/api/v1/review-tasks/{id}/renew`；`:389` 人工提交为 `/review-tasks/{id}/submit`；`:450` 以空 body 委托 SDK 创建主观题建议，`:452` 只接受 teacher_suggestion、teacher_review 或 shadow_only；`:461` 证据校验为 `/ai-grades/{id}/verify-evidence`。 |
| `apps/web-admin/src/api/calibration.ts` | 对五个导出包装函数做上下文定位：20–40 | partial | `:21`、`:25`、`:29`、`:33`、`:37` 分别委托 SDK 的策略读写、会话创建、尝试提交、资格读取。未在此追查生成 SDK 路由或后端事务。 |
| `apps/web-admin/src/features/grading/workbench/hooks/useGradingReviewActions.ts` | 151–211 | partial | `:180` 调用 `submitHumanGrade`，`:181` 携带服务端 `reviewContext.expected_revision`，并携带人工 score、采分点和 reason；这不是自动采纳模型建议为正式成绩的证据。 |
| `apps/web-admin/src/features/grading/workbench/hooks/useGradingMathActions.ts` | 1–111（文件结尾） | full，仅本次调用定位 | `:83` 检查当前任务、可编辑、非显式第二意见、数学证据 ready 和本地动作锁；`:90`、`:94` 在生成前后刷新数学证据；`:92` 调用 `createSubjectiveAiGrade`；`:97` 仍提示教师确认。未据此复核其全部依赖与测试。 |
| `apps/web-admin/src/features/grading/workbench/hooks/useExamScoring.ts` | 136–162 | partial | `:142` 委托 AI 批次启动；`:146` 明确人工逐题确认；`:157` 委托评分命令、`:160` 先读取评分准备检查。其它生命周期未在本次全文审读。 |

## 对 05 / 06 / 07 / 09 的有限结论

| 架构关注点 | 本次前端事实 | 对已核实后端边界的影响 |
| --- | --- | --- |
| 05：冻结与评分模式 | 上下文 snapshot、人工提交 expected_revision、校准策略 expected_revision 均来自服务端；四个指定文件未定义或覆写 scoring_mode。 | 没有提供需要修改评分运行冻结或模式枚举的证据。前端异步作用域保护可作为工作台说明，不能替代后端冻结。 |
| 06：图像与模板 | 工作台查看答题图并委托数学证据 UI；校准显示服务端样本图。四个文件没有模板匹配、配准、裁片或纠正应用算法。 | 不改变配准与模板治理结论。 |
| 07：默认 adapter 与 Panel | 建议创建仍委托既有网关入口；UI 选择现有建议、允许人工采纳并提示数学建议仍需教师确认。四个文件没有 Panel 实例或模型部署选择。 | 不改变后端已核实的默认单 adapter 与库级 Panel 接入边界；不能把前端第二意见提示等同于 Panel 已接入默认路由。 |
| 09：租约、重放、发布 | `renewReviewTask` 属于人工阅卷续租；前端 ref 与作用域检查防止旧结果改新任务。助手只展示版本与导航。 | 不改变 worker runtime 的 leased/running/lease token/expiry 校验、来源提交事务或发布快照事务结论。前端锁不证明服务端幂等。 |

结论只来自静态审读；未运行浏览器、单元测试或后端联调。一次 Git diff 输出被截断，未把该输出当成完整历史变更审读或完成证据。全文现状及结束哈希是本次记录依据。
