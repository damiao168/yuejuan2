import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
	Alert,
	App,
	Button,
	Checkbox,
	Form,
	Input,
	InputNumber,
	Select,
	Space,
	Switch,
} from "antd";
import type {
	QuestionBankScoring,
	QuestionBankVersion,
	RubricInput,
} from "@edugrade/sdk";
import { questionBankApi } from "../../api/questionBank";
import { getUserErrorMessage } from "../../api/client";
import { uploadFile } from "../../api/files";

const initial: QuestionBankScoring = {
	answer: null,
	solution: null,
	rubric: null,
	template_version_id: null,
	assets: [],
	use_policy: "practice_only",
};
type Values = {
	answer: string;
	equivalent: string;
	absolute?: number;
	relative?: number;
	solution: string;
	steps: string;
	rubricEnabled: boolean;
	points: Array<{
		id: string;
		description: string;
		score: number;
		required: boolean;
		evidence: string;
	}>;
	deductions: string;
	examples: string;
	template?: string;
	use_policy: QuestionBankScoring["use_policy"];
};
const json = (v: unknown) => JSON.stringify(v, null, 2);
// 答案可能是文本、布尔值或数组；合法 JSON 保留原结构，解析失败则按普通文本提交，避免改写教师输入。
function answerValue(v: string): unknown {
	try {
		return JSON.parse(v);
	} catch {
		return v;
	}
}
function arrayValue(v: string): unknown[] {
	const a: unknown = JSON.parse(v || "[]");
	if (!Array.isArray(a)) throw Error("请填写有效的列表");
	return a;
}

