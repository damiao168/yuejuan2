import { Alert, Button, Input, InputNumber } from 'antd';
import { Plus, Trash2 } from 'lucide-react';
import type { TemplateRegionEditorView } from './templateViewTypes';
import { clamp, coordLabels } from './templateViewRules';

export function RegionGeometry({ view }: Readonly<{ view: TemplateRegionEditorView }>) {
	const { selectedRegion, readonly, updateRegion } = view.editor;
	return (
		<>
			{selectedRegion ? (
				<details className="template-option-editor template-advanced-geometry">
					<summary>高级设置 · 精确坐标</summary>
					<p>以整页为参照，单位为百分比；左、上是距页边的位置，宽、高是区域尺寸。</p>
					<div className="template-coordinate-fields">
						{(["x", "y", "width", "height"] as const).map((key) => (
							<label key={key}>
								<span>{coordLabels[key]}</span>
								<InputNumber
									aria-label={`${view.editor.selectedRegionKind === "identity" ? "个人信息区域" : "答题区域"}${coordLabels[key]}`}
									min={key === "x" || key === "y" ? 0 : 1}
									max={100}
									step={1}
									precision={1}
									suffix="%"
									disabled={readonly}
									value={selectedRegion[key] * 100}
									onChange={(value) => {
										const next = {
											...selectedRegion,
											[key]: Number(value ?? 0) / 100
										};
										next.x = clamp(next.x, 0, 0.99);
										next.y = clamp(next.y, 0, 0.99);
										next.width = clamp(next.width, 0.01, 1 - next.x);
										next.height = clamp(next.height, 0.01, 1 - next.y);
										updateRegion(selectedRegion.id, next);
									}}
								/>
							</label>
						))}
					</div>
				</details>
			) : null}
		</>
	);
}

export function OptionRegionEditor({ view }: Readonly<{ view: TemplateRegionEditorView }>) {
	const {
		selectedRegion,
		selectedQuestion,
		readonly,
		addOptionRegion,
		updateOption,
		removeOptionRegion
	} = view.editor;
	return (
		<>
			{selectedRegion && view.editor.selectedRegionKind !== "identity" &&
			["single_choice", "multiple_choice", "true_false"].includes(
				selectedQuestion?.question_type ?? ""
			) ? (
				<details className="template-option-editor template-advanced-options">
					<summary>高级设置 · 单个选项</summary>
					<div className="template-pane-head">
						<strong>选项标记区域</strong>
						<Button
							size="small"
							icon={<Plus size={14} />}
							disabled={readonly}
							onClick={addOptionRegion}
						>
							添加
						</Button>
					</div>
					<p>选项坐标以本题区域为参照，单位为百分比；每框只覆盖一个填涂点。</p>
					{(selectedRegion.option_regions ?? []).map((option) => (
						<div className="template-option-row" key={option.id}>
							<Input
								aria-label="选项标签"
								value={option.label}
								disabled={readonly}
								maxLength={16}
								onChange={(event) =>
									updateOption(option.id, {
										label: event.target.value.toUpperCase()
									})
								}
							/>
							{(["x", "y", "width", "height"] as const).map((key) => (
								<label key={key}>
									<span>{coordLabels[key]}</span>
									<InputNumber
										aria-label={`选项 ${option.label} ${coordLabels[key]}`}
										min={0}
										max={100}
										step={1}
										precision={1}
										suffix="%"
										disabled={readonly}
										value={option[key] * 100}
										onChange={(value) =>
											updateOption(option.id, { [key]: Number(value ?? 0) / 100 })
										}
									/>
								</label>
							))}
							<Button
								danger
								type="text"
								icon={<Trash2 size={14} />}
								aria-label={`删除选项 ${option.label}`}
								disabled={readonly}
								onClick={() => removeOptionRegion(option.id)}
							/>
						</div>
					))}
					{(selectedRegion.option_regions ?? []).length === 0 ? (
						<Alert
							type="warning"
							showIcon
							message="尚未配置选项框"
							description="此题无法自动识别填涂，将转人工处理。"
						/>
					) : null}
				</details>
			) : null}
		</>
	);
}
