import { useState } from 'react';
import { Button, Checkbox, List, Select } from 'antd';
import { StatusTag } from '../../../components/StatusTag';
import type { TemplateQuestionPaneView } from './templateViewTypes';

export function TemplateQuestionPane({ view }: Readonly<{ view: TemplateQuestionPaneView }>) {
	const { questions } = view;
	const [onlyMissing, setOnlyMissing] = useState(false);
	const {
		selectedQuestionId,
		setSelectedQuestionId,
		coveredQuestions,
		readonly,
		createRegionWithoutDragging
	} = view.editor;
	const shownQuestions = onlyMissing ? questions.filter(question => !coveredQuestions.has(question.id)) : questions;
  const locateQuestion = (id: string) => {
    setSelectedQuestionId(id);
    const page = view.editor.layout.pages.find(item => item.question_regions.some(region => region.question_id === id));
    const region = page?.question_regions.find(item => item.question_id === id);
    if (page && region) {
      view.previewController.setPageNo(page.page_no); view.editor.setSelectedRegionId(region.id);
      // 再次点击同一道题也要重新定位；框本身的 pointerdown 不触发滚动，避免拖动起点跳走。
      requestAnimationFrame(() => [...document.querySelectorAll<HTMLElement>("[data-region-id]")].find(element => element.dataset.regionId === region.id)?.scrollIntoView?.({ block: "center", inline: "center" }));
    } else view.editor.setSelectedRegionId("");
  };
	return (
		<aside
			id="template-question-pane"
			className="template-question-pane"
		>
			<div className="template-pane-head">
				<strong>题目</strong>
				<span>
					{coveredQuestions.size}/{questions.length}
				</span>
			</div>
			<div className="template-question-filter">
				<Checkbox checked={onlyMissing} onChange={event => setOnlyMissing(event.target.checked)}>只看未配置</Checkbox>
				<span>未配置 {questions.filter(question => !coveredQuestions.has(question.id)).length} 题</span>
			</div>
			<Select
				aria-label="选择要配置的题目"
				value={selectedQuestionId || undefined}
				placeholder="选择题目"
				options={questions.map((question) => ({
					value: question.id,
					label: `第 ${question.question_no} 题 · ${question.score} 分`
				}))}
				onChange={locateQuestion}
			/>
			<Button
				block
				disabled={readonly || !selectedQuestionId}
				onClick={createRegionWithoutDragging}
			>
				创建或定位答题区域
			</Button>
			<List
				dataSource={shownQuestions}
				locale={{ emptyText: onlyMissing && questions.length ? "所有题目均已配置区域" : "尚未配置题目" }}
				renderItem={(question) => (
					<List.Item
						className={
							selectedQuestionId === question.id
								? "template-question active"
								: "template-question"
						}
						onClick={() => locateQuestion(question.id)}
					>
						<div>
							<strong>{question.question_no}</strong>
							<span>{question.score} 分</span>
						</div>
						<StatusTag
							tone={coveredQuestions.has(question.id) ? "success" : "warning"}
						>
							{coveredQuestions.has(question.id) ? "已框选" : "待框选"}
						</StatusTag>
					</List.Item>
				)}
			/>
		</aside>
	);
}
