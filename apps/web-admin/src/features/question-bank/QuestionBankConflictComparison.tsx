import type { QuestionBankVersion } from "@edugrade/sdk";
import { questionTypeOptions } from "../../constants/examCatalog";
import "./question-bank-conflict.css";

const fieldLabels: Record<string, string> = {
  description: "说明", score: "分值", points: "得分", deduction: "扣分", reason: "原因",
  content: "内容", step_no: "步骤", answer: "答案", standard_answer: "标准答案",
  text: "文字", value: "答案值", answers: "各空答案", blanks: "各空答案", unit: "单位",
  condition: "适用条件", feedback: "评语", explanation: "解析", comment: "说明",
  type: "核对方式", target: "核对内容", minimum: "至少满足数量", children: "子条件",
  expected_score: "示例得分", student_answer: "学生作答", example: "示例", max_score: "满分",
  required: "必须满足", rule: "规则", trigger: "触发条件", penalty: "扣分", cap: "分值上限",
  max_deduction: "最多扣分", expected: "预期结果", result: "结果"
};
const evidenceLabels: Record<string, string> = {
  concept: "出现指定概念", unit: "单位正确", domain: "定义域或取值条件",
  valid_transformation: "关键变形成立", final_result: "最终结果正确",
  all_of: "全部条件满足", any_of: "任一条件满足", at_least: "至少满足指定数量"
};

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}
function parseDraft(value: unknown) {
  if (typeof value !== "string") return value;
  try { return JSON.parse(value) as unknown; } catch { return value; }
}
function fingerprint(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(fingerprint).join(",")}]`;
  if (value && typeof value === "object") {
    const entries = Object.entries(value).sort(([a], [b]) => a.localeCompare(b)).map(([key, item]) => `${JSON.stringify(key)}:${fingerprint(item)}`).join(",");
    return `{${entries}}`;
  }
  return JSON.stringify(value) ?? "undefined";
}

function comparisonEntries(values: readonly unknown[]) {
  const occurrences = new Map<string, number>();
  return values.map(value => {
    const item = record(value);
    const id = [item.id, item.point_id, item.rubric_point_id].find(candidate => typeof candidate === "string" && candidate.length > 0);
    // 优先沿用业务 ID；历史项没有 ID 时按内容及重复次数区分，重排不把展开状态移给其他内容。
    const identity = JSON.stringify(id === undefined ? ["content", fingerprint(value)] : ["id", id]);
    const occurrence = occurrences.get(identity) ?? 0;
    occurrences.set(identity, occurrence + 1);
    return { key: `${identity}:${occurrence}`, value };
  });
}

function ComparisonValue({ value, field = "", depth = 0 }: Readonly<{ value: unknown; field?: string; depth?: number }>) {
  if (value === undefined || value === null || value === "" || Array.isArray(value) && !value.length) return <span className="muted">未配置</span>;
  if (typeof value === "boolean") return <span>{value ? "是" : "否"}</span>;
  if (typeof value === "string" || typeof value === "number") return <span style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{field === "type" ? evidenceLabels[String(value)] ?? "历史核对方式" : String(value)}</span>;
  if (depth > 8) return <details><summary>查看复杂历史内容</summary><pre>{JSON.stringify(value, null, 2)}</pre></details>;
  if (Array.isArray(value)) return <ol>{comparisonEntries(value).map(item => <li key={item.key}><ComparisonValue value={item.value} depth={depth + 1} /></li>)}</ol>;
  const entries = Object.entries(record(value));
  const visible = entries.filter(([key]) => fieldLabels[key]);
  const extras = entries.filter(([key]) => !fieldLabels[key] && !["id", "point_id", "rubric_point_id", "source_refs"].includes(key));
  return <><dl>{visible.map(([key, item]) => <div key={key}><dt>{fieldLabels[key]}</dt><dd><ComparisonValue value={item} field={key} depth={depth + 1} /></dd></div>)}</dl>
    {extras.length ? <details><summary>查看 {extras.length} 项历史补充内容</summary>{extras.map(([key, item]) => <div key={key}><strong>{key}</strong><ComparisonValue value={item} depth={depth + 1} /></div>)}</details> : null}</>;
}

type ComparisonRow = { label: string; local: unknown; latest: unknown; attachments?: boolean; templateReference?: boolean };

export function QuestionBankConflictComparison({ original, latest, localContent, scoringDraft }: Readonly<{
  original: QuestionBankVersion | null;
  latest: QuestionBankVersion;
  localContent: Record<string, unknown>;
  scoringDraft: unknown;
}>) {
  const originalScoring = original?.scoring ?? latest.scoring;
  const snapshot = record(scoringDraft);
  const fields = record(snapshot.fields);
  // 缺少草稿快照的字段沿用原版本；显式清空的字段不能回退成原答案，否则比较会掩盖删除。
  const has = (value: Record<string, unknown>, key: string) => Object.prototype.hasOwnProperty.call(value, key);
  const local = (key: string, fallback: unknown) => has(fields, key) ? fields[key] : fallback;
  const content = (key: keyof QuestionBankVersion) => has(localContent, key) ? localContent[key] : original?.[key];
  const originalAnswer = originalScoring.answer?.standard_answer;
  const originalAnswerText = typeof originalAnswer === "string" ? originalAnswer : JSON.stringify(originalAnswer);
  const localAnswer = local("answer", originalAnswer);
  const hasLocalAnswer = typeof localAnswer === "string" ? Boolean(localAnswer.trim()) : localAnswer !== undefined && localAnswer !== null;
  const rubricEnabled = Boolean(local("rubricEnabled", Boolean(originalScoring.rubric)));
  const localPoints = rubricEnabled ? local("points", originalScoring.rubric?.points ?? []) : [];
  const normalizePoint = (value: unknown) => {
    const point = record(value);
    return { description: point.description, score: point.score, required: Boolean(point.required), conditions: parseDraft(point.evidence ?? point.evidence_requirements ?? []) };
  };
  const points = Array.isArray(localPoints) ? localPoints.map(normalizePoint) : [];
  const latestPoints = latest.scoring.rubric?.points.map(normalizePoint) ?? [];
  const rows: ComparisonRow[] = [
    { label: "题干", local: content("stem"), latest: latest.stem },
    { label: "满分（分）", local: content("default_score"), latest: latest.default_score },
    { label: "题型", local: questionTypeOptions.find(item => item.value === content("question_type"))?.label ?? "题型待核对", latest: questionTypeOptions.find(item => item.value === latest.question_type)?.label ?? "题型待核对" },
    { label: "选项", local: content("options"), latest: latest.options },
    { label: "知识点", local: content("knowledge_points"), latest: latest.knowledge_points },
    { label: "标准答案", local: localAnswer === originalAnswerText ? originalAnswer : parseDraft(localAnswer), latest: latest.scoring.answer?.standard_answer },
    { label: "等价答案", local: hasLocalAnswer ? parseDraft(local("equivalent", originalScoring.answer?.equivalent_answers ?? [])) : [], latest: latest.scoring.answer?.equivalent_answers ?? [] },
    { label: "允许绝对误差", local: hasLocalAnswer ? local("absolute", originalScoring.answer?.tolerance?.absolute) : undefined, latest: latest.scoring.answer?.tolerance?.absolute },
    { label: "允许相对误差（比例）", local: hasLocalAnswer ? local("relative", originalScoring.answer?.tolerance?.relative) : undefined, latest: latest.scoring.answer?.tolerance?.relative },
    { label: "解析", local: local("solution", originalScoring.solution?.raw_text), latest: latest.scoring.solution?.raw_text },
    { label: "分步解析", local: parseDraft(local("steps", originalScoring.solution?.steps ?? [])), latest: latest.scoring.solution?.steps ?? [] },
    { label: "使用评分细则", local: rubricEnabled, latest: Boolean(latest.scoring.rubric) },
    { label: "评分细则满分（分）", local: rubricEnabled ? original?.default_score : undefined, latest: latest.scoring.rubric?.max_score },
    ...Array.from({ length: Math.max(points.length, latestPoints.length) }, (_, index) => ({ label: `评分点 ${index + 1}`, local: points[index] ? { description: points[index].description, score: points[index].score, required: points[index].required } : undefined, latest: latestPoints[index] ? { description: latestPoints[index].description, score: latestPoints[index].score, required: latestPoints[index].required } : undefined })),
    ...Array.from({ length: Math.max(points.length, latestPoints.length) }, (_, index) => ({ label: `评分点 ${index + 1} · 核对条件`, local: points[index]?.conditions, latest: latestPoints[index]?.conditions })),
    { label: "扣分规则", local: rubricEnabled ? parseDraft(local("deductions", originalScoring.rubric?.deductions ?? [])) : [], latest: latest.scoring.rubric?.deductions ?? [] },
    { label: "样例答案", local: rubricEnabled ? parseDraft(local("examples", originalScoring.rubric?.examples ?? [])) : [], latest: latest.scoring.rubric?.examples ?? [] },
    { label: "评分附件", local: has(snapshot, "assets") ? snapshot.assets : originalScoring.assets, latest: latest.scoring.assets, attachments: true },
    { label: "引用评分模板", local: local("template", originalScoring.template_version_id) ?? null, latest: latest.scoring.template_version_id ?? null, templateReference: true },
    { label: "可用范围", local: local("use_policy", originalScoring.use_policy) === "exam_allowed" ? "可用于考试" : "仅用于练习", latest: latest.scoring.use_policy === "exam_allowed" ? "可用于考试" : "仅用于练习" }
  ];
  const attachmentNames = (value: unknown) => Array.isArray(value) ? value.map(asset => typeof asset === "string" ? asset : record(asset).name || "附件名称未确认") : value;
  // 正常模板引用是 ID；历史草稿若保留了对象，展示完整字段供人工核对，不能隐成 [object Object]。
  const display = (row: ComparisonRow, value: unknown) => row.templateReference ? <><span>{value ? "已选用评分模板；请核对对应评分点" : "未选用评分模板"}</span>{value ? <details><summary>查看模板版本记录</summary><code>{typeof value === "string" ? value : JSON.stringify(value, null, 2)}</code></details> : null}</> : <ComparisonValue value={row.attachments ? attachmentNames(value) : value} />;
  return <div className="question-bank-conflict-comparison">
    <div className="form-grid"><h3>本地修改 · 原修订 {original?.revision ?? "待核对"}</h3><h3>最新版本 · 修订 {latest.revision}</h3></div>
    {rows.map(row => {
      const changed = fingerprint(row.local) !== fingerprint(row.latest);
      return <section key={row.label} data-changed={changed} aria-label={`${row.label}${changed ? " · 有差异" : " · 相同"}`}>
        <h4>{row.label} <small>{changed ? "有差异，请核对" : "相同"}</small></h4>
        <div className="form-grid"><div aria-label={`本地${row.label}`}><small>本地修改</small>{display(row, row.local)}</div><div aria-label={`最新${row.label}`}><small>最新版本</small>{display(row, row.latest)}</div></div>
      </section>;
    })}
  </div>;
}
