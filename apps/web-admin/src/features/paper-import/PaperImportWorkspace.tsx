import { useState, type ReactNode } from "react";
import { Alert, Button, Upload } from "antd";
import { ClipboardPaste, FileUp } from "lucide-react";
import type { PaperImportJob, PaperImportRole, Question } from "../../api/papers";
import { PaperImportResults } from "./PaperImportResults";
import { PaperImportSourceList } from "./PaperImportSourceList";
import { PaperImportTextModal } from "./PaperImportTextModal";
import type { usePaperImportWorkflow } from "./usePaperImportWorkflow";

export function PaperImportWorkspace({
  canManage,
  latestPaperImport,
  existingQuestions,
  workflow,
  setShowQuestionEditor,
  initialExamId,
  onNavigate, connectionError, onRecheck, headerStatus
}: Readonly<{
  canManage: boolean;
  latestPaperImport?: PaperImportJob;
  existingQuestions?: Question[];
  workflow: ReturnType<typeof usePaperImportWorkflow>;
  setShowQuestionEditor: (show: boolean) => void;
  initialExamId: string;
  onNavigate?: (path: string) => void;
  connectionError?: string;
  onRecheck?: () => void;
  headerStatus?: ReactNode;
}>) {
  const { uploadProps, parsing, importPastedText } = workflow;
  const [textImportOpen, setTextImportOpen] = useState(false);
  const [pastedText, setPastedText] = useState("");
  const [pastedRole, setPastedRole] = useState<PaperImportRole>("auto");
  const [textImportTab, setTextImportTab] = useState("edit");
  const submitPastedText = async () => {
    const imported = await importPastedText(pastedText, pastedRole);
    // 失败保留输入供重试；取消只关闭弹窗，只有导入成功才清空这四个状态。
    if (!imported) return;
    setTextImportOpen(false);
    setPastedText("");
    setPastedRole("auto");
    setTextImportTab("edit");
  };
  const goToQuestionReview = () => {
    if (initialExamId && onNavigate) {
      onNavigate("/exams/" + encodeURIComponent(initialExamId) + "/questions");
      return;
    }
    setShowQuestionEditor(true);
    requestAnimationFrame(() => document.getElementById("paper-question-workbench")?.scrollIntoView({ behavior: "smooth", block: "start" }));
  };

  return <>
    <section className={"workspace-section paper-source-files" + (latestPaperImport ? " has-materials" : " is-empty")}>
      <div className="section-head">
        <div className="paper-source-heading"><div className="paper-source-title"><h2>{latestPaperImport ? "已添加的资料" : "添加试卷资料"}</h2>{headerStatus}</div><p>添加试题、答案、解析或评分标准，识别后请核对匹配结果。</p></div>
        <Button icon={<ClipboardPaste size={16} />} disabled={!canManage || parsing} onClick={() => setTextImportOpen(true)}>粘贴文本</Button>
      </div>
      <p>需要模型解析时，发送本次新增资料的相关原文片段；图片资料发送本次新增图片页面。不会重发已识别的历史资料。</p>
      <Upload.Dragger {...uploadProps} disabled={!canManage || parsing} className="paper-import-dropzone">
        <div className="paper-import-upload-content">
          <FileUp size={22} />
          <strong>{parsing ? "正在上传并识别…" : "选择文件或拖到这里"}</strong>
          <span>PDF、Word（.docx）、图片、Markdown、TXT；支持多文件。</span>
        </div>
      </Upload.Dragger>
      <PaperImportSourceList job={latestPaperImport} workflow={workflow} />
      <PaperImportTextModal open={textImportOpen} parsing={parsing} text={pastedText} role={pastedRole} tab={textImportTab}
        onTextChange={setPastedText} onRoleChange={setPastedRole} onTabChange={setTextImportTab}
        onSubmit={() => void submitPastedText()} onCancel={() => setTextImportOpen(false)} />
    </section>
    {connectionError ? <Alert type="warning" showIcon message="结果状态未更新" description={`下方是上次读取的结果。请重新检查原任务；${connectionError}`} action={<Button onClick={onRecheck}>重新检查原任务</Button>} /> : null}
    {latestPaperImport ? <PaperImportResults job={latestPaperImport} workflow={workflow} existingQuestions={existingQuestions} onReview={goToQuestionReview} /> : null}
  </>;
}
