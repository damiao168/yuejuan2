import { beforeEach, describe, expect, it, vi } from "vitest";
import { submitScoringCommand } from "./scoringCommand";

const api = vi.hoisted(() => ({ recover: vi.fn(), start: vi.fn() }));
vi.mock("../../../../api/review", () => ({ recoverScoringCommand: api.recover, startScoringRun: api.start }));

function fixture() {
  const values = new Map<string, string>();
  const storage = {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value); },
    removeItem: (key: string) => { values.delete(key); }
  };
  return {
    values,
    options: {
      examId: "exam-1",
      storageKey: "tenant:actor:exam-1",
      storage,
      ready: vi.fn(async () => true),
      pending: vi.fn()
    }
  };
}

// 省略 transport 才会经过默认适配器；接口 mock 只验证调用和本地顺序，不验证真实 HTTP。
describe("评分命令默认适配器（mock API）", () => {
  beforeEach(() => { vi.resetAllMocks(); });

  it("恢复已提交的旧命令时不重跑就绪检查或创建任务", async () => {
    const { options, values } = fixture();
    values.set(options.storageKey, "old-command");
    api.recover.mockResolvedValue({ command_id: "old-command", status: "succeeded", scoring_run: { id: "run-1" } });

    expect(await submitScoringCommand(options)).toBe(true);

    expect(api.recover).toHaveBeenCalledWith(options.examId, "old-command");
    expect(options.ready).not.toHaveBeenCalled();
    expect(api.start).not.toHaveBeenCalled();
    expect(values.size).toBe(0);
    expect(options.pending).toHaveBeenLastCalledWith(null);
  });

  it("新命令先持久化再通过默认适配器创建任务", async () => {
    const { options, values } = fixture();
    api.start.mockImplementation(async (examId: string, commandId: string) => {
      expect(examId).toBe(options.examId);
      expect(values.get(options.storageKey)).toBe(commandId);
      expect(options.pending).toHaveBeenCalledWith(commandId);
      return { scoring_run: { id: "run-2" } };
    });

    expect(await submitScoringCommand(options)).toBe(true);

    expect(options.ready).toHaveBeenCalledTimes(1);
    expect(api.recover).not.toHaveBeenCalled();
    expect(api.start).toHaveBeenCalledTimes(1);
    expect(api.start).toHaveBeenCalledWith(options.examId, expect.stringMatching(/^web-/));
    expect(values.size).toBe(0);
    expect(options.pending).toHaveBeenLastCalledWith(null);
  });
});
