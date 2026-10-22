import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, InputNumber, List, Modal, Space } from "antd";
import { cleanupArchivedOriginals, getDurableStorageUsage, hasDurableDesktopStore, listArchivedCleanupCandidates, type ArchivedCleanupCandidate, type DurableStorageUsage } from "../lib/durableStore";
import { formatBytes, formatDate } from "../lib/scanFiles";
import { getUserErrorMessage } from "../api/userError";

export function NativeStoragePanel({ scope }: Readonly<{ scope: string }>) {
  const [usage, setUsage] = useState<DurableStorageUsage | null>(null);
  const [retainDays, setRetainDays] = useState<number | null>(null);
  const [candidates, setCandidates] = useState<ArchivedCleanupCandidate[] | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const scopeRef = useRef(scope);
  scopeRef.current = scope;
  const operation = useRef(0);
  const inFlight = useRef(false);

  const refresh = useCallback(async () => {
    const request = ++operation.current;
    if (!scope || !hasDurableDesktopStore()) return;
    try {
      const next = await getDurableStorageUsage(scope);
      if (scopeRef.current === scope && request === operation.current) setUsage(next);
    } catch (error) {
      if (scopeRef.current === scope && request === operation.current) setMessage(getUserErrorMessage(error, "本机磁盘统计读取失败，请重新登录或刷新。"));
    }
  }, [scope]);
  useEffect(() => {
    setUsage(null); setRetainDays(null); setCandidates(null); setMessage(null); setBusy(false); inFlight.current = false;
    void refresh();
  }, [refresh]);

  const previewCleanup = async () => {
    if (!scope || retainDays === null || !Number.isInteger(retainDays) || retainDays < 1 || retainDays > 3650 || inFlight.current) return;
    const owner = scope; const days = retainDays;
    inFlight.current = true; setBusy(true); setMessage(null); setCandidates(null);
    try {
      const next = await listArchivedCleanupCandidates(days, owner);
      if (scopeRef.current !== owner) return;
      setCandidates(next);
      if (!next.length) setMessage("没有符合条件的已确认归档原件。未上传、未确认和未到保留天数的原件继续留存。");
    } catch (error) {
      if (scopeRef.current === owner) setMessage(getUserErrorMessage(error, "清理候选读取失败，原件未删除。"));
    } finally {
      if (scopeRef.current === owner) { inFlight.current = false; setBusy(false); }
    }
  };

  const confirmCleanup = async () => {
    if (!scope || !candidates?.length || retainDays === null || inFlight.current) return;
    const owner = scope; const days = retainDays; const selected = candidates.slice(0, 500);
    inFlight.current = true; setBusy(true);
    const accepted = await new Promise<boolean>((resolve) => Modal.confirm({
      title: `永久删除 ${selected.length} 份已确认归档原件？`,
      content: <><p>学校要求保留 {days} 天。仅删除本机加密原件，上传回执仍保留。删除后不能从本机恢复；请先确认学校的服务端备份和保留要求。</p><div style={{ maxHeight: 260, overflowY: "auto" }}><List size="small" dataSource={selected} renderItem={(item) => <List.Item>{item.filename} · {formatDate(item.confirmedAt)} · {formatBytes(item.sizeBytes)}</List.Item>} /></div></>,
      okText: "确认永久删除本机原件", okButtonProps: { danger: true }, cancelText: "保留原件",
      onOk: () => resolve(true), onCancel: () => resolve(false)
    }));
    try {
      // 弹窗跨账号时不执行旧选择；原生层仍在当前租约下重新检查回执、期限和受控路径。
      if (!accepted || scopeRef.current !== owner) return;
      const result = await cleanupArchivedOriginals(days, selected.map((item) => item.id), owner);
      if (scopeRef.current !== owner) return;
      setCandidates(null);
      const cleanupMessage = result.failures.length
        ? `有 ${result.failures.length} 份未清理完成，请刷新后重试：${result.failures.join("；")}`
        : "上传回执继续保留，未确认原件未清理。";
      setMessage(`已删除 ${result.removed} 份本机原件。${cleanupMessage}`);
      await refresh();
    } catch (error) {
      if (scopeRef.current === owner) setMessage(getUserErrorMessage(error, "清理未完成，请刷新核对剩余原件后重试。"));
    } finally {
      if (scopeRef.current === owner) { inFlight.current = false; setBusy(false); }
    }
  };

  if (!hasDurableDesktopStore()) return <p>浏览器开发模式不统计原生扫描磁盘，也不提供生产原件清理。</p>;
  if (!scope) return <p>在线登录或解锁本账号的有效本机授权后，可查看该账号原件占用。</p>;
  return <div className="native-storage-panel">
    <p>原件持续留存在本机；归档只隐藏队列，不释放磁盘。当前没有自动清理期限。</p>
    {usage ? <div className="diagnostic-summary-grid">
      <div><span>本账号加密原件</span><strong>{formatBytes(usage.spoolBytes)}</strong><small>{usage.assetCount} 份，包括未完成写入占用</small></div>
      <div><span>本账号数据库及日志</span><strong>{formatBytes(usage.databaseBytes)}</strong><small>SQLite、WAL 和共享内存文件</small></div>
      <div><span>原件所在磁盘可用</span><strong>{usage.freeBytes === null ? "无法读取" : formatBytes(usage.freeBytes)}</strong><small>当前 Windows 用户可用容量</small></div>
    </div> : <p>真实磁盘用量尚未读取。</p>}
    <Space wrap>
      <Button disabled={busy} onClick={() => void refresh()}>刷新磁盘用量</Button>
      <label>学校要求保留天数 <InputNumber aria-label="学校要求保留天数" min={1} max={3650} precision={0} disabled={busy} value={retainDays} placeholder="按学校要求填写" onChange={(value) => { setRetainDays(value); setCandidates(null); }} /></label>
      <Button loading={busy} disabled={retainDays === null || busy} onClick={() => void previewCleanup()}>检查可清理的已确认归档原件</Button>
      <Button danger disabled={busy || !candidates?.length} onClick={() => void confirmCleanup()}>核对后清理 {Math.min(candidates?.length ?? 0, 500)} 份</Button>
    </Space>
    {candidates?.length ? <p>符合条件 {candidates.length} 份，每次最多清理 500 份。执行前会列出原件名称、确认日期和大小。</p> : null}
    {message && <Alert className="section-alert" type="info" showIcon message={message} />}
  </div>;
}
