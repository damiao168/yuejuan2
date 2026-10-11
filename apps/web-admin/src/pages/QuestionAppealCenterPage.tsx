import { lazy, Suspense, useEffect, useState } from "react";
import { Alert, App, Select, Tabs } from "antd";
import type { SessionUser } from "../auth/session";
import { listExams, type Exam } from "../api/exams";
import { getUserErrorMessage } from "../api/client";
import { QuestionAppealWorkspace } from "../components/QuestionAppealWorkspace";
import type { ProductExperience } from "../router/experience";

const LegacyAppealCenterPage = lazy(() => import("./AppealCenterPage").then((module) => ({ default: module.AppealCenterPage })));

export function QuestionAppealCenterPage(props: Readonly<{
  mode: ProductExperience;
  canRead: boolean;
  canManage: boolean;
  canWork: boolean;
  canReadIdentities: boolean;
  canReadExams: boolean;
  currentUser: SessionUser;
  initialExamId?: string;
  canReadQuestionAppeals?: boolean;
  canManageScores?: boolean;
  onNavigate?: (path: string) => void;
}>) {
  const [tab, setTab] = useState("published");
  const [examId, setExamId] = useState(props.initialExamId ?? "");
  const [exams, setExams] = useState<Exam[]>([]);
  const [error, setError] = useState("");
  const { modal } = App.useApp();
  const [editor, setEditor] = useState({ dirty: false, busy: false });
  const changeView = (next: () => void) => {
    if (editor.busy) return;
    if (!editor.dirty) { next(); return; }
    modal.confirm({ title: "放弃未提交的复核回复？", content: "切换入口或考试后当前回复与复评选择无法恢复。", okText: "放弃并继续", cancelText: "继续编辑", onOk: () => { setEditor({ dirty: false, busy: false }); next(); } });
  };
  useEffect(() => {
    if (!props.canReadExams) return;
    let active = true;
    void listExams().then((response) => { if (active) setExams(response.exams); }).catch((cause) => { if (active) setError(getUserErrorMessage(cause, "考试筛选暂不可用，可先查看全部申请")); });
    return () => { active = false; };
  }, [props.canReadExams]);
  return <div className="appeal-shell">
    <section className="appeal-topbar"><div><h1>{props.mode === "teacher" ? "我的成绩复核" : "成绩复核"}</h1><p>{props.mode === "teacher" ? "处理学校分派给我的题目复核申请；考试名称和学科随受派任务提供，学生身份继续隐藏。先核对材料并移交独立意见，再由学校管理员处理。" : "学生门户提交的申请进入本队列；分派独立教师复核，答复或转入重评，再公布结果。"}</p></div></section>
    {/* 新旧记录使用不同证据与裁决模型，保留历史入口，不能把旧记录当作门户申请计数。 */}
    <Tabs activeKey={tab} onChange={(value) => changeView(() => setTab(value))} items={[{ key: "published", label: "学生门户申请" }, { key: "legacy", label: "历史申诉记录" }]} />
    {tab === "published" ? <>
      {props.canReadExams && !props.initialExamId ? <Select aria-label="筛选申请考试" value={examId} options={[{ value: "", label: "全部考试" }, ...exams.map((exam) => ({ value: exam.id, label: exam.name }))]} onChange={(value) => changeView(() => setExamId(value))} style={{ minWidth: 240 }} /> : null}
      {error ? <Alert type="warning" showIcon message={error} /> : null}
      <QuestionAppealWorkspace key={`${props.currentUser.tenant}:${props.currentUser.id}:${examId}`} examId={examId} canManage={props.canManage} canWork={props.canWork} canRead={props.canReadQuestionAppeals ?? (props.canManage || props.canWork)} canManageScores={props.canManageScores} canReadIdentities={props.canReadIdentities} examLabels={Object.fromEntries(exams.map((exam) => [exam.id, exam.name]))} currentUserId={props.currentUser.id} personalScope={props.mode === "teacher"} onNavigate={props.onNavigate} onEditorStateChange={setEditor} />
      {!(props.canReadQuestionAppeals ?? (props.canManage || props.canWork)) ? <Alert type="info" showIcon message="当前账号尚未开通学生门户复核处理权限" description="请联系学校管理员开通复核管理或复核工作权限；原有申诉可在历史申诉记录中查看。" /> : null}
    </> : <Suspense fallback={<p>正在加载历史记录…</p>}><Alert type="info" showIcon message="历史申诉记录" description="此处保留原有业务记录和处理方式。学生门户新提交的题目复核申请请返回学生门户申请页签。" /><LegacyAppealCenterPage {...props} onEditorStateChange={setEditor} /></Suspense>}
  </div>;
}
