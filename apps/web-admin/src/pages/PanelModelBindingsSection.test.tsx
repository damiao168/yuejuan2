// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { fireEvent } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiClientError } from "../api/client";
import { PanelModelBindingsSection } from "./PanelModelBindingsSection";
const m = vi.hoisted(() => ({
	list: vi.fn(),
	save: vi.fn(),
	prompt: vi.fn(),
	error: vi.fn(),
	success: vi.fn(),
	confirm: vi.fn(),
}));
const ctx = vi.hoisted(() => ({
	message: { error: m.error, success: m.success },
	modal: { confirm: m.confirm },
}));
vi.mock("../api/modelApiConfig", () => ({
	listPanelModelRoleBindings: m.list,
	savePanelModelRoleBinding: m.save,
}));
vi.mock("../api/modelGovernance", () => ({
	getCurrentRuntimePrompt: m.prompt,
}));
vi.mock("antd", () => ({
	App: { useApp: () => ctx },
	Button: ({
		children,
		disabled,
		loading,
		onClick,
	}: {
		children: ReactNode;
		disabled?: boolean;
		loading?: boolean;
		onClick?: () => void;
	}) => (
		<button disabled={disabled || loading} onClick={onClick}>
			{children}
		</button>
	),
	Alert: ({ message, description }: { message: string; description?: string }) => <p>{message}{description}</p>,
	Select: ({
		value,
		options = [],
		disabled,
		onChange,
		"aria-label": label,
	}: {
		value?: string;
		options?: { value: string; label: string; disabled?: boolean }[];
		disabled?: boolean;
		onChange?: (value: string) => void;
		"aria-label"?: string;
	}) => (
		<select
			aria-label={label}
			value={value ?? ""}
			disabled={disabled}
			onChange={(e) => onChange?.(e.target.value)}
		>
			<option value="" />
			{options.map((o) => (
				<option key={o.value} value={o.value} disabled={o.disabled}>
					{o.label}
				</option>
			))}
		</select>
	),
	Input: ({
		value,
		onChange,
		"aria-label": label,
	}: {
		value: string;
		onChange?: React.ChangeEventHandler<HTMLInputElement>;
		"aria-label"?: string;
	}) => <input aria-label={label} value={value} onChange={onChange} />,
	InputNumber: ({ value }: { value: number }) => <span>{value}</span>,
	Switch: ({ checked, disabled, onChange, "aria-label": label }: { checked: boolean; disabled?: boolean; onChange: (checked: boolean) => void; "aria-label": string }) => <input type="checkbox" aria-label={label} checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} />,
}));
const config: import("../api/modelApiConfig").ManagedModelAPIConfig = {
	id: "model-a",
	tenant_id: "school-a",
	provider_key: "test",
	display_name: "测试模型",
	adapter_type: "openai_compatible",
	base_url: "https://test.example",
	region: "global",
	credential_configured: true,
	is_default: false,
	last_test_status: "success",
	config_source: "auto",
	created_at: "",
	updated_at: "",
	status: "active",
	model_name: "model",
	model_version: "v1",
	last_capability_status: "success",
	last_capability_probe_version: "structured-json-v4",
};
let host: HTMLDivElement, root: ReturnType<typeof createRoot>;
function button(text: string) {
	return [...host.querySelectorAll("button")].find(
		(b) => b.textContent === text,
	)!;
}
function change(select: HTMLSelectElement, value: string) {
	select.value = value;
	select.dispatchEvent(new Event("change", { bubbles: true }));
}
beforeEach(() => {
	vi.clearAllMocks();
	Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
	host = document.createElement("div");
	document.body.append(host);
	root = createRoot(host);
	m.prompt.mockResolvedValue({
		prompt: { prompt_version: "prompt-v1", components: [] },
	});
	m.confirm.mockImplementation(({ onOk }: { onOk: () => void }) => onOk());
});
afterEach(async () => {
	await act(async () => root.unmount());
	host.remove();
});
it("普通回复成功或旧结构化协议不能启用评分角色，当前结构化协议仍可选择", async () => {
  m.list.mockResolvedValue({ bindings: [] });
  const textOnly = { ...config, id: "text-only", last_text_status: "success" as const, last_capability_status: "failed" as const };
  const legacy = { ...config, id: "legacy-json", last_text_status: "success" as const, last_capability_probe_version: "structured-json-v3" };
  await act(async () => root.render(<PanelModelBindingsSection tenantID="school-a" configs={[textOnly, legacy, config]} />));
  const select = host.querySelector<HTMLSelectElement>('select[aria-label="主评 A评分模型"]')!;
  expect(select.querySelector<HTMLOptionElement>('option[value="text-only"]')!.disabled).toBe(true);
  expect(select.querySelector<HTMLOptionElement>('option[value="legacy-json"]')!.disabled).toBe(true);
  expect(select.querySelector<HTMLOptionElement>('option[value="model-a"]')!.disabled).toBe(false);
  await act(async () => {
    button("为 A/B/C 套用当前部署版本").click();
    change(select, "text-only");
  });
  await act(async () => button("保存主评 A").click());
  expect(m.save).not.toHaveBeenCalled();
  expect(m.error).toHaveBeenLastCalledWith(expect.stringContaining("已通过结构化输出检测"));
});
it("读取失败时禁止修改和保存，成功重试后才允许创建绑定", async () => {
	m.list
		.mockRejectedValueOnce(new Error("offline"))
		.mockResolvedValueOnce({ bindings: [] });
	m.save.mockResolvedValue({
		binding: {
			id: "binding",
			revision: "11111111-1111-4111-8111-111111111111",
			agent_role: "primary_a",
			managed_model_api_config_id: "model-a",
			prompt_version: "prompt-v1",
			strength_rank: 1,
			status: "active",
		},
	});
	await act(async () =>
		root.render(
			<PanelModelBindingsSection tenantID="school-a" configs={[config]} />,
		),
	);
	expect(m.error).toHaveBeenCalled();
	expect(host.textContent).toContain("角色配置读取失败，暂时不能保存");
	expect(button("保存主评 A").disabled).toBe(true);
	expect(button("为 A/B/C 套用当前部署版本").disabled).toBe(true);
	expect(
		host.querySelector<HTMLSelectElement>(
			'select[aria-label="主评 A评分模型"]',
		)!.disabled,
	).toBe(true);
	await act(async () => button("保存主评 A").click());
	expect(m.save).not.toHaveBeenCalled();
	await act(async () => button("刷新绑定").click());
	await act(async () => {
		button("为 A/B/C 套用当前部署版本").click();
		change(
			host.querySelector('select[aria-label="主评 A评分模型"]')!,
			"model-a",
		);
	});
	expect(button("保存主评 A").disabled).toBe(false);
	await act(async () => button("保存主评 A").click());
	expect(m.save).toHaveBeenCalledOnce();
});
it("保存等待期间阻止刷新和重复提交，成功结果保留在当前绑定", async () => {
	let resolveSave!: (value: unknown) => void;
	m.list.mockResolvedValue({ bindings: [] });
	m.save.mockImplementation(
		() =>
			new Promise((resolve) => {
				resolveSave = resolve;
			}),
	);
	await act(async () =>
		root.render(
			<PanelModelBindingsSection tenantID="school-a" configs={[config]} />,
		),
	);
	await act(async () => {
		button("为 A/B/C 套用当前部署版本").click();
		change(
			host.querySelector('select[aria-label="主评 A评分模型"]')!,
			"model-a",
		);
	});
	await act(async () => {
		button("保存主评 A").click();
		button("保存主评 A").click();
		button("刷新绑定").click();
	});
	expect(m.save).toHaveBeenCalledOnce();
	expect(m.list).toHaveBeenCalledOnce();
	expect(button("刷新绑定").disabled).toBe(true);
	await act(async () =>
		resolveSave({
			binding: {
				id: "binding",
			revision: "11111111-1111-4111-8111-111111111111",
				agent_role: "primary_a",
				managed_model_api_config_id: "model-a",
				prompt_version: "prompt-v1",
				strength_rank: 1,
				status: "active",
			},
		}),
	);
	expect(
		host.querySelector<HTMLSelectElement>(
			'select[aria-label="主评 A评分模型"]',
		)!.value,
	).toBe("model-a");
	expect(m.success).toHaveBeenCalledOnce();
	expect(button("刷新绑定").disabled).toBe(false);
});
it.each([
  ["主评 A", "primary_a"],
  ["主评 B", "primary_b"],
  ["仲裁 C", "arbiter"]
])("%s 保存返回 500 时保留三个角色的未保存输入，原内容可直接重试", async (title, role) => {
  const saved = vi.fn();
  const dirty = vi.fn();
  m.list.mockResolvedValue({ bindings: [] });
  m.save.mockRejectedValueOnce(new ApiClientError(500, "panel_model_binding_failed", "pq: internal statement failure"))
    .mockImplementationOnce(async (input) => ({ binding: { ...input, id: "saved-binding", revision: "saved-revision" } }));
  await act(async () => root.render(<PanelModelBindingsSection tenantID="school-a"
    configs={[config, { ...config, id: "model-b" }, { ...config, id: "model-c" }]} onSaved={saved} onDirtyChange={dirty} />));
  await act(async () => {
    button("为 A/B/C 套用当前部署版本").click();
    for (const [roleTitle, modelID] of [["主评 A", "model-a"], ["主评 B", "model-b"], ["仲裁 C", "model-c"]]) {
      change(host.querySelector<HTMLSelectElement>(`select[aria-label="${roleTitle}评分模型"]`)!, modelID);
    }
  });
  await act(async () => button(`保存${title}`).click());
  expect(m.error).toHaveBeenLastCalledWith("阅卷角色配置暂时无法保存，请稍后重试。");
  expect(m.success).not.toHaveBeenCalled();
  expect(saved).not.toHaveBeenCalled();
  expect(dirty).toHaveBeenLastCalledWith(true);
  expect(m.list).toHaveBeenCalledOnce();
  expect(host.querySelectorAll(".platform-panel-role-dirty")).toHaveLength(3);
  for (const [roleTitle, modelID] of [["主评 A", "model-a"], ["主评 B", "model-b"], ["仲裁 C", "model-c"]]) {
    expect(host.querySelector<HTMLSelectElement>(`select[aria-label="${roleTitle}评分模型"]`)!.value).toBe(modelID);
    expect(host.querySelector<HTMLSelectElement>(`select[aria-label="${roleTitle} Prompt 版本"]`)!.value).toBe("prompt-v1");
    expect(button(`保存${roleTitle}`).disabled).toBe(false);
  }
  // 服务端明确拒绝后，只有重试成功的角色才从草稿转为已保存；其他角色仍需单独保存。
  await act(async () => button(`保存${title}`).click());
  expect(m.save).toHaveBeenCalledTimes(2);
  expect(m.save.mock.calls[1][0]).toEqual(m.save.mock.calls[0][0]);
  expect(m.save.mock.calls[1][0]).toMatchObject({ agent_role: role, expected_revision: "absent" });
  expect(saved).toHaveBeenCalledOnce();
  expect(m.success).toHaveBeenLastCalledWith(`${title} 已保存；不会改变日常对话模型`);
  expect(host.querySelectorAll(".platform-panel-role-dirty")).toHaveLength(2);
});
it("相同范围刷新失败保留上次绑定和输入，重新读取成功才能保存", async () => {
	const binding = {
		id: "binding",
			revision: "11111111-1111-4111-8111-111111111111",
		agent_role: "primary_a",
		managed_model_api_config_id: "model-a",
		prompt_version: "older",
		strength_rank: 1,
		status: "active",
	};
	m.list
		.mockResolvedValueOnce({ bindings: [binding] })
		.mockRejectedValueOnce(new Error("offline"))
		.mockResolvedValueOnce({ bindings: [binding] });
	await act(async () =>
		root.render(
			<PanelModelBindingsSection tenantID="school-a" configs={[config]} />,
		),
	);
	await act(async () => button("为 A/B/C 套用当前部署版本").click());
	await act(async () => button("刷新绑定").click());
	expect(
		host.querySelector<HTMLSelectElement>(
			'select[aria-label="主评 A评分模型"]',
		)!.value,
	).toBe("model-a");
	expect(
		host.querySelector<HTMLSelectElement>(
			'select[aria-label="主评 A Prompt 版本"]',
		)!.value,
	).toBe("prompt-v1");
	expect(button("保存主评 A").disabled).toBe(true);
	await act(async () => button("刷新绑定").click());
	expect(
		host.querySelector<HTMLSelectElement>(
			'select[aria-label="主评 A Prompt 版本"]',
		)!.value,
	).toBe("older");
	expect(button("保存主评 A").disabled).toBe(false);
});
it("切换到其他范围后读取失败，不展示前一范围的配置", async () => {
	const binding = {
		id: "binding",
			revision: "11111111-1111-4111-8111-111111111111",
		agent_role: "primary_a",
		managed_model_api_config_id: "model-a",
		prompt_version: "prompt-v1",
		strength_rank: 1,
		status: "active",
	};
	m.list
		.mockResolvedValueOnce({ bindings: [binding] })
		.mockRejectedValueOnce(new Error("offline"));
	await act(async () =>
		root.render(
			<PanelModelBindingsSection tenantID="school-a" configs={[config]} />,
		),
	);
	await act(async () =>
		root.render(
			<PanelModelBindingsSection tenantID="school-b" configs={[config]} />,
		),
	);
	expect(
		host.querySelector<HTMLSelectElement>(
			'select[aria-label="主评 A评分模型"]',
		)!.value,
	).toBe("");
	expect(button("保存主评 A").disabled).toBe(true);
});

