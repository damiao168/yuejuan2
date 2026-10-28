// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { LoginScreen } from "./SessionScreens";

const mocks=vi.hoisted(()=>({school:vi.fn(),login:vi.fn()}));
vi.mock("../../api",()=>({getPublicSchool:mocks.school,login:mocks.login}));
beforeEach(()=>{vi.resetAllMocks();window.history.replaceState({},"","/?school=school-a");mocks.school.mockResolvedValue({code:"school-a",name:"甲学校"});});
afterEach(cleanup);

it("分享学校核对名称后折叠学校码，切换再查一次且登录仍使用学校码",async()=>{
 const loggedIn=vi.fn();mocks.login.mockResolvedValue({user:{id:"student"}});
 render(<LoginScreen onLoggedIn={loggedIn}/>);
 await screen.findByText("甲学校");expect(screen.queryByRole("textbox",{name:"学校代码"})).toBeNull();
 fireEvent.change(screen.getByLabelText("密码"),{target:{value:"old-password"}});
 fireEvent.click(screen.getByRole("button",{name:"切换学校"}));
 expect((screen.getByLabelText("密码") as HTMLInputElement).value).toBe("");
 fireEvent.change(screen.getByRole("textbox",{name:"学校代码"}),{target:{value:"school-b"}});
 expect(mocks.school).toHaveBeenCalledTimes(1);
 mocks.school.mockResolvedValue({code:"school-b",name:"乙学校"});
 fireEvent.click(screen.getByRole("button",{name:"使用此学校"}));
 await screen.findByText("乙学校");
 fireEvent.change(screen.getByRole("textbox",{name:"账号"}),{target:{value:"student"}});
 fireEvent.change(screen.getByLabelText("密码"),{target:{value:"password"}});
 fireEvent.click(screen.getByRole("button",{name:"登录"}));
 await waitFor(()=>expect(mocks.login).toHaveBeenCalledWith({tenant_code:"school-b",username:"student",password:"password"}));
});

it("旧学校名称迟到不覆盖切换后的学校，解析失败保留代码供核对",async()=>{
 let finish!:(school:{code:string;name:string})=>void;
 mocks.school.mockReturnValueOnce(new Promise(resolve=>{finish=resolve}));
 render(<LoginScreen onLoggedIn={vi.fn()}/>);
 fireEvent.click(screen.getByRole("button",{name:"切换学校"}));
 fireEvent.change(screen.getByRole("textbox",{name:"学校代码"}),{target:{value:"school-b"}});
 mocks.school.mockRejectedValueOnce(new Error("unavailable"));
 fireEvent.click(screen.getByRole("button",{name:"使用此学校"}));
 await waitFor(()=>expect(mocks.school).toHaveBeenCalledWith("school-b"));
 finish({code:"school-a",name:"旧学校"});
 await waitFor(()=>expect(screen.getByText("school-b")).toBeTruthy());
 expect(screen.queryByText("旧学校")).toBeNull();
});
