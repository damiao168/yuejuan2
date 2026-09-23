import { Form, Input, InputNumber, Select, Switch } from "antd";
import type { QuestionBankMetadataSchema } from "@edugrade/sdk";
import { subjects, types, archetypes } from "./questionBankOptions";

export function ContentFields({ schema }: { schema?: QuestionBankMetadataSchema }) {
	const knowledgePointTaxonomy = schema?.taxonomies.find(
		(taxonomy) => taxonomy.id === "knowledge_points",
	);
	return (
		<>
			<div className="question-bank-form-pair">
				<Form.Item
					name={["metadata", "subject_code"]}
					label="学科"
					rules={[{ required: true }]}
				>
					<Select options={subjects} />
				</Form.Item>
				<Form.Item
					name={["metadata", "education_stage"]}
					label="学段"
					rules={[{ required: true }]}
				>
					<Select
						options={[
							{ value: "junior", label: "初中" },
							{ value: "senior", label: "高中" },
						]}
					/>
				</Form.Item>
			</div>
			<div className="question-bank-form-pair">
				<Form.Item
					name={["metadata", "grade_scope"]}
					label="年级范围"
					rules={[{ required: true, whitespace: true, max: 80 }]}
				>
					<Input maxLength={80} placeholder="如 grade_8" />
				</Form.Item>
				<Form.Item
					name="default_score"
					label="默认分值"
					rules={[{ required: true }]}
				>
					<InputNumber min={0.01} max={100000} precision={2} />
				</Form.Item>
			</div>
			<div className="question-bank-form-pair">
				<Form.Item
					name="question_type"
					label="题型"
					rules={[{ required: true }]}
				>
					<Select options={types} />
				</Form.Item>
				<Form.Item
					name="assessment_archetype"
					label="作答方式"
					rules={[{ required: true }]}
				>
					<Select options={archetypes} />
				</Form.Item>
			</div>
			<Form.Item
				name="stem"
				label="题干"
				rules={[{ required: true, whitespace: true, max: 50000 }]}
			>
				<Input.TextArea rows={5} maxLength={50000} />
			</Form.Item>
			<Form.Item
				name="options"
				label="选项（每行一个，按题目顺序）"
				getValueProps={(value: string[] = []) => ({ value: value.join("\n") })}
				getValueFromEvent={(event: { target: { value: string } }) =>
					event.target.value === "" ? [] : event.target.value.split("\n")
				}
			>
				<Input.TextArea rows={3} />
			</Form.Item>
			<Form.Item
				name="knowledge_points"
				label="知识点"
				normalize={(value: string[]) => [
					...new Set(value.map((v) => v.trim()).filter(Boolean)),
				]}
			>
				<Select
					mode="multiple"
					tokenSeparators={[",", "，"]}
					maxCount={64}
					options={knowledgePointTaxonomy?.terms
						.filter((term) => term.active)
						.map((term) => ({ value: term.id, label: term.label }))}
						showSearch
					placeholder={
						knowledgePointTaxonomy
							? "选择受控知识点"
							: "当前 schema 未配置 knowledge_points taxonomy"
					}
					disabled={!knowledgePointTaxonomy}
				/>
			</Form.Item>
			<div className="question-bank-form-pair">
				<Form.Item name={["metadata", "difficulty_band"]} label="难度标签">
					<Select
						options={[
							{ value: "unclassified", label: "未分类" },
							{ value: "easy", label: "容易" },
							{ value: "medium", label: "中等" },
							{ value: "hard", label: "困难" },
						]}
					/>
				</Form.Item>
				<Form.Item name={["metadata", "copyright"]} label="版权状态">
					<Select
						options={[
							{ value: "unknown", label: "待确认" },
							{ value: "owned", label: "自有" },
							{ value: "licensed", label: "已授权" },
						]}
					/>
				</Form.Item>
			</div>
			<div className="question-bank-form-pair">
				<Form.Item name={["metadata", "cognitive_level"]} label="认知层级">
					<Select
						options={[
							{ value: "unclassified", label: "未分类" },
							{ value: "remember", label: "记忆" },
							{ value: "understand", label: "理解" },
							{ value: "apply", label: "应用" },
							{ value: "analyze", label: "分析" },
							{ value: "evaluate", label: "评价" },
							{ value: "create", label: "创造" },
						]}
					/>
				</Form.Item>
				<Form.Item name={["metadata", "language"]} label="语言">
					<Select
						options={[
							{ value: "zh-CN", label: "中文" },
							{ value: "en", label: "英语" },
						]}
					/>
				</Form.Item>
			</div>
			<div className="question-bank-form-pair">
				<Form.Item name={["metadata", "intended_use"]} label="建议用途">
					<Select options={[
						{value:"practice",label:"练习"},{value:"homework",label:"作业"},{value:"quiz",label:"测验"},{value:"exam",label:"考试"},{value:"mock_exam",label:"模拟考试"},
					]} />
				</Form.Item>
				<Form.Item name={["metadata", "suggested_time_minutes"]} label="建议用时（分钟）">
					<InputNumber min={1} max={1440} />
				</Form.Item>
			</div>
			<Form.Item name={["metadata", "source_year"]} label="来源年份">
				<InputNumber min={1900} max={2200} />
			</Form.Item>
			{schema?.fields.length ? <section className="question-bank-custom-fields">
				<h3>受控自定义 Metadata · schema v{schema.version}</h3>
				<div className="question-bank-form-pair">
					{schema.fields.map((field) => {
						const rules = [{ required: field.required, message: `${field.label}为必填字段` }];
						let control = <Input maxLength={field.max_length ?? undefined} />;
						if (field.type === "number") control = <InputNumber min={field.min ?? undefined} max={field.max ?? undefined} />;
						if (field.type === "boolean") control = <Switch />;
						if (field.type === "enum") control = <Select options={(field.options ?? []).filter(option=>option.active).map(option=>({value:option.value,label:option.label}))} />;
						if (field.type === "taxonomy") {
							const taxonomy = schema.taxonomies.find(value=>value.id===field.taxonomy_id);
							control = <Select showSearch options={(taxonomy?.terms ?? []).filter(term=>term.active).map(term=>({value:term.id,label:term.label}))} />;
						}
						return <Form.Item key={field.key} name={["custom_metadata",field.key]} label={field.label} rules={rules} valuePropName={field.type === "boolean" ? "checked" : "value"}>{control}</Form.Item>;
					})}
				</div>
			</section> : null}
		</>
	);
}