it("刷新刚开始时拒绝同一轮事件中的保存", async () => {
  let resolveLoad!: (value: unknown) => void;
  m.list.mockResolvedValueOnce({ bindings: [] }).mockImplementationOnce(() => new Promise(resolve => { resolveLoad = resolve; }));
  await act(async () => root.render(<PanelModelBindingsSection tenantID="school-a" configs={[config]} />));
  await act(async () => {
    button("为 A/B/C 套用当前部署版本").click();
    change(host.querySelector('select[aria-label="主评 A评分模型"]')!, "model-a");
  });
  // 两次点击放在同一轮事件中，覆盖 loading state 还没有重绘的窗口。
  await act(async () => { button("刷新绑定").click(); button("保存主评 A").click(); });
  expect(m.list).toHaveBeenCalledTimes(2);
  expect(m.save).not.toHaveBeenCalled();
  expect(button("保存主评 A").disabled).toBe(true);
  await act(async () => resolveLoad({ bindings: [] }));
  expect(button("保存主评 A").disabled).toBe(false);
});


it("首次创建显式要求不存在，成功后再次保存使用返回的新修订", async () => {
  m.list.mockResolvedValue({ bindings: [] });
  const binding = { id: "binding", agent_role: "primary_a", managed_model_api_config_id: "model-a", prompt_version: "prompt-v1", strength_rank: 1, status: "active" };
  const firstRevision = "11111111-1111-4111-8111-111111111111";
  m.save.mockResolvedValueOnce({ binding: { ...binding, revision: firstRevision } })
    .mockResolvedValueOnce({ binding: { ...binding, revision: "22222222-2222-4222-8222-222222222222" } });
  await act(async () => root.render(<PanelModelBindingsSection tenantID="school-a" configs={[config]} />));
  await act(async () => {
    button("为 A/B/C 套用当前部署版本").click();
    change(host.querySelector('select[aria-label="主评 A评分模型"]')!, "model-a");
  });
  await act(async () => button("保存主评 A").click());
  expect(m.save.mock.calls[0][0].expected_revision).toBe("absent");
  await act(async () => button("保存主评 A").click());
  expect(m.save.mock.calls[1][0].expected_revision).toBe(firstRevision);
});

