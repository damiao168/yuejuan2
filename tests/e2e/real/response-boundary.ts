export interface BoundaryResponse {
  url(): string;
  status(): number;
  request(): { method(): string };
}

export function unexpectedForbiddenResponses(
  responses: readonly BoundaryResponse[],
  expectedResponse?: BoundaryResponse,
): string[] {
  // 只豁免本次跨校探测捕获的响应实例，同URL的其他请求仍须报告权限错误。
  return responses
    .filter((response) => response !== expectedResponse)
    .map((response) => `${response.request().method()} ${response.url()}`);
}
