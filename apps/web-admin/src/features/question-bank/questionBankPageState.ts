import type { CreateQuestionBankItemRequest, QuestionBankItem, QuestionBankVersion, UpdateQuestionBankVersionRequest, UpdateQuestionBankMetadataSchemaRequest } from "@edugrade/sdk";
import type { MetadataSchemaFormValues } from "./questionBankTypes";

export const transitionSuccessMessages = {
	publish: "已发布到学校题库。请在考试的题目页选择此版本；已有考试不受影响。",
	"submit-review": "已提交审核，请通知本题库的审核教师在题库中处理。",
	approve: "审核已通过，下一步由有发布权限的教师发布到题库。",
	"return-to-draft": "已退回草稿，可修改后重新提交审核。",
};

export const defaults: Omit<CreateQuestionBankItemRequest, "metadata"> & {metadata: Partial<CreateQuestionBankItemRequest["metadata"]>} = {
	item_code: "",
	question_type: "short_answer",
	assessment_archetype: "short_constructed",
	stem: "",
	default_score: 5,
	options: [],
	knowledge_points: [],
	metadata: {
		subject_code: "mathematics",
		difficulty_band: "unclassified",
		cognitive_level: "unclassified",
		copyright: "unknown",
		language: "zh-CN",
		intended_use: "practice",
	},
};
export function contentFrom(
	v: QuestionBankVersion,
): Omit<UpdateQuestionBankVersionRequest, "expected_revision"> {
	return {
		question_type: v.question_type,
		assessment_archetype: v.assessment_archetype,
		stem: v.stem,
		default_score: v.default_score,
		options: v.options,
		knowledge_points: v.knowledge_points,
		metadata: v.metadata,
		custom_metadata: v.custom_metadata,
	};
}

// 深链接中的题目和版本分别读取，必须核对所属关系后再显示或编辑，避免在另一题标题下操作。
export function matchingVersionForItem(item: QuestionBankItem | null, candidate: QuestionBankVersion | undefined) {
	return item && candidate?.item_id === item.id ? candidate : undefined;
}

export function metadataSchemaReadState({ bankId, loadedBankId, query, formBankId, failureBankId }: Readonly<{
	bankId: string;
	loadedBankId?: string;
	query: Readonly<{ isSuccess: boolean; isError: boolean; data?: { schema?: unknown } }>;
	formBankId: string | null;
	failureBankId: string | null;
}>) {
	const schemaReadable = Boolean(loadedBankId === bankId && query.isSuccess && query.data?.schema);
	const schemaFormReady = schemaReadable && formBankId === bankId;
	const schemaLoadFailed = !query.isSuccess && (query.isError || failureBankId === bankId);
	return { schemaReadable, schemaFormReady, schemaLoadFailed };
}

// 目录与字段使用显式值优先；文本输入只在表单未提供结构化条目时解析，旧选项不被重建。
export function metadataSchemaUpdate(values: MetadataSchemaFormValues, revision: number): UpdateQuestionBankMetadataSchemaRequest {
		const fields = (values.fields ?? []).map(({ options_text, ...field }) => ({
			...field,
			options:
				field.type === "enum"
					? field.options ?? (options_text ?? "").split("\n").map((line) => line.trim()).filter(Boolean).map((line) => {
							const [value, label = value] = line.split("|");
							return { value: value.trim(), label: label.trim(), active: true };
						})
					: undefined,
		}));
		const taxonomies = (values.taxonomies ?? []).map(({ terms_text, ...taxonomy }) => ({
			...taxonomy,
			terms: taxonomy.terms ?? (terms_text ?? "").split("\n").map((line) => line.trim()).filter(Boolean).map((line) => {
				const [id, label = id] = line.split("|");
				return { id: id.trim(), label: label.trim(), active: true };
			}),
		}));
		return { expected_revision: revision, fields, taxonomies } as UpdateQuestionBankMetadataSchemaRequest;
}
