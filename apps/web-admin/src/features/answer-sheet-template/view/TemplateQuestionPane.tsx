import { useState } from 'react';
import { Button, Checkbox, Input, List } from 'antd';
import { StatusTag } from '../../../components/StatusTag';
import type { TemplateQuestionPaneView } from './templateViewTypes';

export function TemplateQuestionPane({ view }: Readonly<{ view: TemplateQuestionPaneView }>) {
	const { questions } = view;
	const [onlyMissing, setOnlyMissing] = useState(false);
	const [search, setSearch] = useState("");
	const {
		selectedQuestionId,
		setSelectedQuestionId,
		coveredQuestions,
		readonly,
		createRegionWithoutDragging
	} = view.editor;
	const shownQuestions = questions.filter(question => (!onlyMissing || !coveredQuestions.has(question.id)) && (!search.trim() || `${question.question_no} ${question.stem ?? ""}`.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase())));
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
			<Input aria-label="搜索要配置的题目" placeholder="搜索题号或题干" value={search} onChange={event => setSearch(event.target.value)} />
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
						{!coveredQuestions.has(question.id) && !readonly ? <Button size="small" onClick={event => { event.stopPropagation(); createRegionWithoutDragging(question.id); }}>添加区域</Button> : <StatusTag
							tone={coveredQuestions.has(question.id) ? "success" : "warning"}
						>
							{coveredQuestions.has(question.id) ? "已框选" : "待框选"}
						</StatusTag>}
					</List.Item>
				)}
			/>
		</aside>
	);
}
