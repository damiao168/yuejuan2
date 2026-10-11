export function downloadGovernanceMaterial(name: string, value: unknown) {
	const url = URL.createObjectURL(
		new Blob([JSON.stringify(value, null, 2)], { type: "application/json" }),
	);
	const anchor = document.createElement("a");
	anchor.href = url;
	anchor.download = name;
	anchor.click();
	URL.revokeObjectURL(url);
}

export const evaluationReportExample = {
	run_key: "math-grade9-example-v1",
	display_name: "九年级数学报告登记示例",
	dataset_reference: "authorized-math-grade9-v1",
	dataset_sha256: "请替换为实际数据集64位SHA256",
	evidence_class: "authorized_frozen_set",
	authorization_reference: "请填真实授权引用",
	subject: "mathematics",
	grade: "九年级",
	question_type: "short_answer",
	modality: "text",
	sample_count: 30,
	repeat_count: 2,
	reason: "请说明评测责任人、离线报告来源和人工核验安排",
	candidates: [
		{
			model_config_id: "请填已验证的学校模型ID",
			prompt_version: "实际提示词版本",
			rubric_version: "实际细则版本",
			evaluated_samples: 30,
			teacher_reviewed_samples: 30,
			teacher_accepted_samples: 0,
			serious_error_samples: 0,
			evidence_valid_samples: 0,
			repeat_comparisons: 30,
			stable_repeat_samples: 0,
			p95_latency_ms: 0,
			total_cost_micros: 0,
			reason: "请用实际运行结果替换示例统计；成本按微美元",
		},
	],
};

export const observationExample = [
	{
		response_key: "deidentified-response-001",
		response_fingerprint: "请替换为实际脱敏答卷64位SHA256",
		reference_kind: "gold",
		subject: "mathematics",
		archetype: "short_constructed",
		ocr_quality: "high",
		answer_length: "medium",
		rubric_complexity: "medium",
		reference_score: 4,
		model_score: 4,
		max_score: 5,
		error_source: "none",
		needs_human_review: false,
		reference_reviewer_count: 1,
		reference_adjudicated: false,
	},
];
