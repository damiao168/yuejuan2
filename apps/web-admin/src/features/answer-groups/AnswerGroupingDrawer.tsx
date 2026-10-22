import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  App,
  Button,
  Descriptions,
  Divider,
  Drawer,
  Empty,
  Input,
  InputNumber,
  List,
  Modal,
  Popconfirm,
  Progress,
  Select,
  Space,
  Spin,
  Tag,
  Tooltip
} from "antd";
import type { AnswerGroup, AnswerGroupMetrics, TeacherReferenceCase } from "@edugrade/sdk";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { listQuestions, type Question } from "../../api/papers";
import {
  buildAnswerGroups,
  confirmAnswerGroup,
  getAnswerGroupMetrics,
  listAnswerGroups,
  reviewAnswerGroupSample,
  rollbackAnswerGroup,
  saveAnswerGroupDecision
} from "../../api/answerGroups";
import { confirmBlockReason, sampleRoleLabels } from "./answerGroupingPresentation";
import "./answer-grouping.css";
import { AnswerGroupSampleEvidence } from "./AnswerGroupSampleEvidence";
import type { AnswerGroupMemberContext } from "../../api/answerGroups";
import { AnswerGroupManualHandoff } from "./AnswerGroupManualHandoff";
import { questionTypeOptions } from "../../constants/examCatalog";

function errorMessage(error: unknown) {
  if (error instanceof ApiClientError) {
    if (error.code === "answer_group_sampling_incomplete") return "抽检未满足策略：请完成最低抽检数量并检查全部异常样本。";
    if (error.code === "answer_group_revision_conflict") return "候选已被其他人更新，请刷新后再操作。";
    if (error.code === "answer_group_no_eligible_answers") return "该题没有可可靠文本化的短答或精确文本答案，不能分组。";
    return getUserErrorMessage(error, "答案分组操作失败");
  }
  return getUserErrorMessage(error, "答案分组操作失败");
}

const statusLabels: Record<string, string> = {
  sampling: "抽检中",
  ready_for_confirmation: "可确认",
  confirmed: "已生成候选",
  rolled_back: "已回滚"
};

function sampleRoleColor(label: string) {
  if (label === "异常样本") return "red";
  if (label === "边界样本") return "orange";
  return "blue";
}

function sampleReviewLabel(status?: string) {
  if (!status) return "未抽检";
  if (status === "accepted") return "已接受";
  return "已拒绝";
}