export function ScoringEditor({
	version,
	disabled,
	onSaved,
	onDirty,
	canManageFiles,
	actorId,
}: {
	version: QuestionBankVersion;
	disabled: boolean;
	onSaved: (v: QuestionBankVersion) => void;
	onDirty: (v: boolean) => void;
	canManageFiles: boolean;
	actorId: string;
}) {
	const { message } = App.useApp();
	const [form] = Form.useForm<Values>();
	const loaded = useRef("");
	const revision = useRef(0);
	const pending = useRef<{ fingerprint: string; key: string } | null>(null);
	const [saving, setSaving] = useState(false);
	const [assets, setAssets] = useState<QuestionBankScoring["assets"]>(
		version.scoring.assets,
	);
	const fileInput = useRef<HTMLInputElement>(null);
	const rubricEnabled = Form.useWatch("rubricEnabled", form);
	const template = Form.useWatch("template", form);
	const templates = useQuery({
		queryKey: [
			"question-bank",
			version.tenant_id,
			actorId,
			"published-templates",
			version.item_id,
		],
		queryFn: async () => {
			const item = await questionBankApi.getQuestionBankItem({
				path: { itemId: version.item_id },
			});
			const bank = await questionBankApi.getQuestionBank({
				path: { bankId: item.item.bank_id },
			});
			const page = await questionBankApi.listQuestionBankRubricTemplates({
				query: { bank_id: item.item.bank_id, limit: 100 },
			});
			return { ...page, schoolId: bank.bank.school_id };
		},
	});
	useEffect(() => {
		if (loaded.current === `${version.id}:${version.revision}`) return;
		loaded.current = `${version.id}:${version.revision}`;
		revision.current = version.revision;
		const s = version.scoring ?? initial;
		setAssets(s.assets);
		form.setFieldsValue({
			answer:
				s.answer?.standard_answer == null
					? ""
					: typeof s.answer.standard_answer === "string"
						? s.answer.standard_answer
						: json(s.answer.standard_answer),
			equivalent: json(s.answer?.equivalent_answers ?? []),
			absolute: s.answer?.tolerance.absolute,
			relative: s.answer?.tolerance.relative,
			solution: s.solution?.raw_text ?? "",
			steps: json(s.solution?.steps ?? []),
			rubricEnabled: Boolean(s.rubric),
			points:
				s.rubric?.points.map((p) => ({
					...p,
					evidence: json(p.evidence_requirements ?? []),
				})) ?? [],
			deductions: json(s.rubric?.deductions ?? []),
			examples: json(s.rubric?.examples ?? []),
			template: s.template_version_id ?? undefined,
			use_policy: s.use_policy,
		});
	}, [version, form]);
	async function save(values: Values) {
		setSaving(true);
		try {
			const tolerance: { absolute?: number; relative?: number } = {};
			if (values.absolute != null) tolerance.absolute = values.absolute;
			if (values.relative != null) tolerance.relative = values.relative;
			const rubric: RubricInput | null = values.rubricEnabled
				? {
						status: "approved",
						max_score: version.default_score,
						points: (values.points ?? []).map((p) => ({
							id: p.id,
							description: p.description,
							score: p.score,
							required: Boolean(p.required),
							evidence_requirements: arrayValue(
								p.evidence,
							) as RubricInput["points"][number]["evidence_requirements"],
						})),
						deductions: arrayValue(values.deductions),
						examples: arrayValue(values.examples),
					}
				: null;
			const scoring: QuestionBankScoring = {
				answer: values.answer.trim()
					? {
							standard_answer: answerValue(values.answer),
							equivalent_answers: arrayValue(values.equivalent),
							tolerance,
						}
					: null,
				solution: values.solution.trim()
					? {
							raw_text: values.solution,
							steps: arrayValue(values.steps) as NonNullable<
								QuestionBankScoring["solution"]
							>["steps"],
							source_refs: [],
						}
					: null,
				rubric,
				template_version_id: values.template ?? null,
				assets,
				use_policy: values.use_policy,
			};
			const body = { expected_revision: revision.current, scoring };
			const fingerprint = json(body);
			if (pending.current?.fingerprint !== fingerprint)
				pending.current = { fingerprint, key: crypto.randomUUID() };
			const result = await questionBankApi.updateQuestionBankScoring({
				path: { versionId: version.id },
				body,
				headers: { "Idempotency-Key": pending.current.key },
			});
			pending.current = null;
			onDirty(false);
			onSaved(result.version);
			message.success("评分标准已保存");
		} catch (error) {
			message.error(
				getUserErrorMessage(
					error,
					"评分标准保存失败，请核对内容或加载最新版本",
				),
			);
		} finally {
			setSaving(false);
		}
	}
	return (
		<Form
			form={form}
			layout="vertical"
			onFinish={save}
			onValuesChange={() => onDirty(true)}
			disabled={disabled || saving}
			className="question-bank-scoring"
		>
			<h3>答案与解析</h3>
			<Form.Item
				name="answer"
				label="标准答案"
				extra={'选择题填 A；多选题填 ["A","B"]；判断题填 true 或 false。'}
			>
				<Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} />
			</Form.Item>
			<Form.Item name="equivalent" label="等价答案列表">
				<Input.TextArea autoSize={{ minRows: 1, maxRows: 4 }} />
			</Form.Item>
			<Space wrap>
				<Form.Item name="absolute" label="绝对容差">
					<InputNumber min={0} />
				</Form.Item>
				<Form.Item name="relative" label="相对容差">
					<InputNumber min={0} />
				</Form.Item>
			</Space>
			<Form.Item name="solution" label="解析">
				<Input.TextArea autoSize={{ minRows: 3, maxRows: 10 }} />
			</Form.Item>
			<Form.Item name="steps" label="分步解析列表">
				<Input.TextArea autoSize={{ minRows: 1, maxRows: 6 }} />
			</Form.Item>
			<h3>评分标准</h3>
			<Form.Item
				name="template"
				label="选用已发布的模板版本"
				extra="保存时复制评分点。模板后续更新不会更改本题。"
			>
				<Select
					allowClear
					loading={templates.isLoading}
					options={templates.data?.items
						.filter((t) => t.current_published_version_id)
						.map((t) => ({
							value: t.current_published_version_id as string,
							label: t.item_code,
						}))}
				/>
			</Form.Item>
			{template ? (
				<Alert
					type="info"
					message="选用模板将以该版本的评分点替换当前评分点；分值需一致。"
				/>
			) : null}
			<Form.Item
				name="rubricEnabled"
				label="配置本题评分点"
				valuePropName="checked"
			>
				<Switch />
			</Form.Item>
			{rubricEnabled ? (
				<>
					<Form.List name="points">
						{(fields, { add, remove }) => (
							<>
								{fields.map(({ key, name, ...field }) => (
									<div key={key} className="question-bank-rubric-point">
										<Space wrap>
											<Form.Item
												{...field}
												name={[name, "id"]}
												label="评分点编号"
												rules={[{ required: true }]}
											>
												<Input />
											</Form.Item>
											<Form.Item
												{...field}
												name={[name, "score"]}
												label="分值"
												rules={[{ required: true }]}
											>
												<InputNumber min={0.01} precision={2} />
											</Form.Item>
											<Form.Item
												{...field}
												name={[name, "required"]}
												valuePropName="checked"
											>
												<Checkbox>必须满足</Checkbox>
											</Form.Item>
										</Space>
										<Form.Item
											{...field}
											name={[name, "description"]}
											label="评分说明"
											rules={[{ required: true }]}
										>
											<Input.TextArea />
										</Form.Item>
										<Form.Item
											{...field}
											name={[name, "evidence"]}
											label="证据要求列表"
										>
											<Input.TextArea autoSize={{ minRows: 1, maxRows: 6 }} />
										</Form.Item>
										<Button onClick={() => remove(name)}>移除评分点</Button>
									</div>
								))}
								<Button
									onClick={() =>
										add({
											id: `P${fields.length + 1}`,
											description: "",
											score: 1,
											required: false,
											evidence: "[]",
										})
									}
								>
									添加评分点
								</Button>
							</>
						)}
					</Form.List>
					<Form.Item name="deductions" label="扣分规则列表">
						<Input.TextArea />
					</Form.Item>
					<Form.Item name="examples" label="评分示例列表">
						<Input.TextArea />
					</Form.Item>
				</>
			) : null}
			<h3>附件</h3>
			<ul>
				{assets.map((a) => (
					<li key={a.file_asset_id}>
						{a.name}{" "}
						<Button
							disabled={disabled || saving}
							onClick={() => {
								setAssets(
									assets.filter((x) => x.file_asset_id !== a.file_asset_id),
								);
								onDirty(true);
							}}
						>
							移除绑定
						</Button>
					</li>
				))}
			</ul>
			{canManageFiles ? (
				<>
					<input
						ref={fileInput}
						type="file"
						hidden
						onChange={async (e) => {
							const file = e.target.files?.[0];
							e.target.value = "";
							if (!file || !templates.data?.schoolId) return;
							setSaving(true);
							try {
								const result = await uploadFile(file, {
									owner_type: "generic",
									school_id: templates.data.schoolId,
								});
								const f = result.file;
								setAssets((current) =>
									current.some((a) => a.file_asset_id === f.id)
										? current
										: [
												...current,
												{
													file_asset_id: f.id,
													sha256: f.hash_sha256,
													name: f.original_name,
													content_type: f.content_type,
												},
											],
								);
								onDirty(true);
							} catch (error) {
								message.error(getUserErrorMessage(error, "附件上传失败"));
							} finally {
								setSaving(false);
							}
						}}
					/>
					<Button
						disabled={disabled || saving || !templates.data?.schoolId}
						onClick={() => fileInput.current?.click()}
					>
						添加附件
					</Button>
				</>
			) : null}
			<Form.Item name="use_policy" label="用途">
				<Select
					options={[
						{ value: "practice_only", label: "仅供练习" },
						{ value: "exam_allowed", label: "允许用于正式考试" },
					]}
				/>
			</Form.Item>
			<Button type="primary" htmlType="submit" loading={saving}>
				保存评分标准
			</Button>
		</Form>
	);
}
