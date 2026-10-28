import { Input, Modal, Select, Tabs } from "antd";
import type { PaperImportRole } from "../../api/papers";
import { MathMarkdown } from "../../components/MathMarkdown";
import { MAX_PASTED_MATERIAL_CHARS } from "./materials";
import { importRoleOptions } from "./paperImportPresentation";

export function PaperImportTextModal({ open, parsing, text, role, tab, onTextChange, onRoleChange, onTabChange, onSubmit, onCancel }: Readonly<{
  open: boolean; parsing: boolean; text: string; role: PaperImportRole; tab: string;
  onTextChange: (text: string) => void; onRoleChange: (role: PaperImportRole) => void;
  onTabChange: (tab: string) => void; onSubmit: () => void; onCancel: () => void;
}>) {
  return <Modal title="粘贴试卷或答案" open={open} width={760} okText="添加并识别" cancelText="取消"
    confirmLoading={parsing} okButtonProps={{ disabled: text.trim().length < 20 || text.length > MAX_PASTED_MATERIAL_CHARS }}
    onOk={onSubmit} onCancel={onCancel}>
    <div className="paper-text-import">
      <div className="paper-text-import-toolbar">
        <label htmlFor="paper-text-import-role">资料类型</label>
        <Select<PaperImportRole> id="paper-text-import-role" value={role} options={importRoleOptions} onChange={onRoleChange} />
      </div>
      <p>粘贴题目、答案或解析，至少20个字符；建议保留题号。</p>
      {/* 数据发送范围是输入前的必要告知；只有公式格式帮助可以折叠。 */}
      <p>结构清晰的文本在本地解析；无法可靠确定结构时，相关新增原文会送入模型解析。不会重发已识别的历史资料。</p>
      <details><summary>公式格式帮助</summary><p>行内公式用 <code>$x^2$</code>，独立公式用 <code>$$...$$</code>；也支持 <code>\(...\)</code> 与 <code>\[...\]</code>。</p></details>
      <Tabs activeKey={tab} onChange={onTabChange} items={[
        { key: "edit", label: "编辑", children: <Input.TextArea autoFocus value={text}
          onChange={(event) => onTextChange(event.target.value)}
          placeholder={"在此粘贴题目、答案、解析或评分标准。\n\n示例：\n## 第 1 题\n已知 $f(x)=x^2+2x+1$，求 $f(x)$ 的最小值。"}
          autoSize={{ minRows: 12, maxRows: 20 }} maxLength={MAX_PASTED_MATERIAL_CHARS} showCount /> },
        { key: "preview", label: "公式预览", children: text.trim()
          ? <div className="paper-text-import-preview"><MathMarkdown>{text}</MathMarkdown></div>
          : <div className="paper-text-import-empty">粘贴内容后可在这里核对 Markdown 与数学公式。</div> }
      ]} />
    </div>
  </Modal>;
}
