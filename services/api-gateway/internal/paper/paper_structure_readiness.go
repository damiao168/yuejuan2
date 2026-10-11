package paper

// 人工组卷没有资料导入时继续原校验；有资料时，只接受当前代次的完整试卷确认。
// 已应用的历史导入保持兼容。新资料会改变摘要，旧就绪确认不能继续开考。
func withStructureReadiness(result ReadinessResult, job PaperImportJob) ReadinessResult {
	confirmed := job.Status == "applied" || job.Status == "review_required" && job.Generation > 0 && job.StructureConfirmedGeneration == job.Generation
	result.Checks = append(result.Checks, ReadinessCheck{Code: "material_structure", Label: "试卷资料与题目结构", Passed: confirmed, Severity: "blocker", Message: "请确认完整试卷资料，并核对采用的题目结构", Section: "questions"})
	result.Ready = result.Ready && confirmed
	result.ConfigurationHash = stableContentHash(struct {
		Configuration string
		ImportID      string
		Generation    int64
		Confirmed     bool
	}{result.ConfigurationHash, job.ID, job.Generation, confirmed})
	return result
}
