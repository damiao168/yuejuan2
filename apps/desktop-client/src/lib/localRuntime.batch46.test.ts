// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { appendLocalLog, readLocalLogs } from "./localRuntime";

describe("本地日志浏览器落地脱敏", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => window.localStorage.clear());

  it.each([
    {
      name: "保留原例中先 Bearer 后字段赋值的处理顺序",
      message: 'Authorization: Bearer abc.def password=cleartext {"access_token":"token-value"}',
      context: "safe operation",
      expectedMessage: "Authorization=[REDACTED] [REDACTED] password=[REDACTED] {access_token=[REDACTED]}",
      expectedContext: "safe operation"
    },
    {
      name: "完整单双引号可消费空白、换行、分隔符和嵌套敏感文本",
      message: '"password" : "two words,\nsemicolon; } secret=nested" \'credential\' = \'single, ; }\n value\'',
      context: 'secret="credential=inner; password=inside"; id_token=outer',
      expectedMessage: "password=[REDACTED] credential=[REDACTED]",
      expectedContext: "secret=[REDACTED]; id_token=[REDACTED]"
    },
    {
      name: "空引号仍被替换但没有值的字段保留原文",
      message: '"password":"" \'secret\':\'\' credential=',
      context: "password= ; secret=, credential=} access_token=\n",
      expectedMessage: "password=[REDACTED] secret=[REDACTED] credential=",
      expectedContext: "password= ; secret=, credential=} access_token=\n"
    },
    {
      name: "未闭合单双引号按裸值边界消费后继续后续字段",
      message: 'password="unfinished secret=hidden, tail',
      context: "credential='unfinished; id_token=part} refresh-token=value",
      expectedMessage: "password=[REDACTED] secret=[REDACTED], tail",
      expectedContext: "credential=[REDACTED]; id_token=[REDACTED]} refresh-token=[REDACTED]"
    },
    {
      name: "敏感键大小写原样保留且不扩大到前后缀键名",
      message: "mypassword=keep password_suffix=keep AccessToken=one refresh-token=two ID_TOKEN=three PASSWORD=four SECRET=five CREDENTIAL=six",
      context: '"authorization"=\'one, two\'; password:plain,secret:two;credential:three}',
      expectedMessage: "mypassword=keep password_suffix=keep AccessToken=[REDACTED] refresh-token=[REDACTED] ID_TOKEN=[REDACTED] PASSWORD=[REDACTED] SECRET=[REDACTED] CREDENTIAL=[REDACTED]",
      expectedContext: "authorization=[REDACTED]; password=[REDACTED],secret=[REDACTED];credential=[REDACTED]}"
    },
    {
      name: "Bearer ASCII 大小写数字和原标点范围继续被脱敏",
      message: "bearer Az09._~+/=- end",
      context: "BEARER TOKEN other",
      expectedMessage: "Bearer [REDACTED] end",
      expectedContext: "Bearer [REDACTED] other"
    }
  ])("$name", async ({ message, context, expectedMessage, expectedContext }) => {
    const entry = await appendLocalLog({ level: "error", message, context });

    expect(entry).toMatchObject({ level: "error", message: expectedMessage, context: expectedContext });
    expect(entry.id).toEqual(expect.any(String));
    expect(entry.at).toEqual(expect.any(String));
    expect(readLocalLogs()).toEqual([entry]);
    expect(JSON.parse(window.localStorage.getItem("edugrade.desktop.logs")!)).toEqual([entry]);
  });

  it("连续落地调用独立处理 message 和 context，不继承上一条的匹配位置", async () => {
    const first = await appendLocalLog({ level: "info", message: "password=first", context: "secret=first-context" });
    const second = await appendLocalLog({ level: "warning", message: "password=second", context: "secret=second-context" });
    const emptyContext = await appendLocalLog({ level: "info", message: "credential=third", context: "" });
    const absentContext = await appendLocalLog({ level: "info", message: "id_token=fourth" });

    expect(first).toMatchObject({ level: "info", message: "password=[REDACTED]", context: "secret=[REDACTED]" });
    expect(second).toMatchObject({ level: "warning", message: "password=[REDACTED]", context: "secret=[REDACTED]" });
    expect(emptyContext.context).toBe("");
    expect(absentContext.context).toBeUndefined();
    expect(readLocalLogs()).toEqual([absentContext, emptyContext, second, first]);
  });
});
