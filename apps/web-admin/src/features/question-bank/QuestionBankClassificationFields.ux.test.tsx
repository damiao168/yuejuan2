// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { Form, type FormInstance } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QuestionBankClassificationFields } from "./QuestionBankClassificationFields";

describe("teacher classification fields retain historical identities", () => {
  let root: Root; let host: HTMLDivElement; let form: FormInstance;
  const original = { fields: [{ key: "legacy_field", label: "教材", type: "enum", required: false, options: [{ value: "legacy_option", label: "旧教材", active: false }] }], taxonomies: [{ id: "knowledge_points", label: "知识点", terms: [{ id: "term-old", label: "旧知识点", active: false }, { id: "term-active", label: "现用知识点", active: true }] }] };
  function Probe() { [form] = Form.useForm(); return <Form form={form} initialValues={structuredClone(original)}><QuestionBankClassificationFields form={form} /></Form>; }
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
    vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
    host = document.createElement("div"); document.body.appendChild(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); });
  async function click(text: string) { const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent?.replace(/\s/g, "") === text.replace(/\s/g, ""))!; expect(button).toBeDefined(); await act(async () => button.click()); }
  async function editValue(previous: string, next: string) { const input = [...host.querySelectorAll<HTMLInputElement>("input")].find((item) => item.value === previous)!; expect(input).toBeDefined(); await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, next); input.dispatchEvent(new Event("input", { bubbles: true })); }); }

  it("keeps historical term and option identifiers plus disabled selection state when labels change", async () => {
    await act(async () => root.render(<Probe />));
    await editValue("旧知识点", "保留的历史知识点"); await editValue("旧教材", "停用教材名称");
    const values = form.getFieldsValue(true);
    expect(values.taxonomies[0].terms[0]).toEqual({ id: "term-old", label: "保留的历史知识点", active: false });
    expect(values.fields[0].options[0]).toEqual({ value: "legacy_option", label: "停用教材名称", active: false });
    expect(values.fields[0].key).toBe("legacy_field"); expect(values.taxonomies[0].id).toBe("knowledge_points");
    expect([...host.querySelectorAll<HTMLInputElement>("input")].filter((input) => ["legacy_field", "term-old", "knowledge_points"].includes(input.value)).every((input) => input.closest(".ant-form-item-hidden"))).toBe(true);
  });

  it("generates new field, option, catalog and term identifiers without asking teachers for English codes", async () => {
    await act(async () => root.render(<Probe />));
    await click("添加补充字段"); await click("添加选项"); await click("添加目录条目"); await click("添加分类目录");
    const values = form.getFieldsValue(true);
    expect(values.fields[1].key).toMatch(/^field_[a-f0-9]{32}$/);
    expect(values.fields[0].options[1]).toMatchObject({ value: expect.stringMatching(/^option_[a-f0-9]{32}$/), active: true });
    expect(values.taxonomies[0].terms[2]).toMatchObject({ id: expect.stringMatching(/^term_[a-f0-9]{32}$/), active: true });
    expect(values.taxonomies[1].id).toMatch(/^catalog_[a-f0-9]{32}$/);
    expect(host.textContent).toContain("系统生成内部编号");
    expect(host.textContent).not.toContain("英文"); expect(host.textContent).not.toContain("legacy_field");
    expect(values.taxonomies[0].terms[0]).toEqual(original.taxonomies[0].terms[0]);
  });
});
