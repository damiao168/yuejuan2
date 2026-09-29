import { useState } from "react";
import { Alert, Button, Input, List, Modal, Progress, Select, Space, Tabs, Upload } from "antd";
import { StatusBadge } from "@edugrade/ui";
import { ChevronDown, ChevronUp, ClipboardPaste, ExternalLink, FileUp, Trash2 } from "lucide-react";
import { getPaperImportUserMessage } from "../../api/client";
import type { PaperImportJob, PaperImportRole, Question } from "../../api/papers";
import { MathMarkdown } from "../../components/MathMarkdown";
import {
  hasBlockingImportIssues,
  hasNoExamContentDetected,
  isPaperImportCancelled,
  MAX_PASTED_MATERIAL_CHARS,
  orderedSourcesAfterMove,
  paperImportProgress,
  paperImportReviewIssues,
  paperImportSummary,
  sourcesAfterRoleChange
} from "./materials";
import { PaperImportReviewPanel } from "./PaperImportReviewPanel";
import type { usePaperImportWorkflow } from "./usePaperImportWorkflow";

const importRoleOptions = [
  { label: "自动判断", value: "auto" },
  { label: "题目", value: "question" },
  { label: "答案", value: "answer" },
  { label: "解析", value: "solution" },
  { label: "评分标准", value: "rubric" },
  { label: "混合资料", value: "mixed" },
  { label: "未知", value: "unknown" }
];
const rubricRequiredArchetypes = new Set(["structured_steps", "short_constructed", "extended_response", "diagram_graph", "table_experiment"]);

