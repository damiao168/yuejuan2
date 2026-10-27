import { primitiveDisplayValue } from "../../utils/displayValue";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Descriptions, Drawer, Empty, Input, InputNumber, List, Modal, Popconfirm, Select, Space, Tag } from "antd";
import type { GoldPaper, GoldPaperVersion } from "@edugrade/sdk";
import { approveGoldPaperVersion, createGoldPaperVersion, getGoldCoverage, listGoldPapers, retireGoldPaper } from "../../api/goldPapers";
import { apiClient, getUserErrorMessage } from "../../api/client";
import { MathMarkdown } from "../../components/MathMarkdown";
import { goldCoverageGapLabel, rubricPointsFromSnapshot } from "./goldPaperPresentation";

const statusLabels = { pending_approval: "待审批", active: "已启用", retired: "已退役" } as const;
interface CoverageState { scope: string; phase: "loading" | "ready" | "error"; gaps: string[]; ready: boolean }

function GoldPaperDetails({ selected, latest, coverage, onRetryCoverage, acting, onApprove, onCreateVersion, onRetire }: Readonly<{
  selected?: GoldPaper;
  latest?: GoldPaperVersion;
  coverage?: CoverageState;
  onRetryCoverage: () => void;
  acting: boolean;
  onApprove: (paper: GoldPaper, version: GoldPaperVersion) => void;
  onCreateVersion: () => void;
  onRetire: () => void;
}>) {
  if (!selected || !latest) return <Empty description="选择一份标准卷查看审批证据" />;
  const rubricPoints = rubricPointsFromSnapshot(latest.rubric_snapshot);
  const answerImageURL = latest.answer_image_url ?? (latest.version === selected.active_version ? selected.answer_image_url : undefined);
  // 审批入口按最新提名版本判断；已批准内容通过新版本调整，退役记录不再展示变更入口。
  return (
    <>
      {!coverage || coverage.phase === "loading" ? <Alert type="info" message="正在读取标准卷覆盖状态" /> : coverage.phase === "error"
        ? <Alert type="warning" showIcon message="标准卷覆盖暂不可判断" description="未能取得有效覆盖结果，请重试。" action={<Button onClick={onRetryCoverage}>重试覆盖查询</Button>} />
        : coverage.gaps.length ? <Alert type="warning" showIcon message="标准卷覆盖仍有缺口" description={coverage.gaps.map(goldCoverageGapLabel).join("；")} />
        : coverage.ready ? <Alert type="success" showIcon message="当前题目标准卷覆盖规则已满足" /> : <Alert type="warning" message="标准卷覆盖尚未达标，请核对标准卷配置" />}
      {answerImageURL ? <img className="gold-paper-answer" src={apiClient.url(answerImageURL)} alt="标准卷冻结答题区域" /> : <Alert type="warning" message="此版本缺少可验证的冻结答题图" description="历史审批记录仍保留，请创建新版本并重新审批后使用。" />}
      <Descriptions size="small" bordered column={2}>
        <Descriptions.Item label="状态"><Tag>{statusLabels[selected.status]}</Tag></Descriptions.Item>
        <Descriptions.Item label="风险级别">{selected.risk_tier}</Descriptions.Item>
        <Descriptions.Item label="版本">v{latest.version}{latest.approved_at ? " · 已批准" : latest.image_invalidated_at ? " · 图像身份已失效" : " · 待批准"}</Descriptions.Item>
        <Descriptions.Item label="拟定分">{latest.reference_score} / {latest.max_score}</Descriptions.Item>
        <Descriptions.Item label="解释" span={2}><MathMarkdown promoteInlineMath={false}>{latest.explanation}</MathMarkdown></Descriptions.Item>
      </Descriptions>
      <section>
        <h3>冻结 Rubric 与采分点证据</h3>
        <List size="small" bordered dataSource={rubricPoints} locale={{ emptyText: "冻结 Rubric 未定义采分点" }} renderItem={(point) => <List.Item extra={<Tag>{primitiveDisplayValue(latest.trait_scores[point.id], "未标注")} / {point.score}</Tag>}><MathMarkdown promoteInlineMath={false}>{point.description}</MathMarkdown></List.Item>} />
      </section>
      <Space wrap>
        {!latest.approved_at && !latest.image_invalidated_at && selected.status !== "retired" ? <Popconfirm title={`批准 v${latest.version} 并设为活动版本？`} description="批准后版本内容不可原地修改。" onConfirm={() => onApprove(selected, latest)}><Button type="primary" loading={acting}>批准版本</Button></Popconfirm> : null}
        {(latest.approved_at || latest.image_invalidated_at) && selected.status !== "retired" ? <Button disabled={acting} onClick={onCreateVersion}>创建新版本</Button> : null}
        {selected.status !== "retired" ? <Button disabled={acting} danger onClick={onRetire}>退役</Button> : null}
      </Space>
      <List size="small" header="版本记录（已批准版本不可变）" dataSource={[...selected.versions].reverse()} renderItem={(version: GoldPaperVersion) => <List.Item><div style={{ minWidth: 0, width: "100%" }}><Space wrap><strong>v{version.version}</strong><span>{version.reference_score} / {version.max_score}</span><Tag color={version.approved_at ? "green" : "orange"}>{version.approved_at ? "已批准" : "待批准"}</Tag></Space><MathMarkdown promoteInlineMath={false}>{version.explanation}</MathMarkdown></div></List.Item>} />
    </>
  );
}

