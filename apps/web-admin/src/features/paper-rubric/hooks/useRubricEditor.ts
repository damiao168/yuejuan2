import { useEffect, useRef, useState } from "react";
import { App } from "antd";
import { getUserErrorMessage } from "../../../api/client";
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
  const [rubricStatus, setRubricStatus] = useState("draft");
  const [rubricPoints, setRubricPoints] = useState<RubricPoint[]>([]);
  const [deductionsJson, setDeductionsJson] = useState("");
  const [examplesJson, setExamplesJson] = useState("");
  const loadedQuestionKey = useRef<string | null>(null);
  const saveInFlight = useRef(false);

  useEffect(() => {
    const questionKey = selectedQuestion?.id ?? "create";
    if ((rubricDirty || preserveUnsaved) && loadedQuestionKey.current === questionKey) return;
    loadedQuestionKey.current = questionKey;
    setRubricDirty(false);
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
  }, [rubricDirty, preserveUnsaved, selectedQuestion]);

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
    setRubricPoints((current) => [...current, { id: `p${current.length + 1}`, description: "", score: 0, required: false }]);
  };

  const prepareRubricSave = (score = selectedQuestion?.score): RubricPayload => {
    const total = rubricPointTotal(rubricPoints);
    if (!score || !Number.isFinite(score) || Math.abs(total - score) > 0.0001) throw new Error("评分细则采分点总分必须等于本次填写的题目分值");
    if (rubricPoints.some(point => !Number.isFinite(Number(point.score)) || Number(point.score) < 0)) throw new Error("采分点分值必须为有限的非负数");
    return { status: rubricStatus, max_score: score, points: structuredClone(rubricPoints), deductions: parseArrayJSON(deductionsJson, "扣分点"), examples: parseArrayJSON(examplesJson, "样例答案") };
  };

  const persistRubricSave = async (questionId: string, payload: RubricPayload, options: { refresh?: boolean; notify?: boolean; onAccepted?: (result: Awaited<ReturnType<typeof createRubric>>) => void } = {}) => {
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
    savingRubric, rubricDirty, discardRubricDraft: () => setRubricDirty(false), rubricStatus, setRubricStatus: (value: string) => { setRubricDirty(true); setRubricStatus(value); }, rubricPoints, deductionsJson,
    setDeductionsJson: (value: string) => { setRubricDirty(true); setDeductionsJson(value); }, examplesJson, setExamplesJson: (value: string) => { setRubricDirty(true); setExamplesJson(value); }, pointTotal,
    scoreMismatch: Boolean(selectedQuestion && Math.abs(pointTotal - selectedQuestion.score) > 0.0001),
    updatePoint, addEvidenceRequirement, updateEvidenceRequirement,
    removeEvidenceRequirement, removePoint, addPoint, saveRubric, prepareRubricSave, persistRubricSave
  };
}
