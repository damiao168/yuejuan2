import { Alert, Button, Input, InputNumber, List, Modal, Select, Space } from "antd";
import { RegradeCandidateEvidence } from "./RegradeCandidateEvidence";
import type { useRegradeWorkflow } from "../hooks/useRegradeWorkflow";

const regradeReasonOptions = [
  { label: "答案错误", value: "answer_key_error" },
  { label: "评分细则错误", value: "rubric_error" },
  { label: "识别结果更正", value: "ocr_correction" },
  { label: "解析规则问题", value: "parser_bug" },
  { label: "模型评分问题", value: "model_issue" },
  { label: "质量事件", value: "quality_incident" },
  { label: "申诉集中问题", value: "appeal_pattern" },
  { label: "其他", value: "other" }
];
const regradeStrategyOptions = [
  { label: "人工复核", value: "human_recheck" },
  { label: "规则重新计算", value: "rule_recompute" },
  { label: "AI 重算后人工复核", value: "ai_recompute_then_review" },
  { label: "导入回标结果", value: "backmark_import" }
];

/** 题目级复评的“复核候选”和“发起复评”弹窗；状态与提交守卫都在 useRegradeWorkflow 中。 */
export function RegradeWorkflowModals({
  publishedReleaseExists,
  actioning,
  regradeWorkflow
}: Readonly<{
  publishedReleaseExists: boolean;
  actioning: string | null;
  regradeWorkflow: ReturnType<typeof useRegradeWorkflow>;
}>) {
  const {
    regradeReviewOpen, closeRegradeReview, regradeReview, regradeReviewScores,
    setRegradeReviewScores, regradeReviewNotes, setRegradeReviewNotes, decideRegradeItem, regradeModalOpen, regradePreview,
    regradeReasonText, regradeAssigneeID, createSelectedRegrade, closeRegradeModal,
    clearRegradePreview, regradeQuestionId, regradeQuestionOptions, setRegradeQuestionId,
    regradeReasonCode, setRegradeReasonCode, regradeStrategy, setRegradeStrategy,
    regradeGraderOptions, loadingRegradeGraders, setRegradeAssigneeID,
    regradeGradersError, setRegradeReasonText, previewSelectedRegrade,
    previewingRegrade
  } = regradeWorkflow;

  return <>
    <Modal
      open={regradeReviewOpen}
      title="复核题目重评候选"
      footer={<Button onClick={closeRegradeReview}>关闭</Button>}
      onCancel={closeRegradeReview}
      width={860}
    >
      <Alert showIcon type="info" message="候选意见尚未改写成绩" description="请逐项完成复核。复评完成后，须到成绩发布页发布更正，学生才会看到新成绩；已发布的版本不变。" />
      <List
        dataSource={regradeReview?.items.filter((item) => item.status === "awaiting_review" || item.status === "exception") ?? []}
        locale={{ emptyText: "当前没有待复核的重评候选" }}
        renderItem={(item) => {
          const selections = item.candidate_rubric_selections ?? [];
          // 核对过当前修订号的材料且没有未处理的修订冲突，才开放三个决定；服务端仍按修订号拒绝过期提交。
          const decisionLocked = Boolean(regradeWorkflow.regradeReviewConflicts?.[item.id]) || regradeWorkflow.reviewedEvidence[item.id] !== item.revision || Boolean(actioning);
          return <List.Item actions={[
            <Button key="accept" type="primary" disabled={decisionLocked} loading={actioning === `regrade-review-${item.id}`} onClick={() => decideRegradeItem(item, "accept")}>接受</Button>,
            <Button key="reject" disabled={decisionLocked} loading={actioning === `regrade-review-${item.id}`} onClick={() => decideRegradeItem(item, "reject")}>驳回</Button>,
            ...(item.status === "exception" ? [] : [<Button key="exception" danger disabled={decisionLocked} loading={actioning === `regrade-review-${item.id}`} onClick={() => decideRegradeItem(item, "exception")}>标记异常</Button>])
          ]}>
            <List.Item.Meta
              title={<Space><strong>重评候选</strong><span>建议得分</span><InputNumber min={0} max={item.max_score} precision={2} step={0.01} value={regradeReviewScores[item.id] ?? undefined} onChange={(value) => setRegradeReviewScores((current) => ({ ...current, [item.id]: value === null ? null : Number(value) }))} /><span>/ {item.max_score}</span></Space>}
              description={<div className="score-regrade-review-detail">
                {regradeWorkflow.regradeReviewConflicts?.[item.id] ? <Alert type="warning" showIcon message="候选已更新，本页修改已保留" description="请先核对新材料，再选择如何处理尚未提交的分数和说明。" action={<Space wrap><Button disabled={Boolean(actioning) || regradeWorkflow.reviewedEvidence[item.id] !== item.revision} onClick={() => regradeWorkflow.resolveReviewConflict(item.id, true)}>保留本页修改</Button><Button disabled={Boolean(actioning) || regradeWorkflow.reviewedEvidence[item.id] !== item.revision} onClick={() => regradeWorkflow.resolveReviewConflict(item.id, false)}>重置为新候选</Button></Space>} /> : null}
                {item.status === "exception" ? <Alert type="warning" showIcon message="异常待处理" description={item.review_note || "请核对证据，填写处理说明后接受或驳回。"} /> : null}
                {selections.length ? <span>采分点：{selections.map((selection) => `${selection.point_id} ${selection.score}分`).join("；")}</span> : <span>未记录采分点明细</span>}
                {item.candidate_comment ? <span>评分依据：{item.candidate_comment}</span> : null}
                <label>复核说明{item.status === "exception" ? "（处理异常必填）" : "（选填）"}<Input.TextArea rows={2} maxLength={2000} value={regradeReviewNotes[item.id] ?? ""} onChange={event => setRegradeReviewNotes(current => ({ ...current, [item.id]: event.target.value }))} /></label>
                <RegradeCandidateEvidence key={`${item.id}:${item.revision}`} id={item.id} revision={item.revision} onReady={revision => regradeWorkflow.markEvidenceReviewed(item.id, revision)} />
              </div>}
            />
          </List.Item>;
        }}
      />
    </Modal>

    <Modal
      open={regradeModalOpen}
      title="发起题目级复评"
      okText="创建复评任务"
      cancelText="取消"
      okButtonProps={{ disabled: !regradePreview || !regradeReasonText.trim() || !regradeAssigneeID }}
      confirmLoading={actioning === "regrade-create"}
      onOk={createSelectedRegrade}
      onCancel={() => { closeRegradeModal(); clearRegradePreview(); }}
    >
      <Alert type="warning" showIcon message="当前公布成绩不变" description="请先预览影响范围，再创建复评任务。" />
      <div className="score-regrade-form">
        {/* 换题后旧预览不再代表影响范围；创建时 hook 还会核对预览的题目和来源版本。 */}
        <label>题目<Select value={regradeQuestionId || undefined} placeholder="选择题目" options={regradeQuestionOptions} onChange={(value) => { setRegradeQuestionId(value); clearRegradePreview(); }} /></label>
        <label>原因<Select value={regradeReasonCode} options={regradeReasonOptions} onChange={setRegradeReasonCode} /></label>
        <label>处理方式<Select value={regradeStrategy} options={regradeStrategyOptions} onChange={setRegradeStrategy} /></label>
        <label>分派阅卷员<Select value={regradeAssigneeID || undefined} placeholder="选择负责本次重评的阅卷员" options={regradeGraderOptions} loading={loadingRegradeGraders} onChange={setRegradeAssigneeID} /></label>
        {regradeGradersError ? <Alert type="warning" showIcon message={regradeGradersError} description="请先在“组织与账号”中创建或启用阅卷员，再发起重评。" /> : null}
        <label>处理说明<Input.TextArea rows={3} maxLength={2000} showCount value={regradeReasonText} placeholder="说明为什么需要本题复评" onChange={(event) => setRegradeReasonText(event.target.value)} /></label>
        <Button onClick={() => void previewSelectedRegrade()} loading={previewingRegrade} disabled={!regradeQuestionId || !publishedReleaseExists}>预览影响范围</Button>
        {regradePreview ? <Alert type="info" showIcon message={`来源：正式成绩 V${regradePreview.source_release_version} · 影响 ${regradePreview.affected_count} 份答卷`} description="预览仅用于核对范围；创建时将冻结该版本对应的题目范围。" /> : null}
      </div>
      <p>复评完成后，须到成绩发布页发布更正，学生才会看到新成绩。</p>
    </Modal>
  </>;
}
