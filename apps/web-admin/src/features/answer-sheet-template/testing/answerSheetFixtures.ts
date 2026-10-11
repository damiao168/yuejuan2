import type {
	AnswerSheetTemplate,
	ExamTemplateBinding,
	OMRCalibrationCase,
	OMRCalibrationDetail,
	OMRCalibrationSession
} from "../../../api/configuration";
import type { PaperVersion, Question } from "../../../api/papers";

const timestamp = "2026-10-01T00:00:00Z";
export const answerQuestion: Question = {
	id: "q1",
	tenant_id: "tenant",
	exam_id: "exam",
	question_no: "1",
	question_type: "single_choice",
	score: 5,
	options: ["A", "B"],
	knowledge_points: [],
	sort_order: 1,
	status: "active"
};
export const answerSource: PaperVersion = {
	id: "source",
	tenant_id: "tenant",
	exam_id: "exam",
	file_asset_id: "asset",
	version_no: 1,
	status: "active",
	purpose: "answer_sheet",
	file: {
		original_name: "blank.pdf",
		content_type: "application/pdf",
		size_bytes: 100,
		hash_sha256: "hash"
	}
};
export const answerRegion = {
	id: "r1",
	question_id: "q1",
	label: "1",
	x: 0.1,
	y: 0.2,
	width: 0.3,
	height: 0.4,
	option_regions: []
};
export const answerTemplate: AnswerSheetTemplate = {
	id: "template",
	tenant_id: "tenant",
	exam_id: "exam",
	exam_paper_id: "source",
	name: "模板",
	version_no: 2,
	revision: 1,
	status: "draft",
	page_count: 1,
	content_hash: "hash",
	created_by: "admin",
	created_at: timestamp,
	updated_at: timestamp,
	layout: {
		pages: [
			{
				page_no: 1,
				width: 100,
				height: 200,
				registration_marks: [],
				identity_regions: [],
				question_regions: [answerRegion]
			}
		]
	}
};

export function calibrationCase(
	patch: Partial<OMRCalibrationCase> = {}
): OMRCalibrationCase {
	return {
		id: "case",
		calibration_id: "session",
		omr_run_id: "run",
		answer_segment_id: "segment",
		question_id: "q1",
		question_no: "1",
		question_type: "single_choice",
		option_labels: ["A", "B"],
		sample_stratum: "selected_high",
		crop_sha256: "crop-hash",
		observed_confidence: 0.99,
		observed_decision: "blank",
		observed_options: [],
		measurements: [],
		segment_image_url: "image.png",
		created_at: timestamp,
		...patch
	};
}

export function calibrationDetail(
	status: OMRCalibrationSession["status"] = "draft",
	mismatch = 0,
	labeled = 1,
	total = 1
): OMRCalibrationDetail {
	return {
		session: {
			id: "session",
			tenant_id: "tenant",
			template_id: "template",
			scope_type: "template",
			question_ids: ["q1"],
			question_type: "template",
			profile_version: "opencv-template-difference-bubble-v1",
			reference_file_asset_id: "asset",
			option_labels: ["A", "B"],
			sample_seed: "seed",
			sample_count: total,
			minimum_samples: 100,
			created_by: "admin",
			created_at: timestamp,
			updated_at: timestamp,
			status,
			minimum_confidence: 0.98,
			minimum_samples_per_option: 10,
			minimum_samples_per_stratum: 10,
			template_content_hash: "template-hash",
			profile_hash: "profile-hash",
			reference_sha256: "reference-hash",
			summary: {
				labeled_count: labeled,
				total_count: total,
				pending_count: total - labeled,
				match_count: labeled - mismatch,
				mismatch_count: mismatch,
				eligible_count: 1,
				eligible_match_count: 1,
				eligible_mismatch_count: mismatch,
				blockers: [],
				stratum_coverage: {},
				question_coverage: {},
				option_coverage: {},
				ready_to_approve: true
			}
		},
		cases: [calibrationCase()]
	};
}

export function templateBinding(
	mode: ExamTemplateBinding["mode"],
	templateId: string
): ExamTemplateBinding {
	return {
		id: "binding",
		tenant_id: "tenant",
		exam_id: "exam",
		template_id: templateId,
		template_content_hash: "hash",
		mode,
		source: "manual",
		revision: 1,
		bound_by: "admin",
		bound_at: timestamp,
		updated_at: timestamp
	};
}
