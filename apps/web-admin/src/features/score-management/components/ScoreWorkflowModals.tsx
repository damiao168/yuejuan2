import { Alert, Button, Checkbox, Input, InputNumber, List, Modal, Select, Space } from "antd";
import { ClipboardCheck } from "lucide-react";
import { RegradeCandidateEvidence } from "./RegradeCandidateEvidence";
import { BusinessReasonField } from "../../../components/BusinessReasonField";
import type { Exam } from "../../../api/exams";
import type { useRegradeWorkflow } from "../hooks/useRegradeWorkflow";
import type { useRosterAttendance } from "../hooks/useRosterAttendance";
import {
  releaseVisibilityLabels,
  questionReleaseContent,
  releaseVisibilityPreset,
  type useScoreReleaseWorkflow
} from "../hooks/useScoreReleaseWorkflow";

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

export function ScoreWorkflowModals({
  selectedExam,
  gradeTotal,
  publishedReleaseExists,
  actioning,
  attendanceWorkflow,
  releaseWorkflow,
  regradeWorkflow
}: Readonly<{
  selectedExam?: Exam;
  gradeTotal: number;
  publishedReleaseExists: boolean;
  actioning: string | null;
  attendanceWorkflow: ReturnType<typeof useRosterAttendance>;
  releaseWorkflow: ReturnType<typeof useScoreReleaseWorkflow>;
  regradeWorkflow: ReturnType<typeof useRegradeWorkflow>;
}>) {
  const {
    attendanceEditor, attendanceReason, setAttendanceReason,
    closeAttendanceEditor, saveAttendance
  } = attendanceWorkflow;
  const {
    releaseModalOpen, closeReleaseModal, createRelease, releaseVisibility,
    setReleaseVisibility, releaseReason, setReleaseReason
  } = releaseWorkflow;
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
      open={Boolean(attendanceEditor)}
      title={attendanceEditor?.status === "absent" ? "确认标记缺考" : "确认恢复应考"}
      okText="确认"
      cancelText="取消"
      confirmLoading={actioning === "attendance"}
      onOk={() => void saveAttendance()}
      onCancel={closeAttendanceEditor}
    >
      <Alert
        type={attendanceEditor?.status === "absent" ? "warning" : "info"}
        showIcon
        icon={<ClipboardCheck size={18} />}
        message={attendanceEditor?.entry.student_name ?? "学生"}
        description={attendanceEditor?.status === "absent" ? "缺考学生不计入班级均分；若后续发现答卷，请先恢复应考再完成身份匹配。" : "恢复后该生必须匹配一份完整答卷，才能通过发布检查。"}
      />
      <label className="score-attendance-label" htmlFor="score-attendance-reason">调整原因（必填）</label>
      <Input.TextArea
        id="score-attendance-reason"
        rows={3}
        maxLength={300}
        showCount
        value={attendanceReason}
        placeholder={attendanceEditor?.status === "absent" ? "例如：经监考记录与班主任确认，学生因病缺考" : "例如：已找到并确认该生答卷，恢复为应考"}
        onChange={(event) => setAttendanceReason(event.target.value)}
      />
    </Modal>

    <Modal
      open={regradeReviewOpen}
      title="复核题目重评候选"
      footer={<Button onClick={closeRegradeReview}>关闭</Button>}
      onCancel={closeRegradeReview}
      width={860}
    >
      <Alert showIcon type="info" message="候选意见尚未改写成绩" description="请逐项完成复核。需要更正时，仍须生成并发布新成绩版本；原版本不变。" />
      <List
        dataSource={regradeReview?.items.filter((item) => item.status === "awaiting_review" || item.status === "exception") ?? []}
        locale={{ emptyText: "当前没有待复核的重评候选" }}
        renderItem={(item) => {
          const selections = item.candidate_rubric_selections ?? [];
          return <List.Item actions={[
            <Button key="accept" type="primary" disabled={Boolean(regradeWorkflow.regradeReviewConflicts?.[item.id]) || regradeWorkflow.reviewedEvidence[item.id] !== item.revision || Boolean(actioning)} loading={actioning === `regrade-review-${item.id}`} onClick={() => decideRegradeItem(item, "accept")}>接受</Button>,
            <Button key="reject" disabled={Boolean(regradeWorkflow.regradeReviewConflicts?.[item.id]) || regradeWorkflow.reviewedEvidence[item.id] !== item.revision || Boolean(actioning)} loading={actioning === `regrade-review-${item.id}`} onClick={() => decideRegradeItem(item, "reject")}>驳回</Button>,
            ...(item.status === "exception" ? [] : [<Button key="exception" danger disabled={Boolean(regradeWorkflow.regradeReviewConflicts?.[item.id]) || regradeWorkflow.reviewedEvidence[item.id] !== item.revision || Boolean(actioning)} loading={actioning === `regrade-review-${item.id}`} onClick={() => decideRegradeItem(item, "exception")}>标记异常</Button>])
          ]}>
            <List.Item.Meta
              title={<Space><strong>重评候选</strong><span>建议得分</span><InputNumber min={0} max={item.max_score} precision={1} value={regradeReviewScores[item.id] ?? undefined} onChange={(value) => setRegradeReviewScores((current) => ({ ...current, [item.id]: value === null ? null : Number(value) }))} /><span>/ {item.max_score}</span></Space>}
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
      open={releaseModalOpen}
      title="创建正式成绩发布草稿"
      okText="创建草稿"
      cancelText="取消"
      confirmLoading={actioning === "release-create"}
      onOk={createRelease}
      onCancel={closeReleaseModal}
    >
      <Alert type="info" showIcon message="创建草稿，不会公开给学生" description="成绩和以下公开设置将随草稿冻结。核对学生预览后，再正式发布。" />
      <p><strong>{selectedExam?.name}</strong> · {gradeTotal} 份成绩</p>
      {selectedExam?.appeal_enabled ? <Alert type={releaseVisibility.show_question_scores ? "info" : "error"} showIcon message="允许学生申请成绩复核" description="开放复核须公开逐题得分；当前仅支持逐题申请。" /> : null}
      <Space wrap><Button onClick={() => setReleaseVisibility(releaseVisibilityPreset("basic", Boolean(selectedExam?.appeal_enabled)))}>使用基础公开设置</Button><Button onClick={() => setReleaseVisibility(releaseVisibilityPreset("question", Boolean(selectedExam?.appeal_enabled)))}>使用逐题公开设置</Button></Space>
      {([
        { title: "成绩与排名", keys: ["show_cohort_statistics", "show_percentile", "show_exact_rank", "show_score_distribution"] },
        { title: "逐题内容", keys: ["show_question_scores", ...questionReleaseContent] }
      ] as const).map(group => <fieldset key={group.title} className="release-visibility-options"><legend>{group.title}</legend>{group.keys.map(key => <Checkbox key={key} checked={releaseVisibility[key]} disabled={(questionReleaseContent.has(key) && !releaseVisibility.show_question_scores) || (key === "show_question_scores" && Boolean(selectedExam?.appeal_enabled) && releaseVisibility.show_question_scores)} onChange={(event) => setReleaseVisibility((current) => ({ ...current, [key]: event.target.checked, ...(key === "show_question_scores" && !event.target.checked ? { show_high_score_paper: false } : {}) }))}>{releaseVisibilityLabels[key]}</Checkbox>)}</fieldset>)}
      {!releaseVisibility.show_question_scores ? <p>关闭后不显示任何逐题详情，包括题目、答案和反馈。</p> : null}
      {publishedReleaseExists ? <><label className="score-attendance-label" htmlFor="score-release-reason">更正说明（必填）</label><Input.TextArea id="score-release-reason" rows={3} maxLength={1000} showCount value={releaseReason} placeholder="请写明本次更正的范围与依据" onChange={event => setReleaseReason(event.target.value)} /></> : <BusinessReasonField id="score-release-reason" label="发布依据（必选）" value={releaseReason} onChange={setReleaseReason} choices={["本场成绩首次正式发布", "已完成复核，正式发布本场成绩"]} />}
      {releaseVisibility.show_high_score_paper ? <Alert type="warning" showIcon message="发布前自动生成匿名范例卷" description="最高分答卷每页须有已锁定模板、成功配准和有效身份区，否则无法发布。请逐页核对身份区确实覆盖姓名、学号、二维码等身份信息；系统仅按配置区域遮挡，不能判断是否遗漏。" /> : null}
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
        <label>题目<Select value={regradeQuestionId || undefined} placeholder="选择题目" options={regradeQuestionOptions} onChange={(value) => { setRegradeQuestionId(value); clearRegradePreview(); }} /></label>
        <label>原因<Select value={regradeReasonCode} options={regradeReasonOptions} onChange={setRegradeReasonCode} /></label>
        <label>处理方式<Select value={regradeStrategy} options={regradeStrategyOptions} onChange={setRegradeStrategy} /></label>
        <label>分派阅卷员<Select value={regradeAssigneeID || undefined} placeholder="选择负责本次重评的阅卷员" options={regradeGraderOptions} loading={loadingRegradeGraders} onChange={setRegradeAssigneeID} /></label>
        {regradeGradersError ? <Alert type="warning" showIcon message={regradeGradersError} description="请先在“组织与账号”中创建或启用阅卷员，再发起重评。" /> : null}
        <label>处理说明<Input.TextArea rows={3} maxLength={2000} showCount value={regradeReasonText} placeholder="说明为什么需要本题复评" onChange={(event) => setRegradeReasonText(event.target.value)} /></label>
        <Button onClick={() => void previewSelectedRegrade()} loading={previewingRegrade} disabled={!regradeQuestionId || !publishedReleaseExists}>预览影响范围</Button>
        {regradePreview ? <Alert type="info" showIcon message={`来源：正式成绩 V${regradePreview.source_release_version} · 影响 ${regradePreview.affected_count} 份答卷`} description="预览仅用于核对范围；创建时将冻结该版本对应的题目范围。" /> : null}
      </div>
      <p>任务完成后，需生成并发布新成绩版本。</p>
    </Modal>
  </>;
}
