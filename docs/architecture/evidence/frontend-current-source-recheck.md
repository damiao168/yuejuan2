# 审计期间变化源码的末次复核

本次只读复核基于 2026-10-10 的当前工作区。[workspace-preservation.json](workspace-preservation.json) 记录 30 个已有文件与启动 SHA-256 不同；来源无法仅由文件内容判断，未回滚或覆盖。原始基线仅保存文件哈希，不保存全部旧正文，因此本记录比较当前代码与图纸事实，不声称已精确分离这些变化的提交者或每个增量。

## 检查范围与结论

30 项中有 17 个非测试 TS/TSX/CSS 业务文件，另有新 SDK 工具及相关测试。当前源码全文/选定边界的读取情况和 SHA-256 见 [frontend-current-source-recheck.json](frontend-current-source-recheck.json)。本组原 42 个已完成审读文件的哈希均仍与原审读记录一致，没有为变化文件自动继承旧完成状态。末次重新完成的文件单独记录当前哈希、启动哈希及差异；尚未结合全文、调用与必要断言完成的条目仍为 pending。

重新执行前端/接口静态盘点并另存 [api-catalog-current-recheck.json](api-catalog-current-recheck.json)。与启动目录相比，TS/TSX/CSS 清点 850 → 852，非测试 TS 483 → 484；其余数量均不变：24 个生产静态入口、4 个 mock，536 个 Handle、288 个精确 OpenAPI、248 个例外、298 个 OpenAPI 操作、10 个参数名别名、139 个使用的 SDK 操作、405 个调用点、376 条注册路由有前端匹配。唯一未匹配项仍为实际存在的 GET /health（HandleFunc 口径）。这些统计不等于已执行全部 API 或审读全部文件。

本轮未发现图 01、图 08 的模块归属、核心 API 或鉴权/存储调用方向因此失真，图规格与布局无需变更。根代理和流水线分组复核的文件不自动计入本组完成状态，统一账本按各自实际审读版本合并。

| 当前文件 | 核对事实 | 对图 01 / 图 08 的影响 |
| --- | --- | --- |
| desktop-client/App.tsx | 七工作区仍装配真实登录、队列、匿名任务、本机草稿和诊断；浏览器卸载走 SDK，Tauri 关闭仍单独等待异步草稿决定 | F10 与文件/本机流程保持成立；不新增业务接口 |
| desktop-client/useScanQueue.ts | 真实考试、可继续批次、锁定模板和 DPI 先校验；逐件检查及加密 Spool；新原件入队后才移旧件，失败/相同件保留原件 | F05/F10 队列投影与失败恢复边界保持成立；不推断硬件采集已验收 |
| student-portal/App.tsx | student 角色与读取权限、Hash 考试路径、release_id 证据/申诉锚点仍存在；仅回执等待和本身份内存草稿取消卸载，已存 sessionStorage 草稿可恢复 | F08 及学生 API 链保持成立；浏览器是否显示提示仍由浏览器决定 |
| useScoreWorkspaceData.ts | 同时读取旧 grade/quality 和新版 release_gate、score_releases、current-published；请求代次和完整快照发布仍分开 | 旧成绩与新版发布快照并存仍成立，不能改成所有发布统一走新 gate |
| CalibrationDrawer.tsx | 题目/阅卷员/权限作用域 key，读/写请求代次及 mounted 检查；参考偏差只在提交后显示 | 人工校准归属 F09；不变成 AI 自动最终评分 |
| GradingWorkbench.tsx / useGradingKeyboard.ts | 实际工作台仍经 review/math/examScoring hooks；输入法状态与控件 Enter 优先，未保存评分走导航 guard | F07 人工确认链不变；快捷键和显示保护不构成新的生产 Panel 接入 |
| AssessmentProfileEditor.tsx / ExamPreparationPage.tsx | 准备阶段配置、快照只读、已保存可识别模式核对及 configurationHash 确认仍同链；模式仍为真实大写标识 | F03/F04 冻结事实保持成立 |
| TemplateWorkspaceView.tsx / CaptureBatchPage.tsx | 模板候选/手工区域/绑定分开，采集与配准仍用原领域命令 | F05 仍为已有模板及人工确认；无新增 YOLO、未知模板自动建模或生产 Panel 证据 |
| SchoolAssistantResult.tsx | 服务端事实、来源时间和限制分开展示，导航经 assistantActionPath 校验 | 学校助手数据流与后端业务范围校验保持成立 |
| LearningReportsPage.tsx | 四类真实 report API、有限指标、班级分数与比例刻度、Blob 导出仍存在 | F08 报告归属和 API 调用保持成立 |
| OrganizationSetupPage.tsx | 组织/CSV/人员激活仍用原 API，当前步骤按真实数据定位；一次性邀请回执先留存再刷新 | F02/F04 不增新后端接口；不把待激活邀请算已启用人员 |
| useUnsavedChangesGuard.ts | dirty/busy 和最新 ref 控制站内离开；SDK 多实例订阅统一浏览器卸载 | UI 状态保护工具；不改变认证、权限、命令收据或远端写入 |
| exam-preparation.css | 透明选项框、实色嵌套标签、原件坐标与指针穿透；容器响应布局 | 仅界面绘制，对模块/API 流无影响 |
| sdk/index.ts / 新 browserLifecycle.ts | 新导出统一 beforeunload 工具；按 Window 聚合订阅、幂等取消、保留旧属性处理器且不覆盖外部接管 | SDK 业务契约导出和 API 注册不变；不新增部署组件 |

## 实际验证边界

本轮读取现有及新增测试的对应断言；未运行 Vitest、浏览器、Tauri、数据库或供应商验收。SDK EventTarget、学生/桌面/jsdom 的卸载测试只能说明断言范围，不能证明真实浏览器一定弹出退出提示。核心 API 和路由重新枚举只确认静态对应关系，不能代替 handler 全文审读。

图纸正文继续保留已确认的权限缓存重放风险、旧/新发布并存、图像/评分分步提交缺口，以及三智能体普通入口未自动接入的限制。变化源码以当前实现为准，不把本轮未出现新事实解释为所有页面无缺陷。
