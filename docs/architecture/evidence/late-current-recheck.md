# 末次变化架构边界复核

2026-10-11 03:47 UTC快照为58个原始文件变化。任务未改业务代码，保留原始基线；没有将变化来源归因给其他人。当前源码抽读及hash见[late-current-reviewed-files.json](late-current-reviewed-files.json)，全部按partial/pending记账。

主任务和协作复核确认：

- `releasegate/service.go:50`正式发布重新求值并追加证据；`:113`新增Inspect仅当前考试级说明，不追加证据，也不校验指定release的冻结快照。旧score.PublishGrades仍独立，新发布一致性仍由scorerelease事务保证。原P1-01成立，图05引用已改为正式入口。重评阻塞计数不含cancelled与ready_for_release。
- `grading_agent/server.py`与config新增TLS监听/成对证书校验；Compose默认API→grading-agent、OCR→math-verification使用HTTPS，CA与证书只读挂载。preflight第112–124行对跨容器链路要求HTTPS。图10已更新两条链路；Nginx仍HTTP代理，外层TLS与生产运行保持待确认。
- 当前数学验证服务采用spawn子进程和预算回收，超时返回uncertain；清理可能在运算预算外额外等待。TLS最低1.2，明文例外仅明确本地环境、显式选项及字面IPv4回环。OCR配置仍默认HTTPS。未运行服务、数学效果或部署验收。
- 生产组装仍为单activeAdapter，三智能体内核接入边界未变。当前桌面离线工作台已重新全文读取，见[current-desktop-recheck.md](current-desktop-recheck.md)；该文件未改变冻结材料、稳定命令先持久化再同步及图片只在会话中显示的结论。

新增测试、文档、镜像构建与CI变化未全部审读，仍按账本pending。Docker构建上下文与隔离E2E配置只用于说明工程工具边界，不能当生产已部署证据。图10更新后重新执行实际draw.io全70页渲染和视觉检查。
