# 最终本地 SonarQube：独立逐项对账

结论：further_action_required。analysisId `a741715d-5427-49a6-ac74-7dd92ad5cfa1`；Scanner exit 0，CE SUCCESS，导出完整 True。

2223 原问题：{'FALSE_POSITIVE': 30, 'FIXED': 2190, 'ACCEPTED': 1, 'OPEN': 2}。47 已知新增：{'FIXED': 43, 'OPEN': 2, 'FALSE_POSITIVE': 2}。总跟踪2270 key；active 4，额外未跟踪 active 0。源码 FIXED、人工 FALSE_POSITIVE 和 ACCEPTED 分开统计，不把处置算作源码修复。

核对2654份冻结/当前源码SHA；原告警291文件均须包含。current差异0，冻结差异0，原规则配置匹配True，原排除/忽略配置匹配True。

质量门禁仍为 ERROR；它与问题关闭数独立。如缺覆盖率数据，不把平台0声称为真实测试覆盖率。UI中断/实际断言差异与PG条件skip限制保留。

需继续处理的证据问题：2。完整逐key状态、额外告警、源SHA和门禁条件见同名JSON。
