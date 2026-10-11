// 公式识别和证据界面只向明确选择的学科开放。
// 没有对应学科的基准证据和发布决策时，不应扩展此列表：
// 语言及人文学科答案必须继续使用通用 OCR 路径。
const subjectAliases: Record<string, "mathematics" | "physics" | "chemistry"> = {
  mathematics: "mathematics",
  math: "mathematics",
  "数学": "mathematics",
  physics: "physics",
  "物理": "physics",
  chemistry: "chemistry",
  "化学": "chemistry"
};

export function isFormulaEvidenceSubject(subject: string | undefined): boolean {
  return Boolean(subjectAliases[String(subject ?? "").trim().toLowerCase()]);
}

