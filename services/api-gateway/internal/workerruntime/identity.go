package workerruntime

import "edugrade-enterprise/services/api-gateway/internal/auth"

// 租约只绑定任务，不能代替调用方的已认证服务角色。队列和 worker_service
// 都来自请求或任务头，属于不可信声明，必须由服务端白名单决定。
func workerIdentityAllows(user auth.User, queueName, workerService string) bool {
	if !auth.IsServiceUser(user) {
		return true
	}
	if auth.HasRole(user, "subjective_grading_worker") {
		return queueName == "subjective-grading" && workerService == "subjective-grading-worker"
	}
	if !auth.HasRole(user, "page_processing_worker") {
		return false
	}
	switch queueName {
	case "page-processing":
		return workerService == "page-processing"
	case "ocr", "paper-formula", "math-verification":
		return workerService == "ocr-worker"
	case "image-quality":
		return workerService == "image-quality-worker"
	default:
		return false
	}
}

func workerIdentityAllowsTask(user auth.User, task Task) bool {
	return workerIdentityAllows(user, task.QueueName, task.WorkerService)
}
