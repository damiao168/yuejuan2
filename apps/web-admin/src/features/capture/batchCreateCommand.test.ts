import { describe, expect, it } from "vitest";
import { acceptBatchCreateCommand, batchCreateCommandKey, loadBatchCreateCommand } from "./batchCreateCommand";

function storage() {
  const values = new Map<string,string>();
  return { getItem: (key:string) => values.get(key) ?? null, setItem: (key:string,value:string) => { values.set(key,value); } };
}

describe("capture batch command persistence", () => {
  it("reloads the original identity and payload after an unknown outcome", () => {
    const store=storage();
    const key=batchCreateCommandKey("tenant","actor","exam");
    const draft={name:"Original",source_type:"web_upload" as const};
    const first=acceptBatchCreateCommand(store,key,draft);
    draft.name="Edited draft";
    expect(loadBatchCreateCommand(store,key)).toEqual(first);
    expect(acceptBatchCreateCommand(store,key,draft)).toEqual(first);
  });
  it("separates tenant, actor and exam while explicit new operations get new identities", () => {
    const store=storage();
    const draft={name:"Same content",source_type:"web_upload" as const};
    const commands=[['t','a','e'],['t','b','e'],['t','a','f'],['u','a','e']].map(parts=>acceptBatchCreateCommand(store,batchCreateCommandKey(parts[0],parts[1],parts[2]),draft));
    expect(new Set(commands.map(command=>command.payload.idempotency_key)).size).toBe(4);
  });
  it("refuses to overwrite corrupt or unavailable durable storage", () => {
    const store=storage(); store.setItem('broken','{');
    expect(()=>acceptBatchCreateCommand(store,'broken',{name:'Draft',source_type:'web_upload'})).toThrow();
    expect(store.getItem('broken')).toBe('{');
    expect(()=>acceptBatchCreateCommand({getItem:()=>null,setItem:()=>{throw new Error('quota');}},'key',{name:'Draft',source_type:'web_upload'})).toThrow('quota');
  });
  it("补传创建恢复时保留原用途和原因，不接受新的表单内容覆盖", () => {
    const store = storage();
    const draft = { name: "补传", source_type: "web_upload" as const, purpose: "supplement" as const, reason: "补扫遗漏答卷" };
    const command = acceptBatchCreateCommand(store, "supplement", draft);
    const recovered = acceptBatchCreateCommand(store, "supplement", { ...draft, reason: "新的编辑" });
    expect(recovered).toEqual(command);
    expect(recovered.payload).toMatchObject({ purpose: "supplement", reason: "补扫遗漏答卷" });
  });
});
