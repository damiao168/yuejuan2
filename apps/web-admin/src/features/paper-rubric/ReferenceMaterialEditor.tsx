import { Alert, Button, Drawer, Input, InputNumber, Select, Space } from "antd";
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { getBusinessCommandSession } from "../../api/businessCommand";
import { createReferenceCrop, getReferenceCropImage, getReferenceMaterials, getReferenceSourceImage, getReferenceSources, resumeReferenceCommand, saveReferenceMaterials, withdrawReferenceMaterials,
  type ReferenceBox, type ReferenceItem, type ReferenceMaterials, type ReferenceRole, type ReferenceSource } from "../../api/referenceMaterials";
import "./reference-materials.css";

export function validReferenceSelection(items: readonly Pick<ReferenceItem, "crop_ref" | "role">[]): boolean {
  return items.length > 0 && items.length <= 4 && items.every(item => item.crop_ref && (item.role === "question_diagram" || item.role === "reference_solution")) && new Set(items.map(item => item.crop_ref)).size === items.length
    && items.filter(item => item.role === "question_diagram").length <= 2 && items.filter(item => item.role === "reference_solution").length <= 2;
}
export function validReferenceBox(box: ReferenceBox, source: Pick<ReferenceSource, "width" | "height">): boolean {
  return Object.values(box).every(Number.isSafeInteger) && box.x >= 0 && box.y >= 0 && box.width > 0 && box.height > 0
    && box.width <= source.width - box.x && box.height <= source.height - box.y && box.width * box.height <= 4_000_000;
}
export const referenceRoleLabel = (role: ReferenceRole) => role === "question_diagram" ? "题目图" : "参考解答图";
// 教师看到的顺序就是发送顺序；同一角色内部可排序，题目图始终先于参考解答。
export const orderReferenceItems = (items: readonly ReferenceItem[]) => [...items].sort((a,b) => Number(a.role === "reference_solution")-Number(b.role === "reference_solution")).map((item,index)=>({...item,ordinal:index+1}));
const subscribeSession = (notify: () => void) => { window.addEventListener("business-command-scope-changed",notify); return () => window.removeEventListener("business-command-scope-changed",notify); };
export const useReferenceSession = () => useSyncExternalStore(subscribeSession,getBusinessCommandSession,getBusinessCommandSession);

// Blob URL 归当前完整会话和资源拥有；迟到图片不能进入新账号/新题，卸载始终释放。
export function ProtectedReferenceImage({ load, identity, alt, onReadyChange }: Readonly<{ load: () => Promise<{ blob: Blob }>; identity: string; alt: string; onReadyChange?: (ready: boolean) => void }>) {
  const [image, setImage] = useState<{ url: string; identity: string; session: ReturnType<typeof getBusinessCommandSession> } | null>(null); const [error, setError] = useState("");
  const current = useRef(identity); current.current = identity;
  // identity 和会话决定资源身份；父组件重绘产生的新加载闭包不应让同一图片重复请求。
  const loadImage = useRef(load); loadImage.current = load;
  const notifyReady = useRef(onReadyChange); notifyReady.current = onReadyChange;
  const session = useReferenceSession();
  useEffect(() => { let disposed = false; let objectUrl = ""; setImage(null); setError(""); notifyReady.current?.(false);
    void loadImage.current().then(file => { if (disposed || current.current !== identity || getBusinessCommandSession() !== session) return;
      objectUrl = URL.createObjectURL(file.blob); setImage({url:objectUrl,identity,session});
    }).catch(() => { if (!disposed && current.current === identity && getBusinessCommandSession() === session) setError("图片暂不可用，请重新核对材料。"); });
    return () => { disposed = true; notifyReady.current?.(false); if (objectUrl) URL.revokeObjectURL(objectUrl); };
  }, [identity, session]);
  return error ? <p role="alert">{error}</p> : image?.identity===identity && image.session===session ? <img className="reference-protected-image" src={image.url} alt={alt}
    onLoad={() => { if (current.current === identity && getBusinessCommandSession() === session) notifyReady.current?.(true); }}
    onError={() => { if (current.current === identity && getBusinessCommandSession() === session) { notifyReady.current?.(false); setError("图片暂不可用，请重新核对材料。"); } }} /> : <p>正在读取图片…</p>;
}

