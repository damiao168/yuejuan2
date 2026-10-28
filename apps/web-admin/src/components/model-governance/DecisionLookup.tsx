import { useEffect, useRef, useState } from "react";
import { Alert, Button, Descriptions, Divider, Drawer, Empty, Input, Space, Tag } from "antd";
import { getAIEligibilityDecision, listAIEligibilityDecisions, type AIEligibilityDecision, type AIEligibilityDecisionSummary } from "../../api/scoringAssurance";
import { getSafeUserText, getUserErrorMessage } from "../../api/client";
import { scoringModeOptions } from "./assuranceOptions";
import { eligibilityArchetypes, eligibilityRisks, eligibilityStages, eligibilitySubjects } from "./eligibilityScope";

function decisionLabel(item: AIEligibilityDecisionSummary) {
  const time = new Date(item.created_at).toLocaleString("zh-CN");
  const mode = scoringModeOptions.find(option => option.value === item.decision)?.label ?? "未知方式";
  const subject = eligibilitySubjects.find(option => option.value === item.subject_code)?.label ?? "未标学科";
  const stage = eligibilityStages.find(option => option.value === item.education_stage)?.label ?? "未标学段";
  const archetype = eligibilityArchetypes.find(option => option.value === item.archetype_code)?.label ?? "未标题型";
  const risk = eligibilityRisks.find(option => option.value === item.risk_tier)?.label ?? "未标风险";
  return `${subject} · ${stage} · ${archetype} · ${risk} · ${time} · ${mode}`;
}

export function DecisionLookup({ canRead, tenantID }: Readonly<{ canRead: boolean; tenantID: string }>) {
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<AIEligibilityDecisionSummary[]>([]);
  const [query, setQuery] = useState("");
  const [appliedQuery, setAppliedQuery] = useState("");
  const [cursor, setCursor] = useState("");
  const [runItemID, setRunItemID] = useState("");
  const [loading, setLoading] = useState(false);
  const [reading, setReading] = useState(false);
  const [error, setError] = useState("");
  const [decision, setDecision] = useState<AIEligibilityDecision>();
  const directoryRequest = useRef(0), detailRequest = useRef(0);
  const scope = useRef(tenantID); scope.current = tenantID;
  // biome-ignore lint/correctness/useExhaustiveDependencies: 切校或撤销读取职责时必须清空目录和废弃旧请求，不能因回调未直接使用身份而删掉边界依赖。
  useEffect(() => {
    directoryRequest.current++; detailRequest.current++;
    setItems([]); setCursor(""); setDecision(undefined); setRunItemID(""); setOpen(false); setError(""); setLoading(false); setReading(false);
    return () => { directoryRequest.current++; detailRequest.current++; };
  }, [tenantID, canRead]);

  const loadDirectory = async (append = false) => {
    if (!canRead || !tenantID) return;
    const request = ++directoryRequest.current, expectedTenant = tenantID;
    setLoading(true); setError("");
    const q = append ? appliedQuery : query.trim();
    try {
      const result = await listAIEligibilityDecisions(expectedTenant, { q, cursor: append ? cursor : undefined });
      // 切校、重查或关闭目录后，旧响应不能重开目录或覆盖当前选择。
      if (request !== directoryRequest.current || scope.current !== expectedTenant) return;
      if (result.decisions.some(item => item.tenant_id !== expectedTenant)) throw new Error("返回记录与当前学校不一致，请重新选择学校");
      setItems(current => append ? [...current, ...result.decisions.filter(item => !current.some(old => old.id === item.id))] : result.decisions);
      setAppliedQuery(q); setCursor(result.has_more ? result.next_cursor : "");
    } catch (reason) {
      if (request === directoryRequest.current) setError(getUserErrorMessage(reason, "读取运行目录失败"));
    } finally { if (request === directoryRequest.current) setLoading(false); }
  };
  const readDecision = async (id: string) => {
    if (!canRead || !id.trim()) return;
    const request = ++detailRequest.current, expectedTenant = tenantID, expectedRun = id.trim();
    setReading(true); setError(""); setDecision(undefined);
    try {
      const result = await getAIEligibilityDecision(expectedRun, expectedTenant);
      if (request !== detailRequest.current || scope.current !== expectedTenant) return;
      if (result.decision.tenant_id !== expectedTenant || result.decision.run_item_id !== expectedRun) throw new Error("返回决策与当前学校或运行不一致，请重新选择");
      setDecision(result.decision); setRunItemID(expectedRun); setOpen(false);
    } catch (reason) {
      if (request === detailRequest.current) setError(getUserErrorMessage(reason, "读取准入决策失败"));
    } finally { if (request === detailRequest.current) setReading(false); }
  };
  if (!canRead) return null;
  return <section className="model-assurance-decision">
    <Divider orientation="left">运行准入审计</Divider>
    <Button onClick={() => { setOpen(true); void loadDirectory(); }}>选择评分运行</Button>
    <details><summary>按运行编号查找</summary><Space.Compact style={{ maxWidth: 560, width: "100%" }}><Input value={runItemID} onChange={event => setRunItemID(event.target.value)} placeholder="输入 AI 评分运行项编号" /><Button loading={reading} onClick={() => void readDecision(runItemID)} disabled={!runItemID.trim()}>查看决策</Button></Space.Compact></details>
    {error ? <Alert type="error" showIcon message={error} /> : null}
    {decision ? <Descriptions size="small" column={2} style={{ marginTop: 12 }} items={[{ key: "decision", label: "评分方式", children: scoringModeOptions.find(option => option.value === decision.decision)?.label ?? "未知方式" }, { key: "external", label: "外部调用", children: decision.external_ai_allowed ? <Tag color="success">允许</Tag> : <Tag color="warning">禁止</Tag> }, { key: "reason", label: "原因", span: 2, children: decision.reasons.map(item => getSafeUserText(item.message, "运行条件未满足")).join("；") || "无额外阻断原因" }]} /> : null}
    <Drawer title="选择评分运行" open={open} width={700} onClose={() => { setOpen(false); directoryRequest.current++; detailRequest.current++; setLoading(false); setReading(false); }}>
      <Space.Compact block><Input value={query} maxLength={128} aria-label="搜索评分运行" placeholder="搜索学科、题型或运行编号" onChange={event => setQuery(event.target.value)} onPressEnter={() => void loadDirectory()} /><Button loading={loading} onClick={() => void loadDirectory()}>搜索</Button></Space.Compact>
      {error ? <Alert type="error" showIcon message={error} /> : null}
      <Space direction="vertical" style={{ width: "100%", marginTop: 16 }}>
        {items.map(item => <Button key={item.id} loading={reading} onClick={() => void readDecision(item.run_item_id)} style={{ whiteSpace: "normal", height: "auto", textAlign: "left" }}>{decisionLabel(item)}</Button>)}
        {!loading && !items.length ? <Empty description="当前学校没有匹配的运行决策" /> : null}
        {cursor ? <Button loading={loading} onClick={() => void loadDirectory(true)}>加载更多运行</Button> : null}
      </Space>
    </Drawer>
  </section>;
}