export function AnswerGroupingDrawer({ open, examId, initialQuestionId = "", availableQuestions, canManage, canReadReview = false, onNavigate, onClose }: Readonly<{
  open: boolean;
  examId: string;
  initialQuestionId?: string;
  availableQuestions?: Pick<Question,"id"|"question_no"|"question_type">[];
  canManage: boolean;
  canReadReview?: boolean;
  onNavigate?: (path: string) => void;
  onClose: () => void;
}>) {
  const { message } = App.useApp();
  const [questions, setQuestions] = useState<Pick<Question,"id"|"question_no"|"question_type">[]>([]);
  const [sampleMaterials,setSampleMaterials]=useState<Record<string,AnswerGroupMemberContext>>({});
  const [questionId, setQuestionId] = useState(initialQuestionId);
  const [groups, setGroups] = useState<AnswerGroup[]>([]);
  const [references, setReferences] = useState<TeacherReferenceCase[]>([]);
  const [metrics, setMetrics] = useState<AnswerGroupMetrics>();
  const [selectedGroupId, setSelectedGroupId] = useState("");
  const [loading, setLoading] = useState(false);
  const [acting, setActing] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [score, setScore] = useState<number | null>(null);
  const [rubricPoints, setRubricPoints] = useState<string[]>([]);
  const [rollbackOpen, setRollbackOpen] = useState(false);
  const [rollbackReason, setRollbackReason] = useState("");
  const loadRequestRef = useRef(0);
  const questionEpochRef = useRef(0);
  const rollbackDialogRef = useRef(0);
  const contextRef = useRef({ examId, questionId, open, groups });
  contextRef.current = { examId, questionId, open, groups };

  const selectedGroup = useMemo(() => groups.find((group) => group.id === selectedGroupId) ?? groups[0], [groups, selectedGroupId]);
  const currentGroup = selectedGroup?.exam_id === examId && selectedGroup.question_id === questionId ? selectedGroup : undefined;
  const blockReason = confirmBlockReason(currentGroup);
  // 刷新后组 ID 可能不变；只有仍属于当前成员表示的已读材料才能提供候选评分约束。
  const readMaterialFor = (group: AnswerGroup) => Object.values(sampleMaterials).find(value => value.group_id === group.id
    && value.question.id === group.question_id && value.question.exam_id === group.exam_id
    && group.members.some(member => member.segment_id === value.member.segment_id && member.representation_hash === value.member.representation_hash));

  const clearQuestionState = useCallback(() => {
    rollbackDialogRef.current += 1;
    setGroups([]);
    setReferences([]);
    setMetrics(undefined);
    setSelectedGroupId("");
    setSampleMaterials({});
    setScore(null);
    setRubricPoints([]);
    setRollbackOpen(false);
    setRollbackReason("");
    setLoadError("");
    setLoading(false);
    setActing(false);
  }, []);

  const beginQuestionContext = useCallback((nextQuestionId: string) => {
    questionEpochRef.current += 1;
    contextRef.current = { examId, questionId: nextQuestionId, open, groups: [] };
    loadRequestRef.current += 1;
    clearQuestionState();
    setQuestionId(nextQuestionId);
  }, [examId, open, clearQuestionState]);

  const load = useCallback(async (nextQuestionId: string) => {
    const requestId = ++loadRequestRef.current;
    const targetExamId = examId;
    const targetQuestionId = nextQuestionId;
    const targetEpoch = questionEpochRef.current;
    // 切题或切考试后旧请求仍可能返回；序号、上下文和题目代次都匹配时才能更新面板。
    const isCurrentRequest = () => requestId === loadRequestRef.current
      && targetEpoch === questionEpochRef.current
      && contextRef.current.open
      && contextRef.current.examId === targetExamId
      && contextRef.current.questionId === targetQuestionId;
    if (!examId || !nextQuestionId) {
      if (isCurrentRequest()) clearQuestionState();
      return;
    }
    setLoading(true);
    setLoadError("");
    try {
      const [groupResponse, metricResponse] = await Promise.all([
        listAnswerGroups(examId, nextQuestionId),
        getAnswerGroupMetrics(examId, nextQuestionId)
      ]);
      if (!isCurrentRequest()) return;
      setGroups(groupResponse.answer_groups);
      setReferences(groupResponse.teacher_reference_cases);
      setMetrics(metricResponse.metrics);
      setSelectedGroupId((current) => groupResponse.answer_groups.some((group) => group.id === current) ? current : groupResponse.answer_groups[0]?.id ?? "");
    } catch (error) {
      if (!isCurrentRequest()) return;
      const text = errorMessage(error);
      setLoadError(text);
      setGroups([]); setReferences([]); setMetrics(undefined);
    } finally {
      if (isCurrentRequest()) setLoading(false);
    }
  }, [examId, clearQuestionState]);

  useEffect(() => {
    if (!open || !examId) {
      // 回滚对话框独立挂载在 Drawer 外；关闭面板时也要清理它和失效请求留下的忙碌状态。
      clearQuestionState();
      setQuestions([]);
      return;
    }
    let active = true;
    setQuestions([]);
    beginQuestionContext(initialQuestionId);
    (availableQuestions ? Promise.resolve({questions:availableQuestions}) : listQuestions(examId))
      .then((response) => {
        if (!active) return;
        setQuestions(response.questions);
        const next = initialQuestionId || response.questions[0]?.id || "";
        beginQuestionContext(next);
        void load(next);
      })
      .catch((error) => active && setLoadError(errorMessage(error)));
    return () => {
      active = false;
      questionEpochRef.current += 1;
      loadRequestRef.current += 1;
    };
  }, [examId, initialQuestionId, open, availableQuestions, beginQuestionContext, clearQuestionState, load]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 切组或决策修订必须重新载入候选；即使两组分数相同也不能沿用上组的本地选择。
  useEffect(() => {
    const decisionScore = selectedGroup?.decision?.score_candidate.score;
    setScore(typeof decisionScore === "number" ? decisionScore : null);
    const selection = selectedGroup?.decision?.rubric_selection.rubric_points;
    setRubricPoints(Array.isArray(selection) ? selection.filter((item): item is string => typeof item === "string") : []);
  }, [selectedGroup?.id, selectedGroup?.decision?.revision, selectedGroup?.decision?.score_candidate.score, selectedGroup?.decision?.rubric_selection.rubric_points]);

  const run = async (action: () => Promise<unknown>, success: string, targetQuestionId = questionId, targetExamId = examId, targetEpoch = questionEpochRef.current) => {
    // 用户可能在写操作返回前切题；旧操作不能刷新新题、显示成功或覆盖新题的忙碌状态。
    const isCurrentOperation = () => contextRef.current.examId === targetExamId
      && contextRef.current.questionId === targetQuestionId
      && contextRef.current.open
      && questionEpochRef.current === targetEpoch;
    if (!isCurrentOperation()) return false;
    setActing(true);
    try {
      await action();
      if (!isCurrentOperation()) return false;
      message.success(success);
      await load(targetQuestionId);
      return isCurrentOperation();
    } catch (error) {
      if (!isCurrentOperation()) return false;
      message.error(errorMessage(error));
      return false;
    } finally {
      if (isCurrentOperation()) setActing(false);
    }
  };

  const build = () => {
    const targetEpoch = questionEpochRef.current;
    return run(() => buildAnswerGroups(examId, questionId), "答案分组候选已生成", questionId, examId, targetEpoch);
  };
  const reviewSample = (segmentId: string, outcome: "accepted" | "rejected") => {
    const group = currentGroup;
    const targetQuestionId = questionId;
    const targetEpoch = questionEpochRef.current;
    return run(
      () => {
        const member = group?.members.find(value => value.segment_id === segmentId);
        const material = group ? sampleMaterials[`${group.id}:${segmentId}`] : undefined;
        // 抽检只能使用已核对且内容哈希未变的样本材料。
        if (!group || !member || material?.member.representation_hash !== member.representation_hash) {
          throw new Error("请先查看该样本的真实答题材料");
        }
        return reviewAnswerGroupSample(group.id, segmentId, outcome);
      },
      outcome === "accepted" ? "样本已接受" : "样本已拒绝，请按样本下方提示继续人工复核",
      targetQuestionId,
      examId,
      targetEpoch
    );
  };
  const saveDecision = () => {
    if (!currentGroup || score === null || rubricPoints.length === 0) {
      message.warning("请填写候选分数并至少选择一个评分要点");
      return;
    }
    const targetQuestionId = questionId;
    const targetEpoch = questionEpochRef.current;
    const material = readMaterialFor(currentGroup);
    const allowedPoints = material?.question.rubric?.points.map(point => point.id) ?? [];
    if (!material || score < 0 || score > material.question.score || rubricPoints.some(point => !allowedPoints.includes(point))) {
      message.warning("请先核对真实样本，并按本题满分与冻结采分点填写候选");
      return;
    }
    // 携带当前候选修订号；服务端拒绝冲突后由 errorMessage 提示刷新。
    void run(() => saveAnswerGroupDecision(currentGroup.id, {
      score_candidate: { score },
      rubric_selection: { rubric_points: rubricPoints },
      expected_revision: currentGroup.decision?.revision ?? 0
    }), "组评分候选已保存", targetQuestionId, examId, targetEpoch);
  };
  const confirm = () => currentGroup?.decision && run(
    () => confirmAnswerGroup(currentGroup.id, currentGroup.decision!.revision),
    "已为组成员生成可追溯评分候选；最终成绩未被修改",
    questionId,
    examId,
    questionEpochRef.current
  );
  const rollback = () => {
    const rollbackReference = currentGroup?.decision?.rollback_reference;
    if (!currentGroup || !rollbackReference || !rollbackReason.trim()) {
      message.warning("请填写回滚原因");
      return;
    }
    const targetQuestionId = questionId;
    const targetExamId = examId;
    const targetEpoch = questionEpochRef.current;
    const targetDialog = rollbackDialogRef.current;
    void run(() => rollbackAnswerGroup(currentGroup.id, rollbackReference, rollbackReason.trim()), "组候选已回滚", targetQuestionId, targetExamId, targetEpoch)
      .then((completed) => {
        // 同题也可能关闭后重新填写；只有当前题、当前对话框的成功操作才能清理回滚原因。
        if (!completed || !contextRef.current.open || contextRef.current.examId !== targetExamId
          || contextRef.current.questionId !== targetQuestionId || questionEpochRef.current !== targetEpoch
          || rollbackDialogRef.current !== targetDialog) return;
        setRollbackOpen(false);
        setRollbackReason("");
      });
  };

  const renderSelectedGroup = (group: AnswerGroup) => {
    const targetEpoch = questionEpochRef.current;
    const readMaterial = readMaterialFor(group);
    // 已确认或已回滚的组不再显示抽检和候选编辑入口；这里只控制界面展示。
    const canReview = canManage && group.status !== "confirmed" && group.status !== "rolled_back";
    return (
      <section>
        <Space direction="vertical" style={{ width: "100%" }} size="middle">
          <div>
            <Space style={{ width: "100%", justifyContent: "space-between" }}>
              <strong>抽检进度</strong><span>{group.reviewed_sample_count} / 至少 {group.minimum_sample}</span>
            </Space>
            <Progress percent={group.minimum_sample ? Math.min(100, Math.round(group.reviewed_sample_count / group.minimum_sample * 100)) : 100} status={group.can_confirm ? "success" : "active"} />
          </div>
          <List
            size="small"
            bordered
            header="代表 / 边界 / 异常样本"
            dataSource={[...group.members].sort((left, right) => Number(right.representative) - Number(left.representative) || Number(right.boundary) - Number(left.boundary) || Number(right.outlier) - Number(left.outlier))}
            renderItem={(member) => {
              const sampleStatusLabel = sampleReviewLabel(member.sample_status);
              return (
                <List.Item actions={canReview ? [
                  <Button key="accept" size="small" disabled={acting||sampleMaterials[`${group.id}:${member.segment_id}`]?.member.representation_hash!==member.representation_hash} type={member.sample_status === "accepted" ? "primary" : "default"} onClick={() => void reviewSample(member.segment_id, "accepted")}>接受</Button>,
                  <Button key="reject" size="small" disabled={acting||sampleMaterials[`${group.id}:${member.segment_id}`]?.member.representation_hash!==member.representation_hash} danger type={member.sample_status === "rejected" ? "primary" : "default"} onClick={() => void reviewSample(member.segment_id, "rejected")}>拒绝</Button>
                ] : undefined}>
                  <List.Item.Meta
                    title={<Space wrap><span>答卷 {member.submission_id.slice(0, 10)}</span>{sampleRoleLabels(member).map((label) => <Tag key={label} color={sampleRoleColor(label)}>{label}</Tag>)}</Space>}
                    description={`相似度 ${(member.similarity * 100).toFixed(1)}% · 异常度 ${(member.outlier_score * 100).toFixed(1)}% · ${sampleStatusLabel}`}
                  />
                  <AnswerGroupSampleEvidence key={`${group.id}:${member.segment_id}:${member.representation_hash}`} groupId={group.id} segmentId={member.segment_id} hash={member.representation_hash} onRead={(value) => {
                    // 图片显示回调也可能迟到；切题并再次返回同一题后，旧代次材料仍不能登记为已读。
                    if (!contextRef.current.open || contextRef.current.examId !== group.exam_id
                      || contextRef.current.questionId !== group.question_id || questionEpochRef.current !== targetEpoch) return;
                    if (value.group_id !== group.id || value.member.segment_id !== member.segment_id
                      || value.member.representation_hash !== member.representation_hash
                      || value.question.id !== group.question_id || value.question.exam_id !== group.exam_id) return;
                    const currentMember = contextRef.current.groups.find(current => current.id === group.id)?.members.find(current => current.segment_id === member.segment_id);
                    if (currentMember?.representation_hash !== value.member.representation_hash) return;
                    setSampleMaterials(current => ({ ...current, [`${group.id}:${member.segment_id}`]: value }));
                  }}/>
                  {member.sample_status === "rejected" ? <AnswerGroupManualHandoff material={sampleMaterials[`${group.id}:${member.segment_id}`]} canReadReview={canReadReview} onNavigate={onNavigate} /> : null}
                </List.Item>
              );
            }}
          />
          {canReview ? (
            <section>
              <Divider orientation="left">组评分候选</Divider>
              <Space wrap>
                <InputNumber min={0} value={score} onChange={(value) => setScore(value === null ? null : Number(value))} placeholder="候选分数" />
                <Select mode="multiple" style={{ minWidth: 320 }} value={rubricPoints} onChange={setRubricPoints} options={(readMaterial?.question.rubric?.points??[]).map(point=>({value:point.id,label:`${point.description} · ${point.score}分`}))} placeholder="先读取样本材料，再选择实际评分点" />
                <Button loading={acting} disabled={!readMaterial} onClick={saveDecision}>保存候选</Button>
                <Tooltip title={blockReason ?? "抽检策略已满足"}>
                  <span>
                    <Popconfirm title="确认生成整组评分候选？" description="系统只写入可追溯候选，不会写最终成绩。" onConfirm={() => void confirm()}>
                      <Button type="primary" disabled={Boolean(blockReason)} loading={acting}>确认组候选</Button>
                    </Popconfirm>
                  </span>
                </Tooltip>
              </Space>
              {blockReason ? <Alert type="warning" showIcon message="暂不能确认" description={blockReason} style={{ marginTop: 12 }} /> : null}
            </section>
          ) : null}
          {group.status === "confirmed" && group.decision?.rollback_reference && canManage ? (
            <Alert type="success" showIcon message="已生成自动化评分候选" description={<Space><span>候选仍需下游评分流程确认，不是最终成绩。</span><Button danger size="small" onClick={() => { rollbackDialogRef.current += 1; setRollbackOpen(true); }}>回滚候选</Button></Space>} />
          ) : null}
        </Space>
      </section>
    );
  };

  const questionNumber = questions.find((question) => question.id === questionId)?.question_no;
  const questionLabel = questionNumber ? `第${questionNumber}题` : "当前题目";

  return (
    <>
      <Drawer title="相似答案分组" width={1040} open={open} onClose={onClose} extra={<Button loading={loading} onClick={() => void load(questionId)}>刷新</Button>}>
        <Alert
          type="info"
          showIcon
          message="分组只生成评分候选，不会直接修改最终成绩"
          description="仅对可可靠文本化的精确文本与短构造题开放；代表、边界与异常样本必须按抽检策略由教师确认。"
          style={{ marginBottom: 16 }}
        />
        <Space wrap style={{ marginBottom: 16 }}>
          <Select
            style={{ minWidth: 300 }}
            value={questionId || undefined}
            placeholder="选择题目"
            options={questions.map((question) => ({ value: question.id, label: `${question.question_no || "题号待核对"} · ${questionTypeOptions.find(option => option.value === question.question_type)?.label ?? "题型待核对"}` }))}
            onChange={(value) => { beginQuestionContext(value); void load(value); }}
          />
          {canManage ? <Button type="primary" disabled={!questionId} loading={acting} onClick={() => void build()}>构建分组</Button> : null}
        </Space>
        {loadError ? <Alert type="error" showIcon message="答案分组加载失败" description={loadError} style={{ marginBottom: 16 }} /> : null}
        {metrics ? (
          <Descriptions size="small" bordered column={5} style={{ marginBottom: 16 }}>
            <Descriptions.Item label="组数">{metrics.group_count}</Descriptions.Item>
            <Descriptions.Item label="组内同质性">{(metrics.group_homogeneity * 100).toFixed(1)}%</Descriptions.Item>
            <Descriptions.Item label="批量回滚率">{(metrics.batch_override_rate * 100).toFixed(1)}%</Descriptions.Item>
            <Descriptions.Item label="节省人工动作">{metrics.human_actions_saved}</Descriptions.Item>
            <Descriptions.Item label="事后审计误差">{metrics.post_audit_error_rate === undefined ? "尚未采集" : `${(metrics.post_audit_error_rate * 100).toFixed(1)}%`}</Descriptions.Item>
          </Descriptions>
        ) : null}

        <Spin spinning={loading}>
          {!groups.length ? <Empty description={questionId ? "暂无答案组，可构建分组候选" : "请先选择题目"} /> : (
            <div style={{ display: "grid", gridTemplateColumns: "280px minmax(0, 1fr)", gap: 16 }}>
              <List
                bordered
                dataSource={groups}
                renderItem={(group) => (
                  <List.Item className="answer-group-choice-item">
                    <button type="button" className="answer-group-choice" aria-pressed={group.id === selectedGroup?.id} onClick={() => setSelectedGroupId(group.id)}>
                      <span className="answer-group-choice-title"><span>{group.member_count} 份答案</span><Tag>{statusLabels[group.status] ?? "未知状态"}</Tag></span>
                      <span className="answer-group-choice-description">同质性 {(group.homogeneity * 100).toFixed(1)}% · 抽检 {group.reviewed_sample_count}/{group.minimum_sample}</span>
                    </button>
                  </List.Item>
                )}
              />
              {currentGroup ? renderSelectedGroup(currentGroup) : null}
            </div>
          )}
        </Spin>

        <Divider orientation="left">教师参考案例（仅已批准标准卷）</Divider>
        <List
          size="small"
          bordered
          dataSource={references}
          locale={{ emptyText: "当前题目暂无已批准标准卷参考案例" }}
          renderItem={(item) => <List.Item><List.Item.Meta title={`${item.reference_score} / ${item.max_score} 分 · 标准卷 v${item.version}`} description={<Space direction="vertical" size={2}><span>{item.explanation}</span>{item.error_tags.length ? <span>{item.error_tags.map((tag) => <Tag key={tag}>{tag}</Tag>)}</span> : null}</Space>} /></List.Item>}
        />
      </Drawer>
      <Modal title={`回滚${questionLabel}的 ${currentGroup?.member_count ?? 0} 份候选`} open={rollbackOpen && Boolean(currentGroup)} confirmLoading={acting} okButtonProps={{ danger: true }} onOk={rollback} onCancel={() => { rollbackDialogRef.current += 1; setRollbackOpen(false); }}>
        <Alert type="warning" showIcon message={`当前为${questionLabel}，共 ${currentGroup?.member_count ?? 0} 份答案。回滚只撤销本次组候选，不会覆盖已形成的最终成绩事实。`} style={{ marginBottom: 12 }} />
        <Input.TextArea rows={3} value={rollbackReason} onChange={(event) => setRollbackReason(event.target.value)} placeholder="填写回滚原因，便于审计追溯" />
      </Modal>
    </>
  );
}
