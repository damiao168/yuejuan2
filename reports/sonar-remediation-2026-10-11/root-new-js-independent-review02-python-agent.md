# SDK 第二轮打开阶段拆分：独立审查

未发现可执行回归。当前 SHA：`257f2b7281b3ce2fcce5d2463a25960dd7da75ad8a16eb69c2dc165d6fd940e6`。完整审读三态返回、三轮 EEXIST 重试、只读原错误传播、FD 所有权与失败关闭顺序；第一轮记录完整保留。

- openExistingArtifact 的 O_RDWR 成功只交出 {descriptor,created:false}；任何 fstat/read/write 异常仍由后续 writeIfChanged finally 关闭。
- ENOENT 唯一返回 undefined，允许当前轮尝试 wx+ 排他创建；null 唯一表示只读内容及路径 dev/ino 已核对、描述符已由 readonly helper 关闭。
- 只读入口仍仅限 EACCES/EPERM；内容不同、非普通文件抛原权限错误，读取/stat/close 自身错误按原顺序传播。不会被外层排他创建 catch 误捕获。
- openArtifactForUpdate 只对 wx+ 的 EEXIST 继续，attempt 0/1/2 共三轮，第 3 次争用立即抛该 createError；不吞其他创建错误、不开第 4 轮、不用截断模式。
- writeIfChanged 对 null 返回 false；对已有/新建描述符仍按 fstat、FD 内容读取、0 位置短写补齐、ftruncate、路径身份验证、finally close 顺序执行，空文件 created 布尔语义不变。
- 中文说明准确解释 undefined 与 null 的职责，未承诺路径校验后的全局排他保证。

root 原 18 Node 测试实际 exit 0，18 pass/0 fail/0 skip；02 报告 SHA 与当前一致。本审查只读原证据，不重复测试；平台仍待最终扫描。
