# Go原告警处理进度

更新时间：2026-10-11T18:36:30.811423+08:00

原209项：关联包测试通过、待Sonar复扫 129 项；源码已修改、待验证 0 项；尚未处理 80 项。平台确认关闭 0 项。

原告警全部在测试代码。保留原断言、失败短路与状态变更次序；不使用排除、抑制或放宽规则。
server 62项、Fujian导入工具6项、modelgovernance 12项由其他智能体独占，本文件未自动宣称其已修复。

|批次|原问题数|实际验证证据|状态|
|---|---:|---|---|
|go-batch01|7|go-batch01-tests.json|passed|
|go-batch02|5|go-batch02-tests.json|passed|
|go-batch03|5|go-batch03-tests.json|passed|
|go-batch04|2|go-batch04-tests.json|passed|
|go-batch05|6|go-batch05-tests.json|passed|
|go-batch06|3|go-batch06-retry1-tests.json|passed|
|go-batch07|4|go-batch07-retry1-tests.json|passed|
|go-batch08|7|go-batch08-tests.json|passed|
|go-batch09|9|go-batch09-tests.json, go-batch09-auth-retry1-tests.json|passed|
|go-batch10|8|go-batch10-11-tests.json|passed|
|go-batch11|5|go-batch10-11-tests.json|passed|
|go-batch12|5|go-batch12-tests.json|passed|
|go-batch13|6|go-batch13-21-tests.json, go-batch13-21-retry-taskmaterials-tests.json|passed by actual package results; initial failure retained|
|go-batch14|5|go-batch13-21-tests.json, go-batch13-21-retry-taskmaterials-tests.json|passed by actual package results; initial failure retained|
|go-batch15|5|go-batch13-21-tests.json, go-batch13-21-retry-taskmaterials-tests.json|passed by actual package results; initial failure retained|
|go-batch16|4|go-batch13-21-tests.json, go-batch13-21-retry-taskmaterials-tests.json|passed by actual package results; initial failure retained|
|go-batch17|3|go-batch13-21-tests.json, go-batch13-21-retry-taskmaterials-tests.json|passed by actual package results; initial failure retained|
|go-batch18|5|go-batch13-21-tests.json, go-batch13-21-retry-taskmaterials-tests.json|passed by actual package results; initial failure retained|
|go-batch19|5|go-batch13-21-tests.json, go-batch13-21-retry-taskmaterials-tests.json|passed by actual package results; initial failure retained|
|go-batch20|5|go-batch13-21-tests.json, go-batch13-21-retry-taskmaterials-tests.json|passed by actual package results; initial failure retained|
|go-batch21|5|go-batch13-21-tests.json, go-batch13-21-retry-taskmaterials-tests.json|passed by actual package results; initial failure retained|
|go-batch22|5|go-batch22-26-and-new-five-tests.json|passed|
|go-batch23|7|go-batch22-26-and-new-five-tests.json|passed|
|go-batch24|4|go-batch22-26-and-new-five-tests.json|passed|
|go-batch25|3|go-batch22-26-and-new-five-tests.json|passed|
|go-batch26|1|go-batch22-26-and-new-five-tests.json|passed|

首次内存/编译失败证据保留，只有真实完成的关联包验证才计入通过。环境条件跳过见测试JSON，不能作为真实数据库或外部模型集成通过。

逐原 issue key 与逐文件审读/SHA256记录见 go-progress.json。未完整审读文件保持 pending；辅助AST估值不替代平台复扫。
