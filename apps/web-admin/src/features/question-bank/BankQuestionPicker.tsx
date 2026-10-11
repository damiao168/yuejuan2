import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, App, Button, Empty, Input, Modal, Select, Space } from "antd";
import type { QuestionBankSearchItem, QuestionBankVersion, SubjectCode } from "@edugrade/sdk";
import { questionBankApi } from "../../api/questionBank";
import { getExam } from "../../api/exams";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { ResponsiveTable } from "../../components/ResponsiveTable";
import { MathMarkdown } from "../../components/MathMarkdown";
import { subjects } from "./questionBankOptions";
import { gradeScopeLabel } from "./ContentFields";

type Selection = { itemId: string; code: string; version: QuestionBankVersion; number: string; order: number };

function materializationFailure(error: unknown) {
  const conflict = error instanceof ApiClientError && error.status === 409;
  // 权限或内容校验已有明确拒绝，允许修正；请求超时仍需确认原请求结果。
  const rejected = error instanceof ApiClientError && error.status >= 400 && error.status < 500 && error.status !== 408;
  return {
    rejected,
    fallback: conflict ? "考试内容已变化，请核对题号后重试。" : "复制结果暂未确认，请继续确认原选题清单。",
  };
}

export function BankQuestionPicker({ examId, nextOrder, onCopied, scopeKey }: Readonly<{
  examId: string; nextOrder: number; onCopied: () => Promise<void>; scopeKey: string;
}>) {
  const { message } = App.useApp();
  const [open, setOpen] = useState(false);
  const [bankId, setBankId] = useState("");
  const [subject, setSubject] = useState<SubjectCode>();
  const [offset, setOffset] = useState(0);
  const [bankOffset, setBankOffset] = useState(0);
  const [query, setQuery] = useState("");
  const [preview, setPreview] = useState<QuestionBankSearchItem>();
  const [previewVersion, setPreviewVersion] = useState<QuestionBankVersion>();
  const [selections, setSelections] = useState<Selection[]>([]);
  const [saving, setSaving] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const busy = useRef(false);
  const active = useRef(true);
  // 等待考试修订号时离开，不再发起旧考试写入；已发请求只忽略迟到的界面回填。
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);
  const pending = useRef<{ fingerprint: string; key: string; revision: number } | null>(null);
  const banks = useQuery({
    queryKey: ["bank-picker", scopeKey, examId, "banks", bankOffset],
    queryFn: ({ signal }) => questionBankApi.listQuestionBanks({ query: { limit: 20, offset: bankOffset }, signal }), enabled: open,
  });
  const items = useQuery({
    queryKey: ["bank-picker", scopeKey, examId, bankId, query, subject, offset],
    // 服务端在完整授权范围筛选当前发布版，不能把当前页过滤误称为全题库搜索。
    queryFn: ({ signal }) => questionBankApi.searchQuestionBankItems({ query: {
      bank_id: bankId, q: query, subject_code: subject, mode: "published", sort: "item_code_asc", limit: 20, offset,
    }, signal }), enabled: open && Boolean(bankId),
  });
  const versions = useQuery({
    queryKey: ["bank-picker", scopeKey, examId, "versions", preview?.item.id],
    queryFn: ({ signal }) => questionBankApi.listQuestionBankVersions({ path: { itemId: preview?.item.id ?? "" }, query: { limit: 100 }, signal }),
    enabled: open && Boolean(preview),
  });
  const locked = saving || uncertain;
  function add(row: QuestionBankSearchItem, version = row.version) {
    if (locked || selections.some(entry => entry.itemId === row.item.id)) return;
    if (selections.length >= 100) { message.warning("一次最多复制100道题，请先完成当前清单。"); return; }
    setSelections(current => current.some(entry => entry.itemId === row.item.id) || current.length >= 100 ? current : [...current, { itemId: row.item.id, code: row.item.item_code, version, number: String(nextOrder + current.length), order: nextOrder + current.length }]);
  }
  function showPreview(row: QuestionBankSearchItem) { setPreview(row); setPreviewVersion(row.version); }
  const numbers = selections.map(entry => entry.number.trim());
  const invalid = numbers.some(number => !number) || new Set(numbers).size !== numbers.length;
  async function copy() {
    if (busy.current || !selections.length || invalid) return;
    busy.current = true; setSaving(true);
    let copied = false;
    try {
      const chosen = selections.map(entry => ({ version_id: entry.version.id, question_no: entry.number.trim(), sort_order: entry.order }));
      const fingerprint = JSON.stringify({ examId, chosen });
      // 响应未知时冻结选题清单、修订号及请求键；重试完成原请求，不再复制一份。
      if (pending.current?.fingerprint !== fingerprint) {
        const latest = await getExam(examId);
        if (!active.current) return;
        pending.current = { fingerprint, key: crypto.randomUUID(), revision: latest.exam.revision };
      }
      await questionBankApi.materializeQuestionBankVersions({ path: { examId }, body: { expected_revision: pending.current.revision, selections: chosen }, headers: { "Idempotency-Key": pending.current.key } });
      if (!active.current) return;
      copied = true; pending.current = null; setUncertain(false); setOpen(false); setSelections([]);
      message.success(`已复制 ${chosen.length} 道题及评分标准，请核对题号、分值，再配置答题区域。`);
      await onCopied();
    } catch (error) {
      if (!active.current) return;
      if (copied) {
        message.warning("题目已复制，页面刷新暂未完成；请刷新考试题目，不要再次复制。");
        return;
      }
      const { rejected, fallback } = materializationFailure(error);
      if (rejected) pending.current = null;
      setUncertain(!rejected && Boolean(pending.current));
      message.error(getUserErrorMessage(error, fallback));
    } finally { busy.current = false; if (active.current) setSaving(false); }
  }
  return <>
    <Button onClick={() => setOpen(true)}>从题库选题</Button>
    <Modal title="从题库选题并核对试卷" open={open} onCancel={() => !saving && setOpen(false)} width={980} style={{ top: 24 }} styles={{ body: { maxHeight: "calc(100dvh - 220px)", overflowY: "auto" } }} destroyOnHidden footer={<Button type="primary" disabled={!selections.length || invalid || saving} loading={saving} onClick={() => void copy()}>{uncertain ? "继续确认原选题结果" : `复制 ${selections.length} 道题到考试`}</Button>}>
      <Alert type="info" showIcon message="默认选用当前发布版" description="先按学科和题干查找，再添加到选题清单。复制会保存题干、答案和评分标准，题库后续更新不会改变本场考试。" />
      {uncertain ? <Alert type="warning" showIcon message="上次复制结果暂未确认，选题清单已保留" description="请继续确认原选题结果。完成前不能修改清单，避免重复复制。" /> : null}
      <Space wrap style={{ margin: "16px 0" }}>
        <Select aria-label="选题题库" placeholder="选择题库" value={bankId || undefined} style={{ minWidth: 200 }} options={banks.data?.banks.filter(bank => bank.status === "active").map(bank => ({ value: bank.id, label: bank.name }))} onChange={id => { setBankId(id); setOffset(0); setPreview(undefined); setPreviewVersion(undefined); }} />
        <Button disabled={bankOffset === 0} onClick={() => setBankOffset(Math.max(0, bankOffset - 20))}>上一页题库</Button>
        <Button disabled={bankOffset + 20 >= (banks.data?.total ?? 0)} onClick={() => setBankOffset(bankOffset + 20)}>下一页题库</Button>
        <Select aria-label="选题学科" placeholder="全部学科" allowClear options={subjects} value={subject} onChange={value => { setSubject(value); setOffset(0); }} />
        <Input.Search placeholder="按题干或题目编号搜索" onSearch={value => { setQuery(value); setOffset(0); }} />
      </Space>
      {banks.isError || items.isError ? <Alert type="error" message="题库加载失败，已选题目仍保留" action={<Button onClick={() => { void banks.refetch(); void items.refetch(); }}>重新加载题库</Button>} /> : null}
      {!bankId ? <Empty description="选择一个已授权题库，查看可以用于考试的发布题目" /> : <ResponsiveTable<QuestionBankSearchItem> className="dense-data-table" rowKey={row => row.item.id} loading={items.isLoading} dataSource={items.data?.items ?? []} columns={[
        { title: "编号", render: (_, row) => row.item.item_code },
        { title: "题干", render: (_, row) => row.version.stem, ellipsis: true },
        { title: "学科 / 年级", render: (_, row) => `${subjects.find(subject => subject.value === row.version.metadata.subject_code)?.label ?? "未登记学科"} · ${gradeScopeLabel(row.version.metadata.grade_scope)}` },
        { title: "当前发布版", render: (_, row) => `第 ${row.version.version_no} 版 · ${row.version.default_score} 分` },
        { title: "选题", render: (_, row) => <Space><Button disabled={locked || selections.some(entry => entry.itemId === row.item.id)} onClick={() => add(row)}>{selections.some(entry => entry.itemId === row.item.id) ? "已加入" : "加入选题"}</Button><Button onClick={() => showPreview(row)}>预览与历史版本</Button></Space> },
      ]} pagination={{ current: offset / 20 + 1, pageSize: 20, total: items.data?.total, showSizeChanger: false, onChange: page => setOffset((page - 1) * 20) }} />}
      {preview && previewVersion ? <section className="question-bank-preview">
        <h3>{preview.item.item_code} · 第 {previewVersion.version_no} 版</h3>
        <MathMarkdown promoteInlineMath={false}>{previewVersion.stem}</MathMarkdown>
        {previewVersion.options.map((option, index) => <MathMarkdown key={`${index}:${option}`} promoteInlineMath={false}>{`${String.fromCodePoint(65 + index)}. ${option}`}</MathMarkdown>)}
        <p>{previewVersion.default_score} 分 · {previewVersion.scoring.rubric?.points.length ?? 0} 个评分点</p>
        <details><summary>选择历史发布版本</summary><p>通常使用当前发布版；只有需要原有评分依据时才改选历史版。</p>
          {versions.isError ? <Alert type="error" message="历史版本暂不可读取，当前发布版仍可选用" action={<Button onClick={() => void versions.refetch()}>重试历史版本</Button>} /> : <Select aria-label="选用发布版本" value={previewVersion.id} loading={versions.isLoading} disabled={locked} options={(versions.data?.versions ?? [preview.version]).filter(version => version.workflow_status === "published").map(version => ({ value: version.id, label: `第 ${version.version_no} 版 · ${version.default_score} 分${version.id === preview.item.current_published_version_id ? " · 当前发布" : " · 历史版"}` }))} onChange={id => setPreviewVersion(versions.data?.versions.find(version => version.id === id) ?? preview.version)} />}
        </details>
        <Button disabled={locked || selections.some(entry => entry.itemId === preview.item.id)} onClick={() => add(preview, previewVersion)}>加入预览的版本</Button>
      </section> : null}
      <section aria-label="选题清单">
        <h3>选题清单 · {selections.length} 道题 · 总分 {selections.reduce((sum, entry) => sum + entry.version.default_score, 0)} 分</h3>
        <p>按添加顺序排列；复制前核对考试题号，不能留空或重复。自动题号可按实际试卷修改。</p>
        {invalid ? <Alert type="warning" message="请补齐题号并消除重复，再复制到考试" /> : null}
        {selections.map(entry => <div className="history-import-row" key={entry.itemId}><strong>{entry.code} · 第 {entry.version.version_no} 版 · {entry.version.default_score} 分</strong><label>考试题号 <Input aria-label={`${entry.code}考试题号`} maxLength={32} value={entry.number} disabled={locked} onChange={event => setSelections(current => current.map(item => item.itemId === entry.itemId ? { ...item, number: event.target.value } : item))} /></label><Button disabled={locked} onClick={() => setSelections(current => current.filter(item => item.itemId !== entry.itemId).map((item, index) => ({ ...item, order: nextOrder + index })))}>移出清单</Button></div>)}
      </section>
    </Modal>
  </>;
}