export function GoldPaperManagerDrawer({ open, examId, questionId, onClose }: Readonly<{ open: boolean; examId?: string; questionId?: string; onClose: () => void }>) {
  const { message } = App.useApp();
  const [items, setItems] = useState<GoldPaper[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [loading, setLoading] = useState(false);
  const [acting, setActing] = useState(false);
  const [versionOpen, setVersionOpen] = useState(false);
  const [retireOpen, setRetireOpen] = useState(false);
  const [score, setScore] = useState<number | null>(null);
  const [explanation, setExplanation] = useState("");
  const [errorTags, setErrorTags] = useState<string[]>([]);
  const [retirementReason, setRetirementReason] = useState("");
  const [coverage, setCoverage] = useState<CoverageState>();
  const [coverageAttempt, setCoverageAttempt] = useState(0);
  const selected = useMemo(() => items.find((item) => item.id === selectedId) ?? items[0], [items, selectedId]);
  const latest = selected?.versions[selected.versions.length - 1];

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await listGoldPapers(examId ? { exam_id: examId, question_id: questionId } : {});
      setItems(response.gold_papers);
      setSelectedId((current) => response.gold_papers.some((item) => item.id === current) ? current : response.gold_papers[0]?.id ?? "");
    } catch (error) {
      message.error(getUserErrorMessage(error, "标准卷加载失败"));
    } finally {
      setLoading(false);
    }
  }, [examId, questionId, message]);

  useEffect(() => { if (open) void load(); }, [load, open]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 用户重试同一题覆盖查询时递增修订，范围不变也必须重新请求。
  useEffect(() => {
    if (!open || !selected) { setCoverage(undefined); return; }
    let active = true;
    const scope = JSON.stringify([selected.exam_id, selected.question_id]);
    setCoverage({ scope, phase: "loading", gaps: [], ready: false });
    getGoldCoverage(selected.exam_id, selected.question_id)
      .then((response) => {
        // 空数组表示成功取得的无缺口事实；null 或读取失败都不能伪装成规则已满足。
        if (!Array.isArray(response.coverage.gaps) || !response.coverage.gaps.every(gap => typeof gap === "string") || typeof response.coverage.ready !== "boolean") throw new Error("invalid Gold coverage response");
        if (active) setCoverage({ scope, phase: "ready", gaps: response.coverage.gaps, ready: response.coverage.ready });
      })
      .catch(() => { if (active) setCoverage({ scope, phase: "error", gaps: [], ready: false }); });
    return () => { active = false; };
  }, [open, selected, coverageAttempt]);

  const run = async (action: () => Promise<unknown>, success: string) => {
    setActing(true);
    try { await action(); message.success(success); await load(); return true; }
    catch (error) { message.error(getUserErrorMessage(error, "标准卷操作失败")); return false; }
    finally { setActing(false); }
  };

  const openVersion = () => {
    setScore(latest?.reference_score ?? null);
    setExplanation("");
    setErrorTags(latest?.error_tags ?? []);
    setVersionOpen(true);
  };

  const createVersion = async () => {
    if (!selected || !latest || score === null || !explanation.trim()) {
      message.error("请填写拟定分和版本解释"); return;
    }
    const saved = await run(() => createGoldPaperVersion(selected.id, {
      reference_score: score,
      explanation: explanation.trim(),
      trait_scores: latest.trait_scores,
      error_tags: errorTags,
      source_grade_ids: latest.source_grade_ids
    }), "新版本已创建，等待审批");
    if (saved) setVersionOpen(false);
  };

  return (
    <>
      <Drawer title="标准卷管理" width={920} open={open} onClose={() => { if (!acting) onClose(); }} closable={!acting} maskClosable={!acting} keyboard={!acting} extra={<Button disabled={acting} loading={loading} onClick={() => void load()}>刷新</Button>}>
        <div className="gold-paper-manager">
          <aside>
            <List
              loading={loading}
              dataSource={items}
              locale={{ emptyText: <Empty description="暂无标准卷提名" /> }}
              renderItem={(item) => (
                <List.Item className={item.id === selected?.id ? "selected" : ""} onClick={() => { if (!acting) setSelectedId(item.id); }}>
                  <List.Item.Meta title={`题目 ${item.question_id.slice(0, 8)}`} description={<Space><Tag>{statusLabels[item.status]}</Tag><span>v{item.versions.length}</span></Space>} />
                </List.Item>
              )}
            />
          </aside>
          <main>
            <GoldPaperDetails selected={selected} latest={latest} coverage={coverage?.scope === JSON.stringify([selected?.exam_id, selected?.question_id]) ? coverage : undefined} onRetryCoverage={() => setCoverageAttempt(value => value + 1)} acting={acting}
              onApprove={(paper, version) => void run(() => approveGoldPaperVersion(paper.id, version.version), "标准卷版本已批准")}
              onCreateVersion={openVersion} onRetire={() => setRetireOpen(true)} />
          </main>
        </div>
      </Drawer>
      <Modal title="创建标准卷新版本" open={versionOpen} confirmLoading={acting} closable={!acting} maskClosable={!acting} keyboard={!acting} onOk={() => { if (!acting) void createVersion(); }} onCancel={() => { if (!acting) setVersionOpen(false); }}>
        <Space direction="vertical" style={{ width: "100%" }}>
          <InputNumber disabled={acting} min={0} max={latest?.max_score} value={score} onChange={(value) => setScore(value === null ? null : Number(value))} suffix={`/ ${latest?.max_score ?? "-"}`} />
          <Input.TextArea disabled={acting} rows={4} value={explanation} onChange={(event) => setExplanation(event.target.value)} placeholder="说明相对上一版本调整了哪些评分边界或证据" />
          <Select disabled={acting} mode="tags" value={errorTags} onChange={setErrorTags} tokenSeparators={[","]} placeholder="典型错误标签" />
        </Space>
      </Modal>
      <Modal title="退役标准卷" open={retireOpen} confirmLoading={acting} closable={!acting} maskClosable={!acting} keyboard={!acting} okButtonProps={{ danger: true }} onOk={() => !acting && selected && retirementReason.trim() && void run(() => retireGoldPaper(selected.id, retirementReason.trim()), "标准卷已退役").then(saved => { if (saved) { setRetireOpen(false); setRetirementReason(""); } })} onCancel={() => { if (!acting) setRetireOpen(false); }}>
        <Input.TextArea disabled={acting} rows={3} value={retirementReason} onChange={(event) => setRetirementReason(event.target.value)} placeholder="说明退役原因，便于审计追溯" />
      </Modal>
    </>
  );
}