it("并发冲突保留三个角色草稿，显式刷新核对成功后才可使用新修订保存", async () => {
  const firstRevision = "11111111-1111-4111-8111-111111111111";
  const refreshedRevision = "22222222-2222-4222-8222-222222222222";
  const original = { id: "binding", revision: firstRevision, agent_role: "primary_a", managed_model_api_config_id: "model-a", prompt_version: "older", strength_rank: 1, status: "active" };
  m.list.mockResolvedValueOnce({ bindings: [original] }).mockResolvedValueOnce({ bindings: [{ ...original, revision: refreshedRevision, managed_model_api_config_id: "model-b", prompt_version: "other-admin" }] });
  m.save.mockRejectedValueOnce(new ApiClientError(409, "panel_model_binding_changed", "changed"))
    .mockResolvedValueOnce({ binding: { ...original, revision: "33333333-3333-4333-8333-333333333333", managed_model_api_config_id: "model-b", prompt_version: "prompt-v1" } });
  await act(async () => root.render(<PanelModelBindingsSection tenantID="school-a" configs={[config, { ...config, id: "model-b" }]} />));
  await act(async () => {
    button("为 A/B/C 套用当前部署版本").click();
    change(host.querySelector('select[aria-label="主评 B评分模型"]')!, "model-b");
  });
  await act(async () => button("保存主评 A").click());
  expect(m.save.mock.calls[0][0]).toMatchObject({ expected_revision: firstRevision, managed_model_api_config_id: "model-a" });
  expect(m.list).toHaveBeenCalledOnce();
  expect(host.textContent).toContain("三个角色的本页输入均已保留");
  expect(host.querySelector<HTMLSelectElement>('select[aria-label="主评 A Prompt 版本"]')!.value).toBe("prompt-v1");
  expect(host.querySelector<HTMLSelectElement>('select[aria-label="主评 B评分模型"]')!.value).toBe("model-b");
  expect(host.querySelector<HTMLSelectElement>('select[aria-label="仲裁 C Prompt 版本"]')!.value).toBe("prompt-v1");
  for (const role of ["主评 A", "主评 B", "仲裁 C"]) expect(button(`保存${role}`).disabled).toBe(true);
  await act(async () => button("保存主评 A").click());
  expect(m.save).toHaveBeenCalledOnce();
  await act(async () => button("刷新绑定").click());
  expect(m.confirm).toHaveBeenCalledOnce();
  expect(host.querySelector<HTMLSelectElement>('select[aria-label="主评 A评分模型"]')!.value).toBe("model-b");
  expect(host.querySelector<HTMLSelectElement>('select[aria-label="主评 A Prompt 版本"]')!.value).toBe("other-admin");
  expect(button("保存主评 A").disabled).toBe(false);
  await act(async () => button("为 A/B/C 套用当前部署版本").click());
  await act(async () => button("保存主评 A").click());
  expect(m.save.mock.calls[1][0]).toMatchObject({ expected_revision: refreshedRevision, managed_model_api_config_id: "model-b" });
});

