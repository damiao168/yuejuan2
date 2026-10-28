import { useState } from "react";
import { Input, Select } from "antd";

/** 常规操作选明确依据；其他情况仍输入说明，最终向原审计接口提交完整中文原因。 */
export function BusinessReasonField({ id, label, value, onChange, choices, placeholder, disabled }: Readonly<{
  id: string; label: string; value: string; onChange: (value: string) => void;
  choices: string[]; placeholder?: string; disabled?: boolean;
}>) {
  const [custom, setCustom] = useState(false);
  const usesCustom = custom || Boolean(value && !choices.includes(value));
  return <div>
    <label className="score-step-label" htmlFor={id}>{label}</label>
    <Select id={id} aria-label={label} disabled={disabled} value={usesCustom ? "__custom" : value || undefined} placeholder="选择本次操作依据" style={{ width: "100%" }} options={[...choices.map(choice => ({ label: choice, value: choice })), { label: "其他情况，填写说明", value: "__custom" }]} onChange={next => { setCustom(next === "__custom"); onChange(next === "__custom" ? "" : next); }} />
    {usesCustom ? <Input.TextArea aria-label={`${label}的补充说明`} rows={3} maxLength={1000} value={value} placeholder={placeholder || "请写明本次操作的依据和原因"} onChange={event => onChange(event.target.value)} disabled={disabled} /> : null}
  </div>;
}
