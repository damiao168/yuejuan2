SAFE_UPSTREAM_ERROR_CODES = frozenset({
    "model_not_found", "unsupported_model", "invalid_api_key", "insufficient_quota",
    "invalid_parameter", "unsupported_parameter", "context_length_exceeded",
    "rate_limit_exceeded", "unsupported_image", "model_not_support_image",
})


class AgentError(Exception):
    def __init__(self, code, message, status=500, retryable=False, request_id="", *,
                 upstream_http_status=None, upstream_error_code=None, execution_diagnostics=None):
        super().__init__(message)
        self.code = code
        self.message = message
        self.status = status
        self.retryable = retryable
        self.request_id = request_id
        from .execution_diagnostics import sanitize_execution_diagnostics
        self.execution_diagnostics = sanitize_execution_diagnostics(execution_diagnostics)
        # 供应商错误可能回显密钥或页面内容，只保留固定类别及有效 HTTP 状态，不透传任意字符串。
        self.upstream_http_status = (upstream_http_status
                                     if type(upstream_http_status) is int and 100 <= upstream_http_status <= 599 else None)
        self.upstream_error_code = (upstream_error_code
                                    if isinstance(upstream_error_code, str) and upstream_error_code in SAFE_UPSTREAM_ERROR_CODES else None)

    def upstream_diagnostics(self):
        """可由明确支持此诊断的调用方返回；既有通用错误 payload 保持原协议。"""
        return {name: value for name, value in {
            "upstream_http_status": self.upstream_http_status,
            "upstream_error_code": self.upstream_error_code,
        }.items() if value is not None}

    def copy(self, request_id=None):
        return AgentError(self.code, self.message, status=self.status, retryable=self.retryable,
                          request_id=self.request_id if request_id is None else request_id,
                          upstream_http_status=self.upstream_http_status, upstream_error_code=self.upstream_error_code,
                          execution_diagnostics=self.execution_diagnostics)

    def payload(self, schema_version="grading-agent-v1"):
        result = {
            "schema_version": schema_version,
            "request_id": self.request_id,
            "error": {
                "code": self.code,
                "message": self.message,
                "retryable": self.retryable,
            },
        }
        if self.execution_diagnostics:
            result["execution_diagnostics"] = self.execution_diagnostics.copy()
        return result
