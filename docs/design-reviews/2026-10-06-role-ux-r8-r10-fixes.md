# 四角色产品复审 R8–R10 修复与验收

本轮承接 [R5–R7 修复](2026-10-06-role-ux-r5-r7-fixes.md)后的复审，按 R8、R9、R10 顺序拆分，由三个智能体并行修复、交叉检查，主智能体统一验收。以本轮开始时的工作区备份为基线，保留已有未提交修改；本文只记录本轮三个问题及修复期间发现的相关边界，不代表四角色全产品验收。本轮涉及 6 个既有文件、4 个新增源码或测试文件及本文；工作期间其他任务的并行修改保留，未计入本轮修复。

## 用户行为变化

| 编号 | 身份、原问题 | 修复后的行为 |
| --- | --- | --- |
| R8 | 学生：批注重试的登录失效结果迟到时，复核申请已在提交；重新登录可以卸载表单，使回执确认界面消失 | 公共重新登录回调同步检查提交 ref，所有相关入口在提交期间禁用。旧回调或直接调用不能绕过保护；请求结束后恢复登录。成功显示申请确认，失败保留草稿供同账号、同发布版本恢复 |
| R9 | 学校管理员：数学考试仍允许选择英语模板，前端可提交，后台按考试学科拒绝后只显示一般失败提示 | 可识别考试学科的下拉框锁定对应学科，保留学段和模板版本选择；保存前再次核对提交的学科和模板。未知或综合学科不默认当作数学，未配置时要求明确选择，已有配置保留原值 |
| R10 | 学校管理员、阅卷教师：公式题的 AI 提示推荐切换规则自动评分，但后端对公式题的规则和 AI 模式均转人工 | 公式题和编程题在所有模式下明确提示需要教师人工评分。只有实际支持规则评分的五类题型，在选择 AI 模式时才提示规则自动路径；既有模式、管理员显式选择和默认策略保持原有语义 |

R8 保护的是界面切换，不撤销已经发出的申请，不表示已解决服务端幂等或所有网络故障。复核 POST 自身返回登录失效时，提交状态结束后仍可登录；草稿继续按账号和成绩发布版本隔离。

R9 使用与后台 `assessment.NormalizeSubjectCode` 一致的别名集合。草稿中的历史异学科配置先展示原值并禁止保存，管理员明确点击“改用考试学科”后重新核对模板和证据；冻结快照及只读配置不提供修复动作、不自动改写。综合或未知学科的学科控件不显式传入 `disabled=false`，以继承真实 Ant Design 表单的冻结、权限和保存禁用状态。原测试替身已修正为 Ant Design 的 nullish 合并规则，四个曾失败的边界先复现、后通过。

R10 的能力说明以 `paper/scoring_coverage.go` 与 `grading/scoring_run_routing.go` 为依据。五类规则题型是单选、多选、判断、填空和数值题；公式与编程题不能靠更换原型或模式获得自动评分能力。未知题型不推测自动能力，也不自动修正已保存策略。

## 验证结果

- 管理端统一专项：9 个文件、161 项通过，包括配置组件 48 项、评分提示 helper 36 项、学科规范化 31 项，以及准备冻结、题目页面、编辑状态、策略和题目展示回归。单 worker、15 秒单项超时，与上一轮同样的专项运行参数。
- 学生端全量：12 个文件、135 项通过，包含新增重新登录保护 7 项。覆盖迟到登录失效、旧回调直接调用、回执成功或失败、整卷批注、申请记录和 POST 自身登录失效。首次全量有一个原有父级重绘用例提前点击仍禁用的按钮；修复为等待已有申请查询完成、按钮启用后点击，保留原断言、业务代码和超时，最终全量通过。
- 智能体独立交叉审查：学生相关 25 项与学校配置相关 79 项通过，属于上述范围的复核，不累计为额外测试总数；按实际后台约束核对学科、评分能力、历史保留和请求代次，未发现剩余阻断问题。
- Chromium：原有默认配置及冻结 5 个场景通过；本轮学科、评分提示及冻结只读 5 个场景通过。新增场景核对数学学科锁定与合法保存、综合学科完整证据交集、未知学科快照控件禁用、公式题人工提示与显式 AI 策略保留、选择题 AI 模式的正确规则建议。首次运行最后两项因定位到被选中值遮挡的内部搜索框而超时，改用真实组合框的键盘操作打开选项后重跑通过；没有改变产品逻辑或测试超时。
- 管理端、学生端 TypeScript 与生产构建通过；管理端生产路由、实际构建体积预算通过。构建保留既有大包提示，学生端原有 NaN 展示夹具仍输出 React 警告；未改变门槛。
- Go：`grading`、`assessment`、`paper` 三个包通过。用户可见文案、9 个已改应用源码或测试文件的 Biome 错误门禁、生成契约检查通过。路由仍为 524 条：276 条 OpenAPI、248 条已审核遗留缺口。

浏览器使用 API 路由夹具，真实页面、Ant Design 控件与请求参与测试；它不验证 PostgreSQL 配置及冻结事务，也不运行正式评分作业。真实数据库完整端到端流程未运行，不能计为通过。本轮没有修改后端业务、SQL 迁移、接口契约或 SDK 生成产物。

## 文件维护核对

已结合实现、调用和测试核对本轮实际修改文件的维护说明。此表不代表全仓审读完成；未涉及文件不计入完成量。

| 文件 | 状态 | 核对点 |
| --- | --- | --- |
| apps/student-portal/src/App.tsx | annotated | 公共回调的同步提交保护、禁止卸载与已发请求的区别、各登录入口禁用 |
| apps/student-portal/src/App.reauthentication.test.tsx | annotated | 迟到登录失效与提交回执顺序、旧回调、同版本草稿恢复、成功后不重复提交 |
| apps/student-portal/src/App.batch8.test.tsx | annotated | 标题出现早于已有申请查询完成，点击前等待实际可提交状态 |
| apps/web-admin/src/components/features/assessment/AssessmentProfileEditor.tsx | annotated | 考试学科约束、历史不重写、提交值再次核对、未知学科继承表单禁用 |
| apps/web-admin/src/components/features/assessment/AssessmentProfileEditor.productUx.test.tsx | annotated | 真实表单禁用合并、合法综合学科切换、强制回调、历史修复及保存等待边界 |
| apps/web-admin/src/components/features/assessment/assessmentScoringDefaults.ts | annotated | AI 不支持不能推导规则支持，真实题型能力与人工要求，旧默认策略保持 |
| apps/web-admin/src/components/features/assessment/assessmentScoringDefaults.test.ts | self_explanatory | 六种模式下公式/编程人工说明、规则题型建议、未知题型保守展示 |
| apps/web-admin/src/components/features/assessment/assessmentSubjectScope.ts | annotated | 后台别名集合、未知或综合学科继续允许逐题选择 |
| apps/web-admin/src/components/features/assessment/assessmentSubjectScope.test.ts | self_explanatory | 中文、规范代码与兼容别名，未知值不退回数学 |
| tests/e2e/specs/assessment-subject-guidance.spec.ts | annotated | API 夹具边界、学科和证据约束、真实 Ant Design 冻结只读及显式评分模式 |
| docs/design-reviews/2026-10-06-role-ux-r8-r10-fixes.md | documentation | 本轮实际行为、验证和未覆盖范围 |

本轮没有提交、推送或部署。历史迁移及协调发布要求仍以 [R1–R4 记录](2026-10-06-role-ux-r1-r4-fixes.md)为准。
