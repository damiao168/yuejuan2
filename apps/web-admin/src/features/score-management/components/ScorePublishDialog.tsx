import { useState } from "react";
import { Alert, Button, Checkbox, Input, InputNumber, Modal, Radio } from "antd";
import type { Exam } from "../../../api/exams";
import type { RosterEntry } from "../../../api/scores";
import { examSubjectLabel } from "../../../constants/examStatus";
import type { RosterCounts } from "../releaseStatus";
import type { ExamEducationStage } from "../hooks/useExamEducationStage";
import {
  maxHighScoreTopN, questionReleaseContent, rankVisibilityKeys, releaseVisibilityLabels, revealsRank, visibleReleaseContent,
  type ReleaseVisibility, type useScoreReleaseWorkflow
} from "../hooks/useScoreReleaseWorkflow";

type Workflow = Pick<ReturnType<typeof useScoreReleaseWorkflow>,
  "publishOpen" | "closePublish" | "republish" | "preset" | "choosePreset" | "visibility" | "setVisibility" |
  "rankReason" | "setRankReason" | "highScoreTopN" | "setHighScoreTopN" | "correctionNote" | "setCorrectionNote" | "submitPublish" | "previewPublish" | "awaitingAuthentication">;

type DialogProps = Readonly<{
  workflow: Workflow;
  exam?: Exam;
  counts: RosterCounts | null;
  educationStage: ExamEducationStage;
  actioning: string | null;
  /** 页面正在重读或已有提交进行中时为 false；不能依据旧快照提交。 */
  canWrite: boolean;
  /** 按缺考计的学生；在弹窗内展开核对，避免再叠一层抽屉。 */
  absentees: RosterEntry[];
}>;

const statisticKeys = ["show_cohort_statistics", "show_score_distribution"] as const;

/** 一步发布：确认发给谁、公开什么，然后生成草稿并发布。学生视角预览可选，不是必经步骤。 */
export function ScorePublishDialog(props: DialogProps) {
  const { workflow, actioning, canWrite } = props;
  const busy = actioning === "release-publish" || actioning === "release-preview";
  const waitingForPassword = actioning === "release-publish" && workflow.awaitingAuthentication;
  return <Modal
    open={workflow.publishOpen}
    title={workflow.republish ? "重新发布成绩" : "发布成绩"}
    width={640}
    onCancel={busy ? undefined : workflow.closePublish}
    maskClosable={!busy}
    destroyOnHidden
    footer={[
      <Button key="preview" disabled={busy || !canWrite} loading={actioning === "release-preview"} onClick={workflow.previewPublish}>查看学生视角</Button>,
      <Button key="cancel" disabled={busy} onClick={workflow.closePublish}>取消</Button>,
      <Button key="publish" type="primary" disabled={busy || !canWrite} loading={actioning === "release-publish" && !waitingForPassword} onClick={workflow.submitPublish}>{waitingForPassword ? "等待身份验证" : workflow.republish ? "重新发布" : "发布"}</Button>
    ]}
  >
    {/* 每次打开都重建内容，“公开排名”等局部开关不会沿用上一次的选择。 */}
    {workflow.publishOpen ? <>
      {waitingForPassword ? <Alert type="info" showIcon message="请在身份验证窗口输入当前账号密码" description="验证后会继续发布；取消验证会保留当前草稿，不会发布成绩。" /> : null}
      <PublishDialogBody {...props} />
    </> : null}
  </Modal>;
}

