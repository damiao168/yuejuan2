import { useEffect, useRef, useState } from "react";
import { App } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import { newRubricPointId, rubricPointIdentityError } from "../rubricPointIdentity";
import { newRubricAssistantDraft, type RubricAssistantDraft } from "../rubricAssistantDraft";
import {
  createRubric,
  type Question,
  type RubricEvidenceRequirement,
  type RubricPoint,
  type RubricPayload
} from "../../../api/papers";

function jsonText(value: unknown, fallback: string) {
  return value === undefined || value === null ? fallback : JSON.stringify(value, null, 2);
}

function parseArrayJSON(value: string, label: string): unknown[] {
  try {
    const parsed = JSON.parse(value || "[]") as unknown;
    if (!Array.isArray(parsed)) throw new Error(`${label}必须包含逐项内容`);
    return parsed;
  } catch {
    throw new Error(`${label}暂时无法读取，请核对历史评分数据后再保存`);
  }
}

export function rubricPointTotal(points: RubricPoint[]) {
  return points.reduce((sum, point) => sum + (Number(point.score) || 0), 0);
}

export function useRubricEditor({
  selectedQuestion,
  selectedExamId,
  preserveUnsaved = false,
  loadConfig,
  onChanged
}: {
  selectedQuestion: Question | null;
  selectedExamId: string;
  preserveUnsaved?: boolean;
  loadConfig: (examId: string, options?: { silent?: boolean }) => Promise<void>;
  onChanged?: () => void;
}) {
  const { message } = App.useApp();
  const [savingRubric, setSavingRubric] = useState(false);
  const [rubricDirty, setRubricDirty] = useState(false);
  const [assistantDirty, setAssistantDirty] = useState(false);
  const [assistantResetVersion, setAssistantResetVersion] = useState(0);
  const assistantDraftStore = useRef<RubricAssistantDraft | null>(null);
  const [rubricStatus, setRubricStatus] = useState("draft");
  const [rubricPoints, setRubricPoints] = useState<RubricPoint[]>([]);
  const [deductionsJson, setDeductionsJson] = useState("");
  const [examplesJson, setExamplesJson] = useState("");
  const loadedQuestionKey = useRef<string | null>(null);
  const saveInFlight = useRef(false);

  useEffect(() => {
    // 同一编号跨学校或考试也属于不同来源，不能用 preserveUnsaved 携带上一范围的细则。
    const questionKey = selectedQuestion ? JSON.stringify([selectedQuestion.tenant_id, selectedQuestion.exam_id, selectedQuestion.id]) : "create";
    if ((rubricDirty || assistantDirty || preserveUnsaved) && loadedQuestionKey.current === questionKey) return;
    loadedQuestionKey.current = questionKey;
    if (assistantDraftStore.current && selectedQuestion && assistantDraftStore.current.scope !== JSON.stringify(selectedQuestion)) {
      assistantDraftStore.current = newRubricAssistantDraft(selectedQuestion, assistantDraftStore.current);
    }
    setRubricDirty(false);
    setAssistantDirty(false);
    if (selectedQuestion) {
      setRubricStatus(selectedQuestion.rubric?.status ?? "draft");
      setRubricPoints(selectedQuestion.rubric?.points ?? []);
      setDeductionsJson(jsonText(selectedQuestion.rubric?.deductions, ""));
      setExamplesJson(jsonText(selectedQuestion.rubric?.examples, ""));
      return;
    }
    setRubricStatus("draft");
    setRubricPoints([{ id: "p1", description: "", score: 10, required: true }]);
    setDeductionsJson("");
    setExamplesJson("");
  }, [rubricDirty, assistantDirty, preserveUnsaved, selectedQuestion]);

  const discardRubricDraft = () => {
    assistantDraftStore.current = selectedQuestion ? newRubricAssistantDraft(selectedQuestion, assistantDraftStore.current) : null;
    setRubricDirty(false);
    setAssistantDirty(false);
    setAssistantResetVersion((version) => version + 1);
  };

  const replaceRubricPoints = (points: RubricPoint[]) => {
    const identityError = rubricPointIdentityError(points);
    if (identityError) throw new Error(identityError);
    if (!points.length || points.some((point) => !point.description.trim() || !Number.isFinite(point.score) || point.score < 0)) {
      throw new Error("请核对采分点内容和分值");
    }
    if (!selectedQuestion || Math.abs(rubricPointTotal(points) - selectedQuestion.score) > 0.0001) {
      throw new Error("采分点合计必须等于本题满分");
    }
    // 分段或 AI 建议只是新草稿；旧扣分规则与示例可能引用原采分点，不能跟随替换继续生效。
    setRubricPoints(structuredClone(points));
    assistantDraftStore.current = newRubricAssistantDraft(selectedQuestion, assistantDraftStore.current);
    setDeductionsJson("[]");
    setExamplesJson("[]");
    setRubricStatus("draft");
    setRubricDirty(true);
    setAssistantDirty(false);
    setAssistantResetVersion((version) => version + 1);
  };

  const updatePoint = (index: number, patch: Partial<RubricPoint>) => {
    setRubricDirty(true);
    setRubricPoints((current) => current.map((point, currentIndex) => currentIndex === index ? { ...point, ...patch } : point));
  };
  const addEvidenceRequirement = (pointIndex: number) => {
    updatePoint(pointIndex, {
      evidence_requirements: [...(rubricPoints[pointIndex]?.evidence_requirements ?? []), { type: "valid_transformation" }]
    });
  };
  const updateEvidenceRequirement = (
    pointIndex: number,
    requirementIndex: number,
    patch: Partial<RubricEvidenceRequirement>
  ) => {
    const requirements = rubricPoints[pointIndex]?.evidence_requirements ?? [];
    updatePoint(pointIndex, {
      evidence_requirements: requirements.map((requirement, index) => index === requirementIndex ? { ...requirement, ...patch } : requirement)
    });
  };
  const removeEvidenceRequirement = (pointIndex: number, requirementIndex: number) => {
    const requirements = rubricPoints[pointIndex]?.evidence_requirements ?? [];
    updatePoint(pointIndex, { evidence_requirements: requirements.filter((_, index) => index !== requirementIndex) });
  };
  const removePoint = (index: number) => {
    setRubricDirty(true);
    setRubricPoints((current) => current.filter((_, currentIndex) => currentIndex !== index));
  };
  const addPoint = () => {
    setRubricDirty(true);
    setRubricPoints((current) => [...current, { id: newRubricPointId(), description: "", score: 0, required: false }]);
  };

  const prepareRubricSave = (score = selectedQuestion?.score): RubricPayload => {
    // 区间配分和模型预览尚未应用时，保存旧采分点会让教师误以为预览已提交。
    if (assistantDirty) throw new Error("请先应用分值划分草稿，或放弃本区修改，再保存评分细则");
    const identityError = rubricPointIdentityError(rubricPoints);
    if (identityError) throw new Error(identityError);
    const total = rubricPointTotal(rubricPoints);
    if (!score || !Number.isFinite(score) || Math.abs(total - score) > 0.0001) throw new Error("评分细则采分点总分必须等于本次填写的题目分值");
    if (rubricPoints.some(point => !Number.isFinite(Number(point.score)) || Number(point.score) < 0)) throw new Error("采分点分值必须为有限的非负数");
    return { status: rubricStatus, max_score: score, points: structuredClone(rubricPoints), deductions: parseArrayJSON(deductionsJson, "扣分点"), examples: parseArrayJSON(examplesJson, "样例答案") };
  };

  const persistRubricSave = async (questionId: string, payload: RubricPayload, options: { refresh?: boolean; notify?: boolean; onAccepted?: (result: Awaited<ReturnType<typeof createRubric>>) => void } = {}) => {
    // 接口前再次检查传入快照；调用方不能绕过编辑器校验提交历史重复 ID。
    const identityError = rubricPointIdentityError(payload.points);
    if (identityError) throw new Error(identityError);
    if (saveInFlight.current) return false;
    saveInFlight.current = true; setSavingRubric(true);
    try {
      // 使用整体校验时的满分快照；题目刚更新时闭包中的旧满分不能写入新细则版本。
      const result = await createRubric(questionId, payload);
      // 接受回执和已保存快照先交给页面；随后读取失败不能抹掉写入已经成功的事实。
      options.onAccepted?.(result);
      setRubricDirty(false);
      if (options.notify !== false) message.success(payload.status === "locked" ? "评分细则已锁定" : "评分细则新版本已提交");
      if (options.refresh !== false) { await loadConfig(selectedExamId); onChanged?.(); }
      return true;
    } finally {
      saveInFlight.current = false; setSavingRubric(false);
    }
  };
  const saveRubric = async () => {
    try {
      if (!selectedQuestion || !selectedExamId) throw new Error("请先选择已保存的题目");
      return await persistRubricSave(selectedQuestion.id, prepareRubricSave());
    } catch (error) { message.error(getUserErrorMessage(error, "评分细则暂不可保存")); return false; }
  };

  const pointTotal = rubricPointTotal(rubricPoints);
  return {
    savingRubric, rubricDirty: rubricDirty || assistantDirty, assistantDirty, assistantResetVersion, assistantDraftStore, setAssistantDirty, replaceRubricPoints, discardRubricDraft, rubricStatus, setRubricStatus: (value: string) => { setRubricDirty(true); setRubricStatus(value); }, rubricPoints, deductionsJson,
    setDeductionsJson: (value: string) => { setRubricDirty(true); setDeductionsJson(value); }, examplesJson, setExamplesJson: (value: string) => { setRubricDirty(true); setExamplesJson(value); }, pointTotal,
    scoreMismatch: Boolean(selectedQuestion && Math.abs(pointTotal - selectedQuestion.score) > 0.0001),
    updatePoint, addEvidenceRequirement, updateEvidenceRequirement,
    removeEvidenceRequirement, removePoint, addPoint, saveRubric, prepareRubricSave, persistRubricSave
  };
}
