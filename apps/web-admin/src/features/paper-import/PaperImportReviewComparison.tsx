import type { PaperImportDraftQuestion } from "../../api/papers";
import { reviewDraftIndex, sameReviewValue } from "./reviewConfirmation";

const valueLabels: Record<string, string> = {
  standard_answer: "标准答案", equivalent_answers: "等价答案", tolerance: "容差",
  raw_text: "解析", steps: "步骤", step_no: "步骤序号", content: "内容",
  max_score: "总分", points: "评分点", description: "条件", score: "分值",
  required: "必需", deductions: "扣分", examples: "示例", student_answer: "学生作答",
  awarded_score: "得分", reason: "原因", status: "状态", locked: "已锁定", draft: "草稿",
  question_no: "题号", question_type: "题型", stem: "题干", answer: "答案", solution: "解析", rubric: "评分标准",
  options: "选项", single_choice: "单选题", multiple_choice: "多选题", true_false: "判断题",
  fill_blank: "填空题", short_answer: "简答题", calculation: "计算题", essay: "作文", discussion: "论述题"
};

function display(value: unknown): string {
  if (value === undefined || value === null || value === "") return "未填写";
  if (typeof value === "boolean") return value ? "是" : "否";
  if (Array.isArray(value)) return value.length ? value.map(display).join("；") : "未填写";
  if (typeof value === "object") return Object.entries(value).filter(([key]) => key !== "id" && key !== "source_refs")
    .map(([key, child]) => `${valueLabels[key] ?? key}：${display(child)}`).join("；") || "未填写";
  if (typeof value === "string") return valueLabels[value] ?? value;
  if (typeof value === "number") return String(value);
  return "不支持的内容类型";
}

const fields = [
  ["question_no", "题号"], ["question_type", "题型"], ["score", "分值"], ["stem", "题干"],
  ["options", "选项"], ["answer_key", "答案"], ["solution", "解析"], ["rubric", "评分标准"],
  ["knowledge_points", "知识点"], ["human_confirmed_fields", "人工确认项"]
] as const;

export function PaperImportReviewComparison({ local, latest }: Readonly<{
  local: PaperImportDraftQuestion[]; latest: PaperImportDraftQuestion[];
}>) {
  const differences = local.flatMap((draft, index) => {
    const saved = latest[reviewDraftIndex(latest, draft, index)];
    return fields.filter(([key]) => !sameReviewValue(draft[key], saved?.[key]))
      .map(([key, label]) => ({ key: `${draft.candidate_id ?? index}:${key}`, question: draft.question_no,
        label, local: display(draft[key]), latest: saved ? display(saved[key]) : "最新结果中已没有此候选" }));
  });
  return <div>
    <p>本页输入尚未写入。请比较你的修改和服务器最新内容；继续核对会保留本地修改，其他题接收最新结果。之后仍需点击原保存按钮。</p>
    {differences.length ? <div style={{ maxHeight: 360, overflow: "auto" }}>
      <table className="dense-data-table" style={{ width: "100%" }}>
        <thead><tr><th>题目 / 内容</th><th>本页输入</th><th>服务器最新内容</th></tr></thead>
        <tbody>{differences.map(row => <tr key={row.key}><th>第 {row.question} 题 · {row.label}</th>
          <td style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{row.local}</td>
          <td style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{row.latest}</td></tr>)}</tbody>
      </table>
    </div> : <p>当前题目内容一致，继续核对后可以重新保存。</p>}
  </div>;
}
