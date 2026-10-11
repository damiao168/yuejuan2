import { Alert, Button, Input, InputNumber, Select, Space, Switch } from "antd";
import { useTeacherFieldRows } from "./structuredTeacherFieldRows";

const labels: Record<string, string> = {
  description: "说明", score: "分值", points: "得分", deduction: "扣分", reason: "原因",
  content: "内容", step_no: "步骤序号", answer: "答案", standard_answer: "标准答案",
  text: "文本", value: "答案值", answers: "各空答案", blanks: "各空答案", unit: "单位",
  condition: "适用条件", feedback: "评语", explanation: "解析", comment: "说明",
  type: "核对方式", target: "核对内容", minimum: "至少满足数量", children: "子条件",
  expected_score: "示例得分", student_answer: "学生作答", example: "示例", max_score: "满分", required: "必须满足",
  rule: "规则", trigger: "触发条件", penalty: "扣分", cap: "分值上限", max_deduction: "最多扣分", expected: "预期结果", result: "结果"
};

function TeacherValue({ value, onChange, disabled, label = "内容", depth = 0 }: Readonly<{
  value: unknown; onChange: (value: unknown) => void; disabled?: boolean; label?: string; depth?: number;
}>) {
  if (label === "核对方式" && typeof value === "string") return <Select aria-label={label} value={value} disabled={disabled} options={[
    ["concept", "出现指定概念"], ["unit", "单位正确"], ["domain", "定义域或取值条件"], ["valid_transformation", "关键变形成立"], ["final_result", "最终结果正确"], ["all_of", "全部条件满足"], ["any_of", "任一条件满足"], ["at_least", "至少满足指定数量"]
  ].map(([code, name]) => ({value:code,label:name})).concat(["concept","unit","domain","valid_transformation","final_result","all_of","any_of","at_least"].includes(value) ? [] : [{value,label:"历史核对方式（保留）"}])} onChange={onChange} />;
  if (depth > 8) return <Alert type="info" message="此项为复杂历史数据，已保留原值；请联系题库负责人调整。" />;
  if (Array.isArray(value)) return <TeacherArrayEditor value={value} onChange={onChange} disabled={disabled} label={label} depth={depth} />;
  if (value && typeof value === "object") {
    const entries = Object.entries(value);
    const visible = entries.filter(([key]) => labels[key]);
    const extras = entries.filter(([key]) => !labels[key] && !["id", "point_id", "rubric_point_id", "source_refs"].includes(key));
    return <div className="form-grid">{visible.map(([key, item]) => <label key={key}>
      <span>{labels[key]}</span>
      <TeacherValue value={item} onChange={next => onChange({ ...value as Record<string, unknown>, [key]: next })} disabled={disabled} label={labels[key]} depth={depth + 1} />
    </label>)}{extras.length ? <p className="muted">另有 {extras.length} 项历史补充信息，保存时保持原内容。需要调整时请联系题目负责人。</p> : null}</div>;
  }
  if (typeof value === "boolean") return <Switch aria-label={label} checked={value} checkedChildren="是" unCheckedChildren="否" disabled={disabled} onChange={onChange} />;
  if (typeof value === "number") return <InputNumber aria-label={label} value={value} disabled={disabled} onChange={next => onChange(next ?? 0)} />;
  return <Input.TextArea aria-label={label} value={primitiveText(value)} disabled={disabled} autoSize={{ minRows: 1, maxRows: 6 }} onChange={event => onChange(event.target.value)} />;
}

function TeacherArrayEditor({ value, onChange, disabled, label, depth }: Readonly<{
  value: unknown[]; onChange: (value: unknown) => void; disabled?: boolean; label: string; depth: number;
}>) {
  const { rows, rowKeys, rememberChange } = useTeacherFieldRows(value);
  function change(nextValues: unknown[], keys: string[]) {
    rememberChange(nextValues, keys);
    onChange(nextValues);
  }
  return <div className="page-stack">{rows.map((row, index) => <div key={row.key}>
    <TeacherValue value={row.value} onChange={next => change(value.map((entry, offset) => offset === index ? next : entry), rowKeys)} disabled={disabled} label={`${label} ${index + 1}`} depth={depth + 1} />
    <Button disabled={disabled} onClick={() => change(value.filter((_, offset) => offset !== index), rowKeys.filter((_, offset) => offset !== index))}>移除第 {index + 1} 项</Button>
  </div>)}<Button disabled={disabled} onClick={() => change([...value, ""], rowKeys)}>添加一项</Button></div>;
}

function parsed(value: string): unknown { try { return JSON.parse(value); } catch { return value; } }
function answerString(value: unknown) { return typeof value === "string" ? value : JSON.stringify(value); }

function primitiveText(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  // 值来自 JSON 或原文本；数组和对象已交给结构化入口，null/undefined 仍显示为空。
  return "";
}

function choiceCurrent(answer: unknown, multiple: boolean): string | string[] {
  if (Array.isArray(answer)) {
    if (multiple) return answer.map(String);
    return String(answer[0] ?? "");
  }
  const text = primitiveText(answer);
  if (multiple) return text.split(/[\s,，]+/).filter(Boolean);
  return text;
}

function choiceOptions(options: string[], current: string | string[]) {
  const codes = options.length ? options.map((_, index) => String.fromCodePoint(65 + index)) : ["A", "B", "C", "D"];
  let selected = current;
  if (!Array.isArray(selected)) selected = selected ? [selected] : [];
  return [...new Set([...codes, ...selected])].map(code => {
    const text = options[codes.indexOf(code)];
    if (text) return { value: code, label: `${code}．${text}` };
    return { value: code, label: code };
  });
}

