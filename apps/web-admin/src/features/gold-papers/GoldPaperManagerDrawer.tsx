import { primitiveDisplayValue } from "../../utils/displayValue";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Descriptions, Drawer, Empty, Input, InputNumber, List, Modal, Popconfirm, Select, Space, Tag } from "antd";
import type { GoldPaper, GoldPaperVersion } from "@edugrade/sdk";
import { approveGoldPaperVersion, createGoldPaperVersion, getGoldCoverage, listGoldPapers, retireGoldPaper } from "../../api/goldPapers";
import { apiClient, getUserErrorMessage } from "../../api/client";
import { MathMarkdown } from "../../components/MathMarkdown";
import { goldCoverageGapLabel, rubricPointsFromSnapshot } from "./goldPaperPresentation";

const statusLabels = { pending_approval: "待审批", active: "已启用", retired: "已退役" } as const;

function GoldPaperDetails({ selected, latest, coverageGaps, acting, onApprove, onCreateVersion, onRetire }: Readonly<{
  selected?: GoldPaper;
  latest?: GoldPaperVersion;
  coverageGaps: string[];
  acting: boolean;
  onApprove: (paper: GoldPaper, version: GoldPaperVersion) => void;
  onCreateVersion: () => void;
  onRetire: () => void;
}>) {
  if (!selected || !latest) return <Empty description="选择一份标准卷查看审批证据" />;
  const rubricPoints = rubricPointsFromSnapshot(latest.rubric_snapshot);
  // 审批入口按最新提名版本判断；已批准内容通过新版本调整，退役记录不再展示变更入口。
  return (
    <>
      {coverageGaps.length ? <Alert type="warning" showIcon message="标准卷覆盖仍有缺口" description={coverageGaps.map(goldCoverageGapLabel).join("；")} /> : <Alert type="success" showIcon message="当前题目标准卷覆盖规则已满足" />}
      {selected.answer_image_url ? <img className="gold-paper-answer" src={apiClient.url(selected.answer_image_url)} alt="标准卷原始答题区域" /> : <Alert type="warning" message="当前记录未提供答题图像地址" />}
      <Descriptions size="small" bordered column={2}>
        <Descriptions.Item label="状态"><Tag>{statusLabels[selected.status]}</Tag></Descriptions.Item>
        <Descriptions.Item label="风险级别">{selected.risk_tier}</Descriptions.Item>
        <Descriptions.Item label="版本">v{latest.version}{latest.approved_at ? " · 已批准" : " · 待批准"}</Descriptions.Item>
        <Descriptions.Item label="拟定分">{latest.reference_score} / {latest.max_score}</Descriptions.Item>
        <Descriptions.Item label="解释" span={2}><MathMarkdown promoteInlineMath={false}>{latest.explanation}</MathMarkdown></Descriptions.Item>
      </Descriptions>
      <section>
        <h3>冻结 Rubric 与采分点证据</h3>
        <List size="small" bordered dataSource={rubricPoints} locale={{ emptyText: "冻结 Rubric 未定义采分点" }} renderItem={(point) => <List.Item extra={<Tag>{primitiveDisplayValue(latest.trait_scores[point.id], "未标注")} / {point.score}</Tag>}><MathMarkdown promoteInlineMath={false}>{point.description}</MathMarkdown></List.Item>} />
      </section>
      <Space wrap>
        {!latest.approved_at && selected.status !== "retired" ? <Popconfirm title={`批准 v${latest.version} 并设为活动版本？`} description="批准后版本内容不可原地修改。" onConfirm={() => onApprove(selected, latest)}><Button type="primary" loading={acting}>批准版本</Button></Popconfirm> : null}
        {latest.approved_at && selected.status !== "retired" ? <Button onClick={onCreateVersion}>创建新版本</Button> : null}
        {selected.status !== "retired" ? <Button danger onClick={onRetire}>退役</Button> : null}
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
  const [coverageGaps, setCoverageGaps] = useState<string[]>([]);
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
  useEffect(() => {
    if (!open || !selected) { setCoverageGaps([]); return; }
    let active = true;
    getGoldCoverage(selected.exam_id, selected.question_id)
      .then((response) => { if (active) setCoverageGaps(response.coverage.gaps); })
      .catch(() => { if (active) setCoverageGaps([]); });
    return () => { active = false; };
  }, [open, selected]);

  const run = async (action: () => Promise<unknown>, success: string) => {
    setActing(true);
    try { await action(); message.success(success); await load(); }
    catch (error) { message.error(getUserErrorMessage(error, "标准卷操作失败")); }
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
    await run(() => createGoldPaperVersion(selected.id, {
      reference_score: score,
      explanation: explanation.trim(),
      trait_scores: latest.trait_scores,
      error_tags: errorTags,
      source_grade_ids: latest.source_grade_ids
    }), "新版本已创建，等待审批");
    setVersionOpen(false);
  };

  return (
    <>
      <Drawer title="标准卷管理" width={920} open={open} onClose={onClose} extra={<Button loading={loading} onClick={() => void load()}>刷新</Button>}>
        <div className="gold-paper-manager">
          <aside>
            <List
              loading={loading}
              dataSource={items}
              locale={{ emptyText: <Empty description="暂无标准卷提名" /> }}
              renderItem={(item) => (
                <List.Item className={item.id === selected?.id ? "selected" : ""} onClick={() => setSelectedId(item.id)}>
                  <List.Item.Meta title={`题目 ${item.question_id.slice(0, 8)}`} description={<Space><Tag>{statusLabels[item.status]}</Tag><span>v{item.versions.length}</span></Space>} />
                </List.Item>
              )}
            />
          </aside>
          <main>
            <GoldPaperDetails selected={selected} latest={latest} coverageGaps={coverageGaps} acting={acting}
              onApprove={(paper, version) => void run(() => approveGoldPaperVersion(paper.id, version.version), "标准卷版本已批准")}
              onCreateVersion={openVersion} onRetire={() => setRetireOpen(true)} />
          </main>
        </div>
      </Drawer>
      <Modal title="创建标准卷新版本" open={versionOpen} confirmLoading={acting} onOk={() => void createVersion()} onCancel={() => setVersionOpen(false)}>
        <Space direction="vertical" style={{ width: "100%" }}>
          <InputNumber min={0} max={latest?.max_score} value={score} onChange={(value) => setScore(value === null ? null : Number(value))} suffix={`/ ${latest?.max_score ?? "-"}`} />
          <Input.TextArea rows={4} value={explanation} onChange={(event) => setExplanation(event.target.value)} placeholder="说明相对上一版本调整了哪些评分边界或证据" />
          <Select mode="tags" value={errorTags} onChange={setErrorTags} tokenSeparators={[","]} placeholder="典型错误标签" />
        </Space>
      </Modal>
      <Modal title="退役标准卷" open={retireOpen} confirmLoading={acting} okButtonProps={{ danger: true }} onOk={() => selected && retirementReason.trim() && void run(() => retireGoldPaper(selected.id, retirementReason.trim()), "标准卷已退役").then(() => { setRetireOpen(false); setRetirementReason(""); })} onCancel={() => setRetireOpen(false)}>
        <Input.TextArea rows={3} value={retirementReason} onChange={(event) => setRetirementReason(event.target.value)} placeholder="说明退役原因，便于审计追溯" />
      </Modal>
    </>
  );
}
