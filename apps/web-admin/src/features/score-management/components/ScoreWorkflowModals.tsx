import { Alert, Button, Checkbox, Input, InputNumber, List, Modal, Select, Space } from "antd";
import { ClipboardCheck } from "lucide-react";
import type { Exam } from "../../../api/exams";
import type { useRegradeWorkflow } from "../hooks/useRegradeWorkflow";
import type { useRosterAttendance } from "../hooks/useRosterAttendance";
import {
  releaseVisibilityLabels,
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
}: {
  selectedExam?: Exam;
  gradeTotal: number;
  publishedReleaseExists: boolean;
  actioning: string | null;
  attendanceWorkflow: ReturnType<typeof useRosterAttendance>;
  releaseWorkflow: ReturnType<typeof useScoreReleaseWorkflow>;
  regradeWorkflow: ReturnType<typeof useRegradeWorkflow>;
}) {
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
    setRegradeReviewScores, decideRegradeItem, regradeModalOpen, regradePreview,
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
      <Alert showIcon type="info" message="候选意见尚未改写成绩" description="逐项接受后才能完成复评。完成后仍需生成并发布新的成绩版本，原发布版本保持不变。" />
      <List
        dataSource={regradeReview?.items.filter((item) => item.status === "awaiting_review") ?? []}
        locale={{ emptyText: "当前没有待复核的重评候选" }}
        renderItem={(item) => {
          const selections = item.candidate_rubric_selections ?? [];
          return <List.Item actions={[
            <Button key="accept" type="primary" loading={actioning === `regrade-review-${item.id}`} onClick={() => decideRegradeItem(item, "accept")}>接受</Button>,
            <Button key="reject" loading={actioning === `regrade-review-${item.id}`} onClick={() => decideRegradeItem(item, "reject")}>驳回</Button>,
            <Button key="exception" danger loading={actioning === `regrade-review-${item.id}`} onClick={() => decideRegradeItem(item, "exception")}>标记异常</Button>
          ]}>
            <List.Item.Meta
              title={<Space><strong>重评候选</strong><span>建议得分</span><InputNumber min={0} max={item.max_score} precision={1} value={regradeReviewScores[item.id] ?? undefined} onChange={(value) => setRegradeReviewScores((current) => ({ ...current, [item.id]: value === null ? null : Number(value) }))} /><span>/ {item.max_score}</span></Space>}
              description={<div className="score-regrade-review-detail">
                {selections.length ? <span>采分点：{selections.map((selection) => `${selection.point_id} ${selection.score}分`).join("；")}</span> : <span>未记录采分点明细</span>}
                {item.candidate_comment ? <span>评分依据：{item.candidate_comment}</span> : null}
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
      <Alert type="info" showIcon message="草稿不会立即对学生生效" description="提交后会冻结当前已确认的成绩事实。请在发布门禁通过后，单独确认发布。" />
      <p><strong>{selectedExam?.name}</strong> · {gradeTotal} 份成绩</p>
      <fieldset className="release-visibility-options"><legend>学生可见内容</legend>{(Object.keys(releaseVisibilityLabels) as Array<keyof typeof releaseVisibilityLabels>).map((key) => <Checkbox key={key} checked={releaseVisibility[key]} disabled={key === "show_high_score_paper" && !releaseVisibility.show_question_scores} onChange={(event) => setReleaseVisibility((current) => ({ ...current, [key]: event.target.checked, ...(key === "show_question_scores" && !event.target.checked ? { show_high_score_paper: false } : {}) }))}>{releaseVisibilityLabels[key]}</Checkbox>)}</fieldset>
      <label className="score-attendance-label" htmlFor="score-release-reason">发布说明（必填）</label>
      <Input.TextArea id="score-release-reason" rows={3} maxLength={1000} showCount value={releaseReason} placeholder="例如：期末考试首次正式发布" onChange={(event) => setReleaseReason(event.target.value)} />
      {releaseVisibility.show_high_score_paper ? <Alert type="warning" showIcon message="发布前自动生成匿名范例卷" description="请确认最高分答卷的每页均有已锁定模板、完成页面配准，并配置覆盖姓名、学号和二维码的身份区域；缺少任一条件将阻断发布。" /> : null}
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
      <Alert type="warning" showIcon message="复评不会直接修改已发布成绩" description="先预览影响范围并创建任务；任务完成后生成新的成绩版本，再通过发布门禁正式发布。" />
      <div className="score-regrade-form">
        <label>题目<Select value={regradeQuestionId || undefined} placeholder="选择题目" options={regradeQuestionOptions} onChange={(value) => { setRegradeQuestionId(value); clearRegradePreview(); }} /></label>
        <label>原因<Select value={regradeReasonCode} options={regradeReasonOptions} onChange={setRegradeReasonCode} /></label>
        <label>处理方式<Select value={regradeStrategy} options={regradeStrategyOptions} onChange={setRegradeStrategy} /></label>
        <label>分派阅卷员<Select value={regradeAssigneeID || undefined} placeholder="选择负责本次重评的阅卷员" options={regradeGraderOptions} loading={loadingRegradeGraders} onChange={setRegradeAssigneeID} /></label>
        {regradeGradersError ? <Alert type="warning" showIcon message={regradeGradersError} description="请先在“组织与账号”中创建或启用阅卷员，再发起重评。" /> : null}
        <label>处理说明<Input.TextArea rows={3} maxLength={2000} showCount value={regradeReasonText} placeholder="说明为什么需要本题复评" onChange={(event) => setRegradeReasonText(event.target.value)} /></label>
        <Button onClick={() => void previewSelectedRegrade()} loading={previewingRegrade} disabled={!regradeQuestionId || !publishedReleaseExists}>预览影响范围</Button>
        {regradePreview ? <Alert type="info" showIcon message={`将影响 ${regradePreview.affected_count} 份答卷`} description={`来源：正式成绩 V${regradePreview.source_release_version}。预览只用于确认范围，创建时系统会再次冻结该版本对应的题目事实。`} /> : null}
      </div>
    </Modal>
  </>;
}