it("旧服务未返回修订时拒绝保存，不能把已有角色误作首次创建", async () => {
  m.list.mockResolvedValue({ bindings: [{ id: "legacy", agent_role: "primary_a", managed_model_api_config_id: "model-a", prompt_version: "older", strength_rank: 1, status: "active" }] });
  await act(async () => root.render(<PanelModelBindingsSection tenantID="school-a" configs={[config]} />));
  expect(host.textContent).toContain("角色配置读取失败，暂时不能保存");
  expect(button("保存主评 A").disabled).toBe(true);
  await act(async () => button("保存主评 A").click());
  expect(m.save).not.toHaveBeenCalled();
});

it("题型选项保留部署范围和当前自定义范围，切换时仍读取同一学校绑定", async () => {
  m.list.mockResolvedValue({ bindings: [] });
  m.prompt.mockResolvedValue({ prompt: { prompt_version: "prompt-v1", components: [
    { key: "subject.mathematics.calculation" }, { key: "subject.mathematics.calculation" },
    { key: "subject.chinese.essay" }, { key: "subject.mathematics.short_answer.extra" }
  ] } });
  await act(async () => root.render(<PanelModelBindingsSection tenantID="school-a" configs={[config]} />));
  const typeSelect = () => [...host.querySelectorAll("label")].find((label) => label.textContent?.startsWith("题型"))!.querySelector<HTMLSelectElement>("select")!;
  expect([...typeSelect().options].map((option) => option.value)).toEqual(["", "*", "calculation"]);
  await act(async () => button("高级设置").click());
  await act(async () => fireEvent.change(host.querySelector(".platform-panel-bindings-scope input")!, { target: { value: "local_question" } }));
  await act(async () => button("载入自定义题型").click());
  expect(typeSelect().value).toBe("local_question");
  expect([...typeSelect().options].map((option) => option.value)).toEqual(["", "*", "calculation", "local_question"]);
  expect(typeSelect().selectedOptions[0].textContent).toBe("自定义题型 · local_question");
  expect(m.list).toHaveBeenLastCalledWith("school-a", "senior", "mathematics", "local_question");
});

