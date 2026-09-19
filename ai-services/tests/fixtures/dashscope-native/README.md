# DashScope 原生协议测试样例

本目录存放离线构造的协议样例，不包含真实学生数据或凭据；离线测试不会向阿里云发送这些文件。它们用于验证请求、响应和错误处理的协议形状，不代表已批准的生产模型配置。

请求结构和原生文本生成路径参考：

- <https://help.aliyun.com/zh/model-studio/text-generation>
- <https://help.aliyun.com/zh/model-studio/qwen-api-via-dashscope>
- <https://help.aliyun.com/zh/model-studio/qwen-structured-output>
- <https://help.aliyun.com/en/model-studio/vision>

错误样例的状态与错误码语义参考 <https://help.aliyun.com/zh/model-studio/error-code/>。`error-*.json` 用本地元数据字段 `http_status` 包装原生响应体；这个外层结构既不会发送给 DashScope，也不是其返回格式。

文本请求采用带日期的协议样例，图像请求采用稳定的模型别名。两者都不是获准用于生产的部署方案。最终模型与版本固定、账户授权、接口地域、费用和数据保留策略仍需按 [STORY-061B1 的准入条件](../../../../docs/stories/STORY-061-multi-provider-native-model-governance.md)审定。

`request-image.json` 将一张很小的合成 PNG 内嵌为 Data URL。多模态契约只接受一个由服务端确认来源的 `answer_segment_crop`，拒绝公开 URL、本地路径和 OSS URL。
