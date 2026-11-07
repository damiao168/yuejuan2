package review

import "edugrade-enterprise/services/api-gateway/internal/auth"

// 阅卷端统一使用答题段的不透明编号，不因普通答卷有考号、盲投无考号而泄露任务类型。
// 只修改响应副本；管理端仍保留原匿名码，持久化身份和成绩匹配不受影响。
func graderTaskView(user auth.User, task ReviewTask) ReviewTask {
	if reviewWorkerScoped(user) {
		task.AnonymousCode = task.AnswerSegmentID
	}
	return task
}