it("启用开关只开放已存在的角色绑定，空绑定不会被误判为已有配置", async () => {
  m.list.mockResolvedValueOnce({ bindings: [] }).mockResolvedValueOnce({ bindings: [{
    id: "binding", revision: "11111111-1111-4111-8111-111111111111", agent_role: "primary_a",
    managed_model_api_config_id: "model-a", prompt_version: "prompt-v1", strength_rank: 1, status: "disabled"
  }] });
  await act(async () => root.render(<PanelModelBindingsSection tenantID="school-a" configs={[config]} />));
  const toggle = (role: string) => host.querySelector<HTMLInputElement>(`input[aria-label="${role}启用"]`)!;
  for (const role of ["主评 A", "主评 B", "仲裁 C"]) expect(toggle(role).disabled).toBe(true);
  await act(async () => button("刷新绑定").click());
  expect(toggle("主评 A").disabled).toBe(false);
  expect(toggle("主评 A").checked).toBe(false);
  expect(toggle("主评 B").disabled).toBe(true);
  expect(toggle("仲裁 C").disabled).toBe(true);
});

it("表格带入的模型只填入用户选择的角色，保存后通知刷新用途摘要", async () => {
  const saved = vi.fn();
  m.list.mockResolvedValue({ bindings: [] });
  m.save.mockImplementation(async (input) => ({ binding: { ...input, revision: "saved" } }));
  await act(async () => root.render(<PanelModelBindingsSection tenantID="school-a" configs={[config]} suggestedModelID={config.id} onSaved={saved}
    savedUses={[{ model_config_id: config.id, education_stage: "senior", subject_code: "physics", archetype_code: "short_answer", agent_role: "primary_b" }]} />));
  await act(async () => button("高中 / 物理 / 简答题 / 主评 B").click());
  expect(m.list).toHaveBeenLastCalledWith("school-a", "senior", "physics", "short_answer");
  await act(async () => button("将 model 用于主评 B").click());
  expect(host.querySelector<HTMLSelectElement>('select[aria-label="主评 A评分模型"]')!.value).toBe("");
  expect(host.querySelector<HTMLSelectElement>('select[aria-label="主评 B评分模型"]')!.value).toBe(config.id);
  expect(host.querySelector<HTMLSelectElement>('select[aria-label="仲裁 C评分模型"]')!.value).toBe("");
  await act(async () => button("为 A/B/C 套用当前部署版本").click());
  await act(async () => button("保存主评 B").click());
  expect(m.save).toHaveBeenCalledWith(expect.objectContaining({ tenant_id: "school-a", education_stage: "senior", subject_code: "physics", archetype_code: "short_answer", agent_role: "primary_b", managed_model_api_config_id: config.id, expected_revision: "absent" }));
  expect(saved).toHaveBeenCalledOnce();
});
