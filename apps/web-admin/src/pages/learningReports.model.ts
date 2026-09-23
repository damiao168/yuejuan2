import type { ClassReport, ErrorClue, QuestionAnalysis, ReportMetric } from "../api/reports";

export interface ErrorRow {
  key: string;
  question_no: string;
  source: string;
  text: string;
  count: number;
  score_rate?: number;
}

interface KnowledgeRow {
  knowledge_point: string;
  mastery_rate: number;
  question_count: number;
  score: number;
  max_score: number;
}

export const questionTypeLabels: Record<string, string> = {
  single_choice: "单选题",
  multiple_choice: "多选题",
  true_false: "判断题",
  fill_blank: "填空题",
  numeric: "数值题",
  formula: "公式题",
  short_answer: "简答题",
  calculation: "计算题",
  essay: "作文题",
  discussion: "论述题",
  coding: "编程题",
  subjective: "主观题",
  objective: "客观题"
};

const errorSourceLabels: Record<string, string> = {
  ai: "AI 识别",
  ocr: "卷面识别",
  teacher: "教师标注"
};

export function formatScore(value?: number | null) {
  if (value === undefined || value === null || !Number.isFinite(value)) {
    return "-";
  }
  return Number(value.toFixed(2)).toString();
}

export function formatPercent(value?: number | null) {
  if (value === undefined || value === null || !Number.isFinite(value)) {
    return "-";
  }
  return `${Number((value * 100).toFixed(1)).toString()}%`;
}

export function percentValue(value?: number | null) {
  if (value === undefined || value === null || !Number.isFinite(value)) {
    return 0;
  }
  return Number((value * 100).toFixed(1));
}

export function formatMetric(metric?: ReportMetric, mode: "percent" | "score" = "percent") {
  if (!metric?.available) {
    return "不可用";
  }
  return mode === "percent" ? formatPercent(metric.value) : formatScore(metric.value);
}

export function metricDetail(metric?: ReportMetric) {
  if (!metric?.available) {
    return "统计样本不足";
  }
  if (metric.denominator) {
    return `${metric.numerator ?? 0} / ${metric.denominator}`;
  }
  return "";
}

export function emptyReason(reason?: string) {
  if (reason === "no_published_grades") {
    return "该考试的成绩尚未发布。请先在『成绩发布』页发布成绩，报告会自动生成。";
  }
  return "当前考试暂无报告数据。";
}

export function aggregateKnowledge(classes: ClassReport[]): KnowledgeRow[] {
  const grouped = new Map<string, KnowledgeRow>();
  for (const classReport of classes) {
    for (const item of classReport.weak_knowledge_points ?? []) {
      const current = grouped.get(item.knowledge_point) ?? {
        knowledge_point: item.knowledge_point,
        mastery_rate: 0,
        question_count: 0,
        score: 0,
        max_score: 0
      };
      current.score += item.score;
      current.max_score += item.max_score;
      current.question_count += item.question_count;
      current.mastery_rate = current.max_score > 0 ? current.score / current.max_score : item.mastery_rate;
      grouped.set(item.knowledge_point, current);
    }
  }
  return Array.from(grouped.values())
    .sort((a, b) => a.mastery_rate - b.mastery_rate)
    .slice(0, 10);
}

export function flattenErrors(questions: QuestionAnalysis[], classes: ClassReport[]): ErrorRow[] {
  const fromQuestions = questions.flatMap((question) =>
    (question.frequent_errors ?? []).map((item: ErrorClue, index) => ({
      key: `${question.question_id}-${item.text}-${index}`,
      question_no: item.question_no || question.question_no,
      source: errorSourceLabels[item.source] ?? "题目分析",
      text: item.text,
      count: item.count ?? 1,
      score_rate: question.score_rate
    }))
  );
  if (fromQuestions.length > 0) {
    return fromQuestions.sort((a, b) => b.count - a.count).slice(0, 8);
  }
  return classes
    .flatMap((classReport) =>
      (classReport.frequent_wrong_questions ?? []).map((item, index) => ({
        key: `${classReport.class_id}-${item.question_id}-${index}`,
        question_no: item.question_no,
        source: classReport.class_name,
        text: `班级高频错题，得分率 ${formatPercent(item.score_rate)}`,
        count: item.wrong_count,
        score_rate: item.score_rate
      }))
    )
    .sort((a, b) => b.count - a.count)
    .slice(0, 8);
}