function ReferenceCropSurface({ source,examId,identity,box,onBox,onReadyChange,onDraggingChange }: Readonly<{source:ReferenceSource;examId:string;identity:string;box:ReferenceBox;onBox:(box:ReferenceBox)=>void;onReadyChange:(ready:boolean)=>void;onDraggingChange:(dragging:boolean)=>void}>) {
  const start=useRef<{x:number;y:number;pointer:number}|null>(null);
  const ready=useRef(false);
  // 指针坐标按已显示图片的尺寸换算到原图像素；未解码或零尺寸时不能开始拖框。
  const point=(event:React.PointerEvent<HTMLDivElement>)=>{const image=event.currentTarget.querySelector("img");if(!image||!ready.current)return null;const bounds=image.getBoundingClientRect();if(bounds.width<=0||bounds.height<=0)return null;return {x:Math.max(0,Math.min(source.width,Math.round((event.clientX-bounds.left)/bounds.width*source.width))),y:Math.max(0,Math.min(source.height,Math.round((event.clientY-bounds.top)/bounds.height*source.height)))};};
  const stop=(event:React.PointerEvent<HTMLDivElement>)=>{if(start.current?.pointer!==event.pointerId)return;start.current=null;onDraggingChange(false);if(event.currentTarget.hasPointerCapture(event.pointerId))event.currentTarget.releasePointerCapture(event.pointerId);};
  const move=(event:React.PointerEvent<HTMLDivElement>)=>{const p=point(event),origin=start.current;if(!p||!origin||origin.pointer!==event.pointerId)return;onBox({x:Math.min(origin.x,p.x),y:Math.min(origin.y,p.y),width:Math.max(1,Math.abs(origin.x-p.x)),height:Math.max(1,Math.abs(origin.y-p.y))});};
  return <div className="reference-crop-surface" role="group" aria-label="拖动选择参考图范围，也可使用下方像素输入框"
    onPointerDown={event=>{if(event.button!==0||start.current)return;const p=point(event);if(!p)return;start.current={...p,pointer:event.pointerId};onDraggingChange(true);event.currentTarget.setPointerCapture(event.pointerId);}}
    onPointerMove={move} onPointerUp={event=>{move(event);stop(event);}} onPointerCancel={stop} onLostPointerCapture={stop}>
    <ProtectedReferenceImage identity={identity} alt="参考图来源页" load={()=>getReferenceSourceImage(examId,source.page_ref)} onReadyChange={value=>{ready.current=value;onReadyChange(value);}}/>
    <span className="reference-crop-outline" style={{left:`${box.x/source.width*100}%`,top:`${box.y/source.height*100}%`,width:`${box.width/source.width*100}%`,height:`${box.height/source.height*100}%`}}/>
  </div>;
}

