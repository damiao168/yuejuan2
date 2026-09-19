import { useEffect, useState } from "react";
import { App } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import {
  createRubric,
  type Question,
  type RubricEvidenceRequirement,
  type RubricPoint
} from "../../../api/papers";

function jsonText(value: unknown, fallback: string) {
  return value === undefined || value === null ? fallback : JSON.stringify(value, null, 2);
}

function parseArrayJSON(value: string, label: string): unknown[] {
  try {
    const parsed = JSON.parse(value || "[]") as unknown;
    if (!Array.isArray(parsed)) throw new Error(`${label}必须是 JSON 数组`);
    return parsed;
  } catch {
    throw new Error(`${label}解析失败，请检查 JSON 格式`);
  }
}

export function rubricPointTotal(points: RubricPoint[]) {
  return points.reduce((sum, point) => sum + (Number(point.score) || 0), 0);
}

export function useRubricEditor({
  selectedQuestion,
  selectedExamId,
  loadConfig,
  onChanged
}: {
  selectedQuestion: Question | null;
  selectedExamId: string;
  loadConfig: (examId: string, options?: { silent?: boolean }) => Promise<void>;
  onChanged?: () => void;
}) {
  const { message } = App.useApp();
  const [savingRubric, setSavingRubric] = useState(false);
  const [rubricStatus, setRubricStatus] = useState("draft");
  const [rubricPoints, setRubricPoints] = useState<RubricPoint[]>([]);
  const [deductionsJson, setDeductionsJson] = useState("");
  const [examplesJson, setExamplesJson] = useState("");

  useEffect(() => {
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
  }, [selectedQuestion]);

  const updatePoint = (index: number, patch: Partial<RubricPoint>) => {
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
    setRubricPoints((current) => current.filter((_, currentIndex) => currentIndex !== index));
  };
  const addPoint = () => {
    setRubricPoints((current) => [...current, { id: `p${current.length + 1}`, description: "", score: 0, required: false }]);
  };

  const saveRubric = async () => {
    if (!selectedQuestion || !selectedExamId) {
      message.error("请先选择已保存的题目");
      return;
    }
    const total = rubricPointTotal(rubricPoints);
    if (Math.abs(total - selectedQuestion.score) > 0.0001) {
      message.error("评分细则采分点总分必须等于题目分值");
      return;
    }
    let deductions: unknown[];
    let examples: unknown[];
    try {
      deductions = parseArrayJSON(deductionsJson, "扣分点");
      examples = parseArrayJSON(examplesJson, "样例答案");
    } catch (error) {
      message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
      return;
    }
    setSavingRubric(true);
    try {
      await createRubric(selectedQuestion.id, {
        status: rubricStatus,
        max_score: selectedQuestion.score,
        points: rubricPoints,
        deductions,
        examples
      });
      message.success(rubricStatus === "locked" ? "评分细则已锁定" : "评分细则新版本已提交");
      await loadConfig(selectedExamId);
      onChanged?.();
    } catch (error) {
      message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
    } finally {
      setSavingRubric(false);
    }
  };

  const pointTotal = rubricPointTotal(rubricPoints);
  return {
    savingRubric, rubricStatus, setRubricStatus, rubricPoints, deductionsJson,
    setDeductionsJson, examplesJson, setExamplesJson, pointTotal,
    scoreMismatch: Boolean(selectedQuestion && Math.abs(pointTotal - selectedQuestion.score) > 0.0001),
    updatePoint, addEvidenceRequirement, updateEvidenceRequirement,
    removeEvidenceRequirement, removePoint, addPoint, saveRubric
  };
}