function PublishDialogBody({ workflow, exam, counts, educationStage, absentees }: DialogProps) {
  const { visibility, setVisibility } = workflow;
  const appealEnabled = Boolean(exam?.appeal_enabled);
  // 高中可以直接勾选排名；义务教育学段和读不到学段时，排名项要先单独打开并填写原因。
  const senior = educationStage === "senior";
  const [rankUnlocked, setRankUnlocked] = useState(revealsRank(visibility));
  const rankKeys = senior || rankUnlocked ? rankVisibilityKeys : [];
  const option = (key: keyof ReleaseVisibility) => <Checkbox key={key} checked={visibility[key]}
    disabled={(questionReleaseContent.has(key) && !visibility.show_question_scores) || (key === "show_question_scores" && appealEnabled && visibility.show_question_scores)}
    onChange={(event) => setVisibility(current => ({ ...current, [key]: event.target.checked, ...(key === "show_question_scores" && !event.target.checked ? { show_high_score_paper: false } : {}) }))}>
    {releaseVisibilityLabels[key]}
  </Checkbox>;

  return <div className="score-publish-dialog">
    <p className="score-publish-target"><strong>{exam?.name ?? "未选择考试"}</strong>{exam ? ` · ${examSubjectLabel(exam.subject)}` : ""}</p>
    {counts ? <p>
      将向 <strong>{counts.withScores}</strong> 名学生发布成绩
      {counts.absent ? <>；<strong>{counts.absent}</strong> 名学生没有匹配答卷，按缺考计</> : null}。
    </p> : <p>考试名单尚未读取完整，发布时由服务端按冻结名单核对。</p>}
    {absentees.length ? <details className="score-publish-absentees">
      <summary>查看按缺考计的学生</summary>
      <ul>{absentees.map(entry => <li key={entry.key}>{entry.student_name ?? "未命名学生"}{entry.student_no ? ` · ${entry.student_no}` : ""}{entry.class_name ? ` · ${entry.class_name}` : ""}</li>)}</ul>
      <p>如有漏扫或未识别的答卷，请先到“答卷与匹配”补齐再发布。</p>
    </details> : null}

    <fieldset className="score-publish-section">
      <legend>学生能看到</legend>
      <Radio.Group value={workflow.preset} onChange={(event) => workflow.choosePreset(event.target.value)}>
        <Radio value="basic">只公开成绩</Radio>
        <Radio value="question">公开逐题详情</Radio>
        <Radio value="custom">自定义</Radio>
      </Radio.Group>
      {/* 摘要按实际生效的公开项生成，不用固定文案描述预设，避免与开关不一致。 */}
      <p className="score-publish-summary">学生将看到：{["总分", ...visibleReleaseContent(visibility)].join("、")}</p>
      {workflow.preset === "custom" ? <div className="score-publish-custom">
        <fieldset className="release-visibility-options"><legend>成绩与统计</legend>
          {statisticKeys.map(option)}
          {!senior ? <Checkbox checked={rankUnlocked} onChange={(event) => {
            setRankUnlocked(event.target.checked);
            if (!event.target.checked) setVisibility(current => ({ ...current, show_exact_rank: false, show_percentile: false }));
          }}>公开排名或百分位</Checkbox> : null}
          {rankKeys.map(option)}
        </fieldset>
        <fieldset className="release-visibility-options"><legend>逐题内容</legend>
          {(["show_question_scores", ...questionReleaseContent] as Array<keyof ReleaseVisibility>).filter(key => key !== "show_answers" && key !== "show_high_score_paper").map(option)}
        </fieldset>
        {!visibility.show_question_scores ? <p>关闭后不显示任何逐题详情，包括题目、答案和反馈。</p> : null}
      </div> : null}
    </fieldset>

    <fieldset className="score-publish-section">
      <legend>优秀答卷</legend>
      <div className="score-publish-excellent-row">
        <Checkbox checked={visibility.show_high_score_paper} onChange={event => {
          const enabled = event.target.checked;
          // 优秀答卷依赖逐题内容，只自动开启这项必要条件，不连带开放题干、反馈或统计。
          if (enabled && !visibility.show_question_scores) workflow.choosePreset("custom");
          setVisibility(current => ({ ...current, show_high_score_paper: enabled, ...(enabled ? { show_question_scores: true } : {}) }));
        }}>提供优秀答卷</Checkbox>
        <label htmlFor="score-publish-excellent-top-n">取本校前</label>
        <InputNumber<number> id="score-publish-excellent-top-n" aria-label="优秀答卷名次范围" min={1} max={maxHighScoreTopN} step={1} precision={0}
          value={workflow.highScoreTopN} disabled={!visibility.show_high_score_paper} onChange={workflow.setHighScoreTopN} />
        <span>名</span>
      </div>
      <p className="score-publish-hint">按本场考试、本学科的本校成绩选取；截止名次同分全部纳入，实际份数可能更多。</p>
    </fieldset>

    {!senior && revealsRank(visibility) ? <div className="score-publish-section">
      <Alert type="warning" showIcon message="义务教育学段考试结果默认不排名、不公布" description="依据《教育部办公厅关于加强义务教育学校考试管理的通知》（教基厅函〔2021〕34号）。确需公开排名或百分位时，请写明原因，原因随本次发布保存。" />
      <label className="score-attendance-label" htmlFor="score-publish-rank-reason">公开排名原因（必填）</label>
      <Input.TextArea id="score-publish-rank-reason" rows={2} maxLength={150} showCount value={workflow.rankReason} onChange={event => workflow.setRankReason(event.target.value)} />
    </div> : null}

    {appealEnabled ? <Alert type={visibility.show_question_scores ? "info" : "error"} showIcon message="成绩复核：已开放" description="学生在开放时段内从逐题详情申请复核，因此必须公开逐题得分。" /> : <p>成绩复核：未开放</p>}
    {visibility.show_high_score_paper ? <Alert type="warning" showIcon message="发布前自动生成匿名优秀答卷" description="所选答卷每页须有已锁定模板、成功配准和有效身份区，否则无法发布。请逐页核对身份区确实覆盖姓名、学号、二维码等身份信息；系统仅按配置区域遮挡，不能判断是否遗漏。" /> : null}

    {workflow.republish ? <div className="score-publish-section">
      <label className="score-attendance-label" htmlFor="score-publish-note">重新发布原因（必填）</label>
      <Input.TextArea id="score-publish-note" rows={3} maxLength={800} showCount value={workflow.correctionNote} placeholder="例如：补充公开逐题详情" onChange={event => workflow.setCorrectionNote(event.target.value)} />
    </div> : null}
    <p className="score-publish-hint">发布后学生立即可以查看。之后需要更正时，通过题目级复评发布更正。</p>
  </div>;
}