function ChoiceAnswerEditor({ answer, multiple, options, disabled, onChange }: Readonly<{
  answer: unknown; multiple: boolean; options: string[]; disabled?: boolean; onChange?: (value: string) => void;
}>) {
  const current = choiceCurrent(answer, multiple);
  function change(next: string | string[]) {
    // 历史单选数组仍提交单元素数组，不能因控件现在显示单个选项而改变保存结构。
    if (Array.isArray(answer) && !multiple) {
      onChange?.(JSON.stringify([next]));
      return;
    }
    onChange?.(answerString(next));
  }
  return <Select aria-label="标准答案选项" mode={multiple ? "multiple" : undefined} value={current || undefined} disabled={disabled} placeholder={multiple ? "选择所有正确选项" : "选择一个正确选项"}
    options={choiceOptions(options, current)} onChange={change} />;
}

function trueFalseSelection(answer: unknown): string | undefined {
  if (answer === true || answer === "true" || answer === "正确") return "true";
  if (answer === false || answer === "false" || answer === "错误") return "false";
  return undefined;
}

function TrueFalseAnswerEditor({ answer, disabled, onChange }: Readonly<{
  answer: unknown; disabled?: boolean; onChange?: (value: string) => void;
}>) {
  return <Select aria-label="判断题标准答案" value={trueFalseSelection(answer)} disabled={disabled} placeholder="选择正确或错误" options={[{ value: "true", label: "正确（√）" }, { value: "false", label: "错误（×）" }]} onChange={next => onChange?.(next)} />;
}

// 新增示例提供三个独立字段；已有字符串和扩展对象仍按原结构编辑、保存，不强制转换。
export function createScoringExample() {
  return { student_answer: "", expected_score: 0, reason: "" };
}

/** 表单内部仍使用既有序列化字段；教师通过选择与逐项输入编辑，历史扩展字段在原结构上更新。 */
export function StructuredAnswerEditor({ value = "", onChange, questionType, options = [], disabled }: Readonly<{
  value?: string; onChange?: (value: string) => void; questionType?: string; options?: string[]; disabled?: boolean;
}>) {
  const answer = parsed(value);
  if (answer !== null && typeof answer === "object" && !Array.isArray(answer)) {
    return <TeacherValue value={answer} onChange={next => onChange?.(answerString(next))} disabled={disabled} label="标准答案" />;
  }
  if (questionType === "single_choice" || questionType === "multiple_choice") {
    return <ChoiceAnswerEditor answer={answer} multiple={questionType === "multiple_choice"} options={options} disabled={disabled} onChange={onChange} />;
  }
  if (questionType === "true_false") return <TrueFalseAnswerEditor answer={answer} disabled={disabled} onChange={onChange} />;
  if (questionType === "fill_blank" && !Array.isArray(answer)) return <div>
    <TeacherValue value={answer} onChange={next => onChange?.(answerString(next))} disabled={disabled} label="第 1 空答案" />
    <Button disabled={disabled} onClick={() => onChange?.(JSON.stringify([answer ?? "", ""]))}>增加一空</Button>
  </div>;
  return <TeacherValue value={answer} onChange={next => onChange?.(answerString(next))} disabled={disabled} label="标准答案" />;
}

export function StructuredListEditor({ value = "[]", onChange, disabled, itemLabel = "项目", newItem = "" }: Readonly<{
  value?: string; onChange?: (value: string) => void; disabled?: boolean; itemLabel?: string; newItem?: unknown;
}>) {
  const list = parsed(value || "[]");
  // 无法读取的数据不能静默替换成空列表，否则保存其他字段也会丢失原来的评分事实。
  if (!Array.isArray(list)) return <Alert type="error" showIcon message={`${itemLabel}暂时无法读取，原数据已保留`} description="请联系考试或题库负责人核对后再保存。" />;
  return <TeacherListRows list={list} onChange={onChange} disabled={disabled} itemLabel={itemLabel} newItem={newItem} />;
}

function TeacherListRows({ list, onChange, disabled, itemLabel, newItem }: Readonly<{
  list: unknown[]; onChange?: (value: string) => void; disabled?: boolean; itemLabel: string; newItem: unknown;
}>) {
  const { rows, rowKeys, rememberChange } = useTeacherFieldRows(list);
  function change(nextValues: unknown[], keys: string[]) {
    if (!onChange) return;
    const text = JSON.stringify(nextValues);
    rememberChange(nextValues, keys);
    onChange(text);
  }
  function add() {
    if (!onChange) return;
    const item = typeof newItem === "object" && newItem ? structuredClone(newItem) : newItem;
    change([...list, item], rowKeys);
  }
  return <div className="page-stack">{rows.map((row, index) => <section key={row.key}>
    <Space><strong>{itemLabel} {index + 1}</strong><Button disabled={disabled} onClick={() => change(list.filter((_, offset) => offset !== index), rowKeys.filter((_, offset) => offset !== index))}>移除{itemLabel}</Button></Space>
    <TeacherValue value={row.value} onChange={next => change(list.map((entry, offset) => offset === index ? next : entry), rowKeys)} disabled={disabled} label={`${itemLabel} ${index + 1}`} />
  </section>)}<Button disabled={disabled} onClick={add}>添加{itemLabel}</Button></div>;
}
