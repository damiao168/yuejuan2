# modelgovernance 原 12 个 Critical 问题

六个归属测试文件均完整审读并按真实协议、治理和流事件阶段拆分。保留原断言和测试案例。

基线六文件由 Go overlay 加载在同一当前生产源码上；当前版单独运行，均实际退出 0。GOMAXPROCS=1、-p1、GOMEMLIMIT=384MiB，网络依赖关闭。

- baseline-overlay：366 个通过测试事件，0 个跳过；原始 JSON 事件保存在对应 jsonl。
- current：366 个通过测试事件，0 个跳过；原始 JSON 事件保存在对应 jsonl。

测试身份、跳过身份与流事件 TRACE 完全一致。源码及本地 import 闭包 SHA 在测试前后不变。状态为 validated_fix_pending_platform_rescan，平台由 root 统一复扫。
