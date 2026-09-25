package workerruntime

import "edugrade-enterprise/services/api-gateway/internal/auth"

// A lease is scoped to a task, but it is not a substitute for the caller's
// authenticated service role. Keep this allowlist server-side: queue and
// worker_service values in requests and task headers are untrusted claims.
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