type ReferenceMaterialEditorProps = Readonly<{ examId: string; questionId: string; editable: boolean; onChanged?: () => void }>;
export function ReferenceMaterialEditor(props: ReferenceMaterialEditorProps) {
  const session=useReferenceSession();
  // 同账号重新登录、切题或进入只读状态都重建草稿；旧实例只能保留原命令记录。
  return <ReferenceMaterialEditorContent key={`${session.scope}:${session.generation}:${props.examId}:${props.questionId}:${props.editable}`} {...props}/>;
}
function ReferenceMaterialEditorContent({ examId, questionId, editable, onChanged }: ReferenceMaterialEditorProps) {
  const [open, setOpen] = useState(false); const [materials, setMaterials] = useState<ReferenceMaterials | null>(null);
  const [items, setItems] = useState<ReferenceItem[]>([]); const [sources, setSources] = useState<ReferenceSource[]>([]);
  const [source, setSource] = useState<ReferenceSource | null>(null); const [box, setBox] = useState<ReferenceBox>({ x: 0, y: 0, width: 1, height: 1 });
  const [role, setRole] = useState<ReferenceRole>("question_diagram"); const [crop, setCrop] = useState<ReferenceItem | null>(null);
  const [sourceReady, setSourceReady] = useState(false); const [cropReady, setCropReady] = useState(false); const [dragging, setDragging] = useState(false);
  const [error, setError] = useState(""); const [busy, setBusy] = useState(false); const [pending, setPending] = useState(""); const [reason, setReason] = useState("");
  const owner = `${examId}:${questionId}`; const current = useRef(owner); current.current = owner;
  const session = getBusinessCommandSession(); const key = `reference-material-command:${session.scope}:${owner}`;
  const lock = useRef(false); const epoch = useRef(0); const mounted = useRef(true);
  const active = () => mounted.current && current.current === owner && getBusinessCommandSession() === session;
  useEffect(() => { mounted.current = true; epoch.current++; lock.current = false; setMaterials(null); setItems([]); setSources([]); setSource(null); setCrop(null); setError(""); setBusy(false); setOpen(false);
    try { setPending(sessionStorage.getItem(key) ?? ""); } catch { setPending(""); }
    return () => { mounted.current = false; epoch.current++; };
  }, [key]);
  const refresh = async () => {
    if (!active()) return;
    const version = epoch.current;
    const response = await getReferenceMaterials(examId, questionId);
    if (!active() || version !== epoch.current) return;
    setMaterials(response.materials); setItems(orderReferenceItems(response.materials.items));
  };
  const show = async () => { if (lock.current) return; lock.current = true; setOpen(true); setBusy(true); setError(""); const version = epoch.current;
    try { const [response, candidates] = await Promise.all([getReferenceMaterials(examId, questionId), editable ? getReferenceSources(examId) : Promise.resolve({ sources: [] })]);
      if (!active() || version !== epoch.current) return; setMaterials(response.materials); setItems(orderReferenceItems(response.materials.items)); setSources(candidates.sources);
    } catch { if (active() && version === epoch.current) setError("材料读取失败，请重新打开核对。"); } finally { if (active() && version === epoch.current) { lock.current = false; setBusy(false); } }
  };
  const finish = (id: string) => { if (!active()) return; let next = ""; try { const stored = sessionStorage.getItem(key); if (stored === id) sessionStorage.removeItem(key); else next = stored ?? ""; } catch { /* 远端结果已接受；本地清理失败不撤销成功。 */ } setPending(next); onChanged?.(); };
  const execute = async (work: (id: string) => Promise<void>) => {
    if (!active() || lock.current || pending || !session.scope) return; const id = crypto.randomUUID(); const version = epoch.current;
    try { const existing = sessionStorage.getItem(key); if (existing) { setPending(existing); setError("本题已有待确认操作，请先核对原操作结果。"); return; } sessionStorage.setItem(key, id); } catch { setError("无法保存待确认操作，请检查浏览器存储后重试。"); return; }
    setPending(id); setBusy(true); setError(""); lock.current = true;
    // 本次闭包冻结输入；浏览器只保存命令 ID，刷新后由服务端恢复原裁图或封闭旧命令。
    try { await work(id); if (active() && version === epoch.current) finish(id); }
    catch { if (active() && version === epoch.current) setError("操作结果尚未确认，请核对原操作结果。不要重复创建操作。"); }
    finally { if (active() && version === epoch.current) { lock.current = false; setBusy(false); } }
  };
  // 恢复可能补完裁图或封闭旧命令，属于写入；只读或会话未确定时不能继续原操作。
  const recover = async () => { if (!editable || !session.scope || !pending || lock.current || !active()) return; lock.current = true; setBusy(true); const version = epoch.current;
    try { const response = await resumeReferenceCommand(examId, questionId, pending); if (!active() || version !== epoch.current) return; const command = response.command;
      // 即使接口成功返回，也不能把另一条命令或另一题的回执当作本次恢复结果。
      const target = `${examId}/${questionId}`;
      if (command.command_id !== pending || (command.target_id && command.target_id !== target && !(command.operation === "reference.withdraw" && command.target_id.startsWith(`${target}/`)))) throw new Error("操作回执不匹配");
      if (command.status === "succeeded" && command.result) {
        if (command.operation === "reference.crop" && "crop_ref" in command.result) { setCropReady(false); setCrop(command.result); }
        else if ((command.operation === "reference.save" || command.operation === "reference.withdraw") && "items" in command.result && Array.isArray(command.result.items)) {
          // 回执只证明原操作成功；材料此后可能已冻结或撤回，编辑权限必须重新读取当前状态。
          setMaterials(null); setItems([]); finish(pending);
          try { await refresh(); if (active() && version === epoch.current) setError(""); }
          catch { if (active() && version === epoch.current) setError("原操作已确认，当前材料读取失败，请重新打开核对。"); }
          return;
        }
        else throw new Error("操作回执内容不匹配");
        finish(pending); setError("");
      } else if (command.status === "rejected") {
        setMaterials(null); setItems([]); setCrop(null); finish(pending);
        try { await refresh(); if (active() && version === epoch.current) setError("原操作未提交，旧命令已封闭。请核对当前内容后重新编辑。"); }
        catch { if (active() && version === epoch.current) setError("旧命令已封闭，当前材料读取失败，请重新打开核对。"); }
      }
      else setError("原操作仍待核实；图片尚未完成或请求结果未知，暂不能新建操作。");
    } catch { if (active() && version === epoch.current) setError("原操作核对失败，请稍后再次核对。"); }
    finally { if (active() && version === epoch.current) { lock.current = false; setBusy(false); } }
  };
  const canEdit = editable && materials && !materials.frozen && !pending && !busy;
  const chooseSource = (ref: string) => { const next = sources.find(item => item.page_ref === ref) ?? null; setSource(next); setSourceReady(false); setDragging(false); setCrop(null); if (next) setBox({ x: 0, y: 0, width: next.width, height: next.height }); };
  const createCrop = () => { if (!canEdit || !source || !sourceReady || dragging || !validReferenceBox(box, source)) return; const input = { page_ref: source.page_ref, page_sha256: source.sha256, page_width: source.width, page_height: source.height, pixel_bbox: { ...box } };
    setCrop(null); setCropReady(false); void execute(async id => { const result = await createReferenceCrop(examId, questionId, { ...input, command_id: id }); if (active()) setCrop(result.crop); }); };
  const save = (mode: "reference_images" | "text_only") => { if (!canEdit || !materials || (mode === "reference_images" && !validReferenceSelection(items))) return;
    const input = { expected_revision: materials.revision, mode, items: mode === "text_only" ? [] : orderReferenceItems(items).map(({ crop_ref, role, ordinal }) => ({ crop_ref, role, ordinal })) };
    void execute(async id => { const result = await saveReferenceMaterials(examId, questionId, { ...input, command_id: id }); if (active()) { setMaterials(result.materials); setItems(orderReferenceItems(result.materials.items)); } }); };
  return <section className="reference-material-editor" aria-label="本题参考图片">
    <div className="section-head"><div><h3>参考图</h3><p>题目图、参考解答图随本场评分依据一起冻结。</p></div><Button onClick={() => void show()}>核对本题参考图</Button></div>
    <Drawer title="本题参考图" open={open} width={760} onClose={() => { setOpen(false); setSourceReady(false); setCropReady(false); setDragging(false); }} destroyOnClose>
      {error && <Alert type="warning" showIcon message={error} />}
      {pending && <Button disabled={busy || !editable || !session.scope} onClick={() => void recover()}>核对原操作结果</Button>}
      {materials && <p>{materials.mode === "legacy_unrecorded" ? "本题尚未记录材料模式" : materials.mode === "text_only" ? "已明确使用文字参考" : `已保存 ${materials.items.length} 张参考图 · 第 ${materials.revision} 版`}{materials.frozen ? "；已随考试冻结" : ""}{materials.use_state === "withdrawn" ? "；已撤回使用授权" : ""}</p>}
      <ol className="reference-selected-list">{items.map((item, index) => <li key={item.crop_ref}>
        <strong>{referenceRoleLabel(item.role)} {index + 1}</strong><p>{item.width} × {item.height} · 来源第 {item.source.page_no} 页</p>
        <ProtectedReferenceImage identity={`${session.scope}:${owner}:${item.crop_ref}`} alt={`${referenceRoleLabel(item.role)}实际裁图`} load={() => getReferenceCropImage(examId, questionId, item.crop_ref)} />
        {canEdit && <Space wrap><Select className="reference-role-select" aria-label={`第${index + 1}张图片角色`} value={item.role} options={(["question_diagram", "reference_solution"] as const).map(value => ({ value, label: referenceRoleLabel(value), disabled: value !== item.role && items.filter(current => current.role === value).length >= 2 }))} onChange={value => { const next = orderReferenceItems(items.map((current, i) => i === index ? { ...current, role: value } : current)); if (validReferenceSelection(next)) setItems(next); }} />
          <Button disabled={index === 0 || items[index-1].role !== item.role} onClick={() => setItems(orderReferenceItems(items.map((current, i) => i === index - 1 ? item : i === index ? items[index - 1] : current)))}>上移</Button><Button danger onClick={() => setItems(orderReferenceItems(items.filter((_, i) => i !== index)))}>移除</Button></Space>}
      </li>)}</ol>
      {canEdit && <>
        <p>题目图和参考解答图各最多 2 张，合计最多 4 张；题目图先发送。</p>
        <Select aria-label="选择已有资料页" value={source?.page_ref} placeholder="从已有资料选择页面" style={{ width: "100%" }} options={sources.map(item => ({ value: item.page_ref, label: `${item.name} · 第 ${item.page_no} 页` }))} onChange={chooseSource} />
        {!sources.length && <p>没有可用图片页。请先在原资料入口导入图片，或完成 PDF 页面解析。</p>}
        {source && <><ReferenceCropSurface key={source.page_ref} source={source} examId={examId} identity={`${session.scope}:${owner}:${source.page_ref}`} box={box} onBox={value=>{setCrop(null);setBox(value);}} onReadyChange={setSourceReady} onDraggingChange={setDragging}/>
          <p>以页面左上角为原点填写整数像素范围。保留图例、条件和辅助线，并核对可见身份信息。</p>
          <div className="reference-coordinate-grid">{(["x", "y", "width", "height"] as const).map(field => <label key={field}>{({ x: "左边距", y: "上边距", width: "宽度", height: "高度" })[field]}<InputNumber min={field === "x" || field === "y" ? 0 : 1} precision={0} value={box[field]} onChange={value => { setCrop(null); setBox({ ...box, [field]: value ?? 0 }); }} /></label>)}</div>
          <Button disabled={!sourceReady || dragging || !validReferenceBox(box, source)} onClick={createCrop}>生成实际裁图</Button>
        </>}
      </>}
      {crop && <section><h4>核对将发送的实际图片</h4><ProtectedReferenceImage identity={`${session.scope}:${owner}:${crop.crop_ref}`} alt="待确认的实际裁图" load={() => getReferenceCropImage(examId, questionId, crop.crop_ref)} onReadyChange={setCropReady} />
        {/* Blob 已取回不代表图片已显示；加载失败或仍在加载时，教师不能确认实际内容。 */}
        <Space wrap><Select className="reference-role-select" aria-label="待添加图片角色" value={role} options={(["question_diagram", "reference_solution"] as const).map(value => ({ value, label: referenceRoleLabel(value), disabled: items.filter(current => current.role === value).length >= 2 }))} onChange={setRole} /><Button disabled={!canEdit || !cropReady || !validReferenceSelection([...items, { ...crop, role }])} onClick={() => { if (!cropReady) return; setItems(orderReferenceItems([...items, { ...crop, role, ordinal: items.length + 1 }])); setCrop(null); }}>已核对，加入本题</Button></Space>
      </section>}
      {canEdit && <Space wrap><Button type="primary" disabled={!validReferenceSelection(items)} onClick={() => save("reference_images")}>保存参考图清单</Button><Button onClick={() => save("text_only")}>明确仅使用文字参考</Button></Space>}
      {editable && materials?.frozen && materials.manifest_id && materials.use_state === "active" && <section><h4>撤回本题材料使用授权</h4><p>冻结内容保留；撤回会阻断后续 AI 调用及未提交建议。</p><Input.TextArea aria-label="撤回原因" maxLength={1000} value={reason} onChange={event => setReason(event.target.value)} /><Button danger disabled={busy || Boolean(pending) || !reason.trim()} onClick={() => { const id = materials.manifest_id!; const revision = materials.authorization_revision!; const frozenReason = reason.trim(); void execute(async command => { const response = await withdrawReferenceMaterials(examId, questionId, id, { command_id: command, expected_authorization_revision: revision, reason: frozenReason }); if (active()) { setMaterials(response.materials); await refresh(); } }); }}>撤回使用授权</Button></section>}
    </Drawer>
  </section>;
}
