import { MockGradingAdapter } from "./mockAdapter.js";
import { LocalModelAdapter } from "./localModelAdapter.js";

export class NotConfiguredError extends Error {
  constructor(message) {
    super(message);
    this.name = "NotConfiguredError";
  }
}

// 这里只声明兼容适配器的配置与能力接口，grade 仍主动拒绝执行，不能用于真实调用。
export class OpenAICompatibleGradingAdapter {
  constructor(env = process.env) {
    this.baseUrl = env.GRADING_OPENAI_BASE_URL ?? "https://api.openai.com/v1";
    this.apiKey = env.GRADING_OPENAI_API_KEY;
    this.model = env.GRADING_OPENAI_MODEL;
    if (!this.apiKey || !this.model) {
      throw new NotConfiguredError("OpenAI-compatible adapter requires GRADING_OPENAI_API_KEY and GRADING_OPENAI_MODEL");
    }
  }

  get_model_info() {
    return { adapter: "openai_compatible", model_version: this.model, mock: false };
  }

  supports_vision() {
    return true;
  }

  supports_structured_output() {
    return true;
  }

  async grade() {
    throw new NotConfiguredError("Network grading is intentionally not implemented in the isolated lab skeleton");
  }
}

export function createAdapter(name = "mock", options = {}) {
  if (name === "mock") return new MockGradingAdapter(options);
  if (name === "openai_compatible") return new OpenAICompatibleGradingAdapter(options.env);
  if (name === "local") return new LocalModelAdapter(options);
  throw new Error(`Unsupported grading adapter: ${name}`);
}

export { LocalModelAdapter, MockGradingAdapter };
