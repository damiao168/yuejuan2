import type { MathUnderstandingResponse } from "../../../api/mathUnderstanding";

// 更正投影由服务端负责。旧版响应仍可读取，
// 但客户端不会从有限的审计历史中推导有效证据。
export function selectEffectiveMathArtifact(response: MathUnderstandingResponse) {
  return response.effective_artifact ?? response.artifact;
}
