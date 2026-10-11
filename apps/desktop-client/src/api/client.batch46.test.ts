import { afterEach, describe, expect, it, vi } from "vitest";
import { DesktopApiClient, normalizeBaseUrl } from "./client";

afterEach(() => vi.unstubAllGlobals());

describe("桌面服务端地址尾斜杠边界", () => {
  it.each([
    [" \t https://grading.example.edu/// \n", "https://grading.example.edu"],
    ["https://grading.example.edu/school//api///", "https://grading.example.edu/school//api"],
    ["https://grading.example.edu/school/%2F///", "https://grading.example.edu/school/%2F"],
    ["http://[::1]:8080///", "http://[::1]:8080"],
    ["https://grading.example.edu", "https://grading.example.edu"]
  ])("规范化 %s 时只裁剪尾部斜杠", (input, expected) => {
    expect(normalizeBaseUrl(input)).toBe(expected);
    expect(new DesktopApiClient({ baseUrl: input }).url("/health")).toBe(`${expected}/health`);
  });

  it("长尾斜杠被裁剪，后接字符的长斜杠路径保留", async () => {
    const slashes = "/".repeat(12000);
    const trailing = `https://grading.example.edu/school${slashes}`;
    const nonTrailing = `https://grading.example.edu/school${slashes}paper`;
    expect(normalizeBaseUrl(trailing)).toBe("https://grading.example.edu/school");
    expect(normalizeBaseUrl(nonTrailing)).toBe(nonTrailing);

    const fetchMock = vi.fn().mockImplementation(async () => new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await new DesktopApiClient({ baseUrl: trailing }).health();
    await new DesktopApiClient({ baseUrl: nonTrailing }).health();
    expect(fetchMock.mock.calls.map(call => call[0])).toEqual([
      "https://grading.example.edu/school/health",
      `${nonTrailing}/health`
    ]);
  });

  it.each([
    [" \t\n ", "API 服务端地址不能为空"],
    [" //// ", "API 服务端地址不能为空"],
    ["not a url///", "API 服务端地址格式无效"],
    ["http://user:password@grading.example.edu/path?draft=1#part///", "API 服务端地址不能包含用户名或密码"],
    ["http://grading.example.edu/path?draft=1#part///", "API 服务端地址不能包含查询参数或片段"],
    ["http://grading.example.edu/path///", "远程 API 必须使用 HTTPS；HTTP 仅允许本机开发地址"]
  ])("输入 %s 仍按原校验顺序拒绝", (input, expectedError) => {
    expect(() => normalizeBaseUrl(input)).toThrow(expectedError);
    expect(() => new DesktopApiClient({ baseUrl: input })).toThrow(expectedError);
  });
});
