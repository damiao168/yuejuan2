# full03 两项待处置误报：独立只读审读

analysisId `a741715d-5427-49a6-ac74-7dd92ad5cfa1`。两份当前源/冻结源与 scope SHA 均匹配：True。

`eb390af3-af14-4c27-b33c-0b1442f95181` / python:S125：build_preview.py 第17–18行是中文字体回退说明。tokenize 确认属于 COMMENT；删除注释不改变 AST，实际字体替换由第19行执行。符合保留维护规则说明的要求，建议平台 FALSE_POSITIVE。

`98688ff8-f75e-4bbb-897a-2eaf11ee1c8f` / javascript:S905：check_preview_typography.js 是 `playwright-cli run-code --filename=...` 的函数输入。已安装 @playwright/cli 0.1.18 的 coreBundle.js 第67038–67062行读取文件、在括号内求值并 await __fn__(page)；第68416–68428行定义命令映射。函数值由 runner 实际消费，建议平台 FALSE_POSITIVE。

只读 Node vm 函数输入解析实际 exit 0；未调用函数体或启动浏览器，不能声称当前浏览器验收通过。详细 loader 原文、SHA、告警 key 和限制见同名 JSON。
