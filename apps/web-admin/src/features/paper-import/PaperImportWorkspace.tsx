import { useState } from "react";
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
  onNavigate, connectionError, onRecheck
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
        <div><span className="preparation-kicker">考试资料</span><h2>{latestPaperImport ? "已添加的资料" : "添加试卷资料"}</h2><p>上传试题、答案、解析或评分标准，系统会自动识别并匹配到题目。</p></div>
        <Button icon={<ClipboardPaste size={16} />} disabled={!canManage || parsing} onClick={() => setTextImportOpen(true)}>粘贴文本</Button>
      </div>
      <Upload.Dragger {...uploadProps} disabled={!canManage || parsing} className="paper-import-dropzone">
        <div className="paper-import-upload-content">
          <FileUp size={22} />
          <strong>{parsing ? "正在上传并识别…" : "选择文件或拖到这里"}</strong>
          <span>PDF、Word、图片、Markdown、TXT；可一次添加多份资料</span>
        </div>
      </Upload.Dragger>
      <PaperImportSourceList job={latestPaperImport} workflow={workflow} />
      <PaperImportTextModal open={textImportOpen} parsing={parsing} text={pastedText} role={pastedRole} tab={textImportTab}
        onTextChange={setPastedText} onRoleChange={setPastedRole} onTabChange={setTextImportTab}
        onSubmit={() => void submitPastedText()} onCancel={() => setTextImportOpen(false)} />
    </section>
    {connectionError ? <Alert type="warning" showIcon message="当前任务结果尚未确认" description={`${connectionError}。下方显示最后一次读取的状态，请重新检查原任务获取最新结果。`} action={<Button onClick={onRecheck}>重新检查原任务</Button>} /> : null}
    {latestPaperImport ? <PaperImportResults job={latestPaperImport} workflow={workflow} existingQuestions={existingQuestions} onReview={goToQuestionReview} /> : null}
  </>;
}