export function PaperImportWorkspace({
  canManage,
  latestPaperImport,
  existingQuestions,
  workflow,
  setShowQuestionEditor,
  initialExamId,
  onNavigate
}: {
  canManage: boolean;
  latestPaperImport?: PaperImportJob;
  existingQuestions?: Question[];
  workflow: ReturnType<typeof usePaperImportWorkflow>;
  setShowQuestionEditor: (show: boolean) => void;
  initialExamId: string;
  onNavigate?: (path: string) => void;
}) {
  const {
    uploadProps, parsing, reviewDrafts, savingImportReview, updatingImportSources,
    stoppingImport, retryingParse, invalidReviewRubric, invalidReviewScore, replaceImportSources,
    stopPaperImport, retryImportParse, removeImportSource, openImportSource,
    confirmPaperImport, saveImportReview, updateReviewDraft, importPastedText
  } = workflow;
  const [textImportOpen, setTextImportOpen] = useState(false);
  const [pastedText, setPastedText] = useState("");
  const [pastedRole, setPastedRole] = useState<PaperImportRole>("auto");
  const [textImportTab, setTextImportTab] = useState("edit");
  const submitPastedText = async () => {
    const imported = await importPastedText(pastedText, pastedRole);
    if (!imported) return;
    setTextImportOpen(false);
    setPastedText("");
    setPastedRole("auto");
    setTextImportTab("edit");
  };
  const importCancelled = latestPaperImport ? isPaperImportCancelled(latestPaperImport) : false;
  const noExamContentDetected = latestPaperImport ? hasNoExamContentDetected(latestPaperImport) : false;
  // 重新识别期间仍保留上一轮题目，但暂不展示旧问题，避免把历史告警当作本轮结果。
  const visibleImportIssues = importCancelled || latestPaperImport?.status === "processing" ? [] : noExamContentDetected
    ? (latestPaperImport?.structured_issues ?? []).filter((issue) => issue.code === "NO_EXAM_CONTENT_DETECTED")
    : latestPaperImport?.structured_issues?.length
      ? paperImportReviewIssues(latestPaperImport)
      : (latestPaperImport?.issues ?? []).map((message) => ({ message, certainty: "unknown" as const }));
  const importSummary = latestPaperImport ? paperImportSummary(latestPaperImport) : null;
  const excludedPageFurniture = importCancelled || latestPaperImport?.status === "processing" ? []
    : (latestPaperImport?.structured_issues ?? []).filter((issue) => issue.code === "PAGE_FURNITURE_EXCLUDED");
  const importProgress = latestPaperImport ? paperImportProgress(latestPaperImport) : null;
  const progressTiming = importProgress?.startedAt
    ? `已处理约 ${Math.max(1, Math.floor((Date.now() - new Date(importProgress.startedAt).getTime()) / 1000))} 秒`
    : "";
  const canEditImportSources = latestPaperImport && ["processing", "review_required", "failed", "cancelled"].includes(latestPaperImport.status);
  const goToQuestionReview = () => {
    if (initialExamId && onNavigate) {
      onNavigate(`/exams/${encodeURIComponent(initialExamId)}/questions`);
      return;
    }
    setShowQuestionEditor(true);
    requestAnimationFrame(() => document.getElementById("paper-question-workbench")?.scrollIntoView({ behavior: "smooth", block: "start" }));
  };

  return <>
    <section className={`workspace-section paper-source-files${latestPaperImport ? " has-materials" : " is-empty"}`}>
      <div className="section-head"><div><span className="preparation-kicker">考试资料</span><h2>{latestPaperImport ? "已添加的资料" : "添加试卷资料"}</h2><p>上传试题、答案、解析或评分标准，系统会自动识别并匹配到题目。</p></div><Button icon={<ClipboardPaste size={16} />} disabled={!canManage || parsing} onClick={() => setTextImportOpen(true)}>粘贴文本</Button></div>
      <Upload.Dragger {...uploadProps} disabled={!canManage || parsing} className="paper-import-dropzone">
        <div className="paper-import-upload-content">
          <FileUp size={22} />
          <strong>{parsing ? "正在上传并识别…" : "选择文件或拖到这里"}</strong>
          <span>PDF、Word、图片、Markdown、TXT；可一次添加多份资料</span>
        </div>
      </Upload.Dragger>
      {latestPaperImport?.sources.length ? <List
        size="small"
        className="paper-import-source-list"
        dataSource={latestPaperImport.sources}
        renderItem={(source, index) => <List.Item actions={[
          <Button key="open" type="text" size="small" icon={<ExternalLink size={14} />} onClick={() => void openImportSource(source.file_asset_id)}>来源</Button>,
          <Button key="up" type="text" size="small" aria-label="上移资料" icon={<ChevronUp size={14} />} disabled={index === 0 || !canEditImportSources || updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, orderedSourcesAfterMove(latestPaperImport.sources, source.id, -1))} />,
          <Button key="down" type="text" size="small" aria-label="下移资料" icon={<ChevronDown size={14} />} disabled={index === latestPaperImport.sources.length - 1 || !canEditImportSources || updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, orderedSourcesAfterMove(latestPaperImport.sources, source.id, 1))} />,
          <Select<PaperImportRole> key="role" size="small" aria-label="资料类型" value={source.role_hint} options={importRoleOptions} disabled={!canEditImportSources || updatingImportSources} onChange={(roleHint) => void replaceImportSources(latestPaperImport, sourcesAfterRoleChange(latestPaperImport.sources, source.id, roleHint))} />,
          <Button key="remove" type="text" danger size="small" aria-label="删除资料" icon={<Trash2 size={14} />} disabled={!canEditImportSources || updatingImportSources} onClick={() => void removeImportSource(latestPaperImport, source.id)} />
        ]} extra={<StatusBadge tone={importCancelled ? "neutral" : source.processing_status === "processed" ? "success" : source.processing_status === "failed" ? "danger" : "processing"}>{importCancelled ? "已停止" : source.processing_status === "processed" ? "已识别" : source.processing_status === "failed" ? "失败" : "处理中"}</StatusBadge>}>
          <List.Item.Meta title={`${source.document_index + 1}. ${source.original_name || "考试资料"}`} description={`识别内容：${source.detected_role === "question" ? "题目" : source.detected_role === "answer" ? "答案" : source.detected_role === "solution" ? "解析" : source.detected_role === "rubric" ? "评分标准" : source.detected_role === "mixed" ? "混合内容" : "识别中"}${source.role_confidence ? ` · 资料类型判断 ${Math.round(source.role_confidence * 100)}%（不代表逐字准确率）` : ""}`} />
        </List.Item>}
      /> : null}
      <Modal
        title="粘贴 Markdown 资料"
        open={textImportOpen}
        width={760}
        okText="添加并识别"
        cancelText="取消"
        confirmLoading={parsing}
        okButtonProps={{ disabled: pastedText.trim().length < 20 || pastedText.length > MAX_PASTED_MATERIAL_CHARS }}
        onOk={() => void submitPastedText()}
        onCancel={() => setTextImportOpen(false)}
      >
        <div className="paper-text-import">
          <div className="paper-text-import-toolbar">
            <label htmlFor="paper-text-import-role">资料类型</label>
            <Select<PaperImportRole> id="paper-text-import-role" value={pastedRole} options={importRoleOptions} onChange={setPastedRole} />
            <span>行内公式使用 <code>$x^2$</code>，独立公式使用 <code>$$...$$</code>；也兼容 <code>\(...\)</code> 与 <code>\[...\]</code>。</span>
          </div>
          <Alert
            type="info"
            showIcon
            message="结构清晰的 Markdown 在本地解析"
            description="题号、【答案】和【解析/详解】明确时不会调用大模型。只有结构无法可靠确定时，才发送本次新增资料的相关原文片段进入模型解析；若本次上传的是图片，则发送本次新增图片页面。不会重发已识别的历史资料。"
          />
          <Tabs
            activeKey={textImportTab}
            onChange={setTextImportTab}
            items={[
              {
                key: "edit",
                label: "编辑",
                children: <Input.TextArea
                  autoFocus
                  value={pastedText}
                  onChange={(event) => setPastedText(event.target.value)}
                  placeholder={"在此粘贴题目、答案、解析或评分标准。\n\n示例：\n## 第 1 题\n已知 $f(x)=x^2+2x+1$，求 $f(x)$ 的最小值。"}
                  autoSize={{ minRows: 12, maxRows: 20 }}
                  maxLength={MAX_PASTED_MATERIAL_CHARS}
                  showCount
                />
              },
              {
                key: "preview",
                label: "公式预览",
                children: pastedText.trim()
                  ? <div className="paper-text-import-preview"><MathMarkdown>{pastedText}</MathMarkdown></div>
                  : <div className="paper-text-import-empty">粘贴内容后可在这里核对 Markdown 与数学公式。</div>
              }
            ]}
          />
        </div>
      </Modal>
    </section>

    {latestPaperImport ? <section className="workspace-section paper-import-review">
      <div className="section-head">
        <div>
          <h2>自动识别结果</h2>
          <p>{latestPaperImport.status === "review_required"
            ? noExamContentDetected
              ? "未识别到与考试有关的题目、答案、解析或评分标准，请检查是否上传了无关图片或错误文件"
              : `已识别题目 ${latestPaperImport.question_candidates?.length ?? latestPaperImport.questions.length}、答案 ${latestPaperImport.answer_candidates?.length ?? 0}、解析 ${latestPaperImport.solution_candidates?.length ?? 0}、评分标准 ${latestPaperImport.rubric_candidates?.length ?? 0}`
            : latestPaperImport.status === "applied" ? "已确认并写入当前考试"
              : latestPaperImport.status === "failed" ? getPaperImportUserMessage(latestPaperImport.issues[0], "考试资料识别失败，请检查资料后重试")
                : latestPaperImport.status === "cancelled" ? "识别任务已手动停止，已上传资料仍然保留"
                  : "正在识别考试资料中的题目、答案与解析"}</p>
        </div>
        {latestPaperImport.status === "review_required" ? noExamContentDetected
          ? <Space wrap><Button type="primary" loading={updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, latestPaperImport.sources)}>重新识别</Button><Button onClick={goToQuestionReview}>手动补充题目</Button></Space>
          : <Space><Button onClick={goToQuestionReview}>逐题核对</Button><Button loading={savingImportReview} onClick={() => void saveImportReview(latestPaperImport)}>保存人工核对</Button><Button type="primary" loading={parsing} disabled={hasBlockingImportIssues(latestPaperImport) || invalidReviewRubric || invalidReviewScore} onClick={() => void confirmPaperImport(latestPaperImport)}>确认导入</Button></Space>
          : latestPaperImport.status === "failed" && latestPaperImport.error_code === "ai_parse_failed"
            ? <Space wrap><Button type="primary" loading={retryingParse} onClick={() => void retryImportParse(latestPaperImport)}>仅重新解析</Button><Button loading={updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, latestPaperImport.sources)}>重新识别全部</Button></Space>
            : latestPaperImport.status === "cancelled" && latestPaperImport.sources.length
              ? <Space wrap><Button type="primary" loading={retryingParse} onClick={() => void retryImportParse(latestPaperImport)}>仅解析本次补充</Button><Button loading={updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, latestPaperImport.sources)}>重新识别全部</Button></Space>
            : latestPaperImport.status === "failed" && latestPaperImport.sources.length
              ? <Button type="primary" loading={updatingImportSources} onClick={() => void replaceImportSources(latestPaperImport, latestPaperImport.sources)}>重新识别</Button>
              : latestPaperImport.status === "processing" ? <Button danger loading={stoppingImport} onClick={() => stopPaperImport(latestPaperImport)}>停止识别</Button> : null}
      </div>
      {latestPaperImport.status === "processing" && importProgress ? <div className="paper-import-progress" aria-live="polite">
        <div><strong>{importProgress.label}</strong><span>{importProgress.detail}{progressTiming ? ` · ${progressTiming}` : ""}</span></div>
        {importProgress.percent === undefined ? <div className="paper-import-progress-indeterminate" role="progressbar" aria-label="正在处理，暂无可计算的完成比例"><span /></div> : <Progress percent={importProgress.percent} format={() => importProgress.counter ?? `${importProgress.percent}%`} status="active" />}
      </div> : null}
      {latestPaperImport.status === "cancelled" && importProgress ? <div className="paper-import-progress" aria-live="polite"><div><strong>{importProgress.label}</strong><span>{importProgress.detail}</span></div>{importProgress.percent === undefined ? null : <Progress percent={importProgress.percent} status="normal" />}</div> : null}
      {latestPaperImport.status !== "applied" && reviewDrafts.length && latestPaperImport.status !== "review_required" ? <Alert
        type={latestPaperImport.status === "failed" ? "warning" : "info"}
        showIcon
        message={`上一轮已识别的 ${reviewDrafts.length} 道题目仍然保留`}
        description={latestPaperImport.status === "processing" ? "本次补充资料正在单独处理；完成后会按题号合并，以下历史结果暂时只读。" : latestPaperImport.status === "cancelled" ? "本次补充识别已停止，不影响以下历史结果。可选择“仅解析本次补充”继续。" : "本次补充资料处理失败，不影响以下历史结果。"}
      /> : null}
      {latestPaperImport.status !== "applied" && reviewDrafts.length ? <div className="paper-import-facts">
        <span><strong>{importSummary?.questions ?? 0}</strong> 道题目</span><span><strong>{importSummary?.answers ?? 0}</strong> 个答案</span><span><strong>{importSummary?.solutions ?? 0}</strong> 份解析</span><span><strong>{latestPaperImport.rubric_candidates?.length ?? 0}</strong> 份评分标准</span><span><strong>{importSummary?.reviewIssues ?? 0}</strong> 项需核对</span><span><strong>{latestPaperImport.questions.reduce((sum, item) => sum + item.score, 0)}</strong> 分</span>
      </div> : null}
      {latestPaperImport.status !== "applied" && reviewDrafts.length ? <PaperImportReviewPanel job={latestPaperImport} drafts={reviewDrafts} existingQuestions={existingQuestions} readOnly={latestPaperImport.status !== "review_required"} onChange={updateReviewDraft} onOpenSource={(ref) => void openImportSource(ref.file_asset_id, ref.page_no)} /> : null}
      {excludedPageFurniture.length ? <details><summary>已自动排除 {excludedPageFurniture.length} 项页眉、页脚或水印</summary><ul className="validation-issue-list">{excludedPageFurniture.map((issue, index) => <li key={`${issue.message}-${index}`}>{issue.message}{issue.source_refs?.[0]?.file_asset_id ? <Button type="link" size="small" onClick={() => void openImportSource(issue.source_refs[0].file_asset_id, issue.source_refs[0].page_no)}>核对来源</Button> : null}</li>)}</ul><p>原图和 OCR 原文仍保留；如果正文被误排除，请对照来源补充。</p></details> : null}
      {latestPaperImport.status === "applied" ? <div className="paper-import-result"><div className="paper-import-facts"><span>已写入题目：<strong>{latestPaperImport.questions.length}</strong></span><span>已配置答案：<strong>{latestPaperImport.questions.filter((item) => item.answer_key).length}</strong></span><span>已导入解析：<strong>{latestPaperImport.questions.filter((item) => item.solution).length}</strong></span><span>已配置评分标准：<strong>{latestPaperImport.questions.filter((item) => item.rubric).length}</strong></span><span>已锁定评分标准：<strong>{latestPaperImport.questions.filter((item) => item.rubric?.status === "locked").length}</strong></span><span>仍需处理：<strong>{latestPaperImport.questions.filter((item) => rubricRequiredArchetypes.has(item.assessment_archetype ?? "") && item.rubric?.status !== "locked").length}</strong></span></div><Button type="primary" onClick={goToQuestionReview}>逐题核对内容与分值</Button></div> : null}
      {visibleImportIssues.length ? <Alert type={latestPaperImport.status === "failed" || noExamContentDetected ? "error" : "warning"} showIcon message={noExamContentDetected ? "未识别到考试内容" : "还需完善"} description={<ul className="validation-issue-list">{visibleImportIssues.map((issue, index) => <li key={`${issue.message}-${index}`}><strong>{"question_no" in issue && issue.question_no ? `第${issue.question_no}题：` : ""}</strong>{getPaperImportUserMessage(issue.message, "考试资料存在需要核对的内容")}{issue.certainty === "suspected" ? "（疑似）" : ""}</li>)}</ul>} /> : null}
    </section> : null}
  </>;
}
