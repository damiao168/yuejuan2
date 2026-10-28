// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup,fireEvent,render,screen,waitFor } from "@testing-library/react";
import { afterEach,beforeEach,expect,it,vi } from "vitest";
import { PrivateOpinionDraft } from "./PrivateOpinionDraft";
const mocks=vi.hoisted(()=>({request:vi.fn(),session:{scope:"tenant:actor",generation:1}}));
vi.mock("../api/client",()=>({apiClient:{request:mocks.request},getUserErrorMessage:(reason:Error)=>reason.message}));
vi.mock("../api/businessCommand",()=>({getBusinessCommandSession:()=>mocks.session}));
vi.mock("antd",()=>({Space:({children}:{children:ReactNode})=><div>{children}</div>,Alert:({message}:{message:ReactNode})=><p role="alert">{message}</p>,Button:({children,onClick,disabled}:{children:ReactNode;onClick:()=>void;disabled?:boolean})=><button disabled={disabled} onClick={onClick}>{children}</button>}));
beforeEach(()=>{vi.resetAllMocks();mocks.session={scope:"tenant:actor",generation:1};});afterEach(cleanup);
it("恢复服务端本人草稿，保存带独立CAS修订且不写浏览器持久存储",async()=>{
 const restore=vi.fn(),local=vi.spyOn(Storage.prototype,"setItem");
 mocks.request.mockResolvedValueOnce({revision:2,source:"source",source_revision:5,draft:{source:"source",source_revision:4,values:{reason:"旧意见"}}}).mockResolvedValueOnce({revision:3,source:"source",source_revision:5,draft:{}});
 render(<PrivateOpinionDraft kind="arbitration" recordId="task" source="source" sourceRevision={5} values={{reason:"当前意见"}} dirty busy={false} onRestore={restore}/>);
 await waitFor(()=>expect(restore).toHaveBeenCalledWith({reason:"旧意见"},true));
 fireEvent.click(screen.getByRole("button",{name:"保存私有草稿"}));
 await screen.findByText("已保存本人私有草稿，7天内可在重新登录后恢复。");
 const request=mocks.request.mock.calls[1];expect(JSON.parse(request[1].body)).toEqual({source:"source",source_revision:5,expected_revision:2,values:{reason:"当前意见"}});expect(local).not.toHaveBeenCalled();local.mockRestore();
});
it("另一页面修订冲突保留当前输入，切记录后迟到草稿不能恢复到新记录",async()=>{
 let complete!:(value:unknown)=>void;const restore=vi.fn();mocks.request.mockReturnValueOnce(new Promise(resolve=>{complete=resolve}));
 const view=render(<PrivateOpinionDraft kind="arbitration" recordId="old" source="source" sourceRevision={1} values={{reason:"当前输入"}} dirty busy={false} onRestore={restore}/>);
 mocks.request.mockResolvedValueOnce({revision:0,source:"source",source_revision:1,draft:null}).mockRejectedValueOnce(new Error("另一页面已更新"));
 view.rerender(<PrivateOpinionDraft kind="arbitration" recordId="new" source="source" sourceRevision={1} values={{reason:"当前输入"}} dirty busy={false} onRestore={restore}/>);
 complete({revision:1,source:"source",source_revision:1,draft:{values:{reason:"旧记录秘密"}}});
 await waitFor(()=>expect((screen.getByRole("button",{name:"保存私有草稿"}) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button",{name:"保存私有草稿"}));await screen.findByText("另一页面已更新");expect(restore).not.toHaveBeenCalled();
});

it("保存A时切B解除旧等待，旧账号失败不能污染B；会话变化后停用保存",async()=>{
 let reject!:(reason:Error)=>void;
 mocks.request.mockResolvedValueOnce({revision:0,source:"source",source_revision:1,draft:null}).mockReturnValueOnce(new Promise((_resolve,fail)=>{reject=fail})).mockResolvedValueOnce({revision:0,source:"source",source_revision:1,draft:null});
 const props={kind:"arbitration" as const,source:"source",sourceRevision:1,values:{reason:"当前输入"},dirty:true,busy:false,onRestore:vi.fn()};
 const view=render(<PrivateOpinionDraft {...props} recordId="A"/>);
 await waitFor(()=>expect((screen.getByRole("button",{name:"保存私有草稿"}) as HTMLButtonElement).disabled).toBe(false));fireEvent.click(screen.getByRole("button",{name:"保存私有草稿"}));
 view.rerender(<PrivateOpinionDraft {...props} recordId="B"/>);
 await waitFor(()=>expect((screen.getByRole("button",{name:"保存私有草稿"}) as HTMLButtonElement).disabled).toBe(false));
 reject(new Error("旧A的失败"));await waitFor(()=>expect(screen.queryByText("旧A的失败")).toBeNull());
 mocks.session={scope:"tenant:other",generation:2};fireEvent(window,new Event("business-command-scope-changed"));
 await screen.findByText("登录状态已变化，请重新打开当前记录后处理草稿");
 expect((screen.getByRole("button",{name:"保存私有草稿"}) as HTMLButtonElement).disabled).toBe(true);
});

it("读取失败后可以主动重读，重读期间新增输入需要核对后恢复",async()=>{
 const restore=vi.fn();
 mocks.request.mockRejectedValueOnce(new Error("临时读取失败")).mockResolvedValueOnce({revision:2,source:"source",source_revision:1,draft:{source:"source",source_revision:1,values:{reason:"已保存意见"}}});
 render(<PrivateOpinionDraft kind="arbitration" recordId="task" source="source" sourceRevision={1} values={{reason:"本页新输入"}} dirty busy={false} onRestore={restore}/>);
 await screen.findByText("临时读取失败");fireEvent.click(screen.getByRole("button",{name:"重新读取私有草稿"}));
 await waitFor(()=>expect(restore).toHaveBeenCalledWith({reason:"已保存意见"},true));
 expect(mocks.request).toHaveBeenCalledTimes(2);
});
