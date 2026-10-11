# 架构图维护说明

图纸描述当前实现，源码变更后先核对事实，再修改图纸。继续遵守 [注释与维护标准](../engineering/commenting-standard.md)；解释权限、状态、并发顺序和恢复边界，不能用理想行为替代实际实现。

## 变更影响矩阵

| 代码变化 | 必须核对的图纸 | 重点证据 |
| --- | --- | --- |
| 新功能/调整功能入口 | 01、05，并检查08/10 | 真正路由、页面渲染、权限、调用与失败分支；保留稳定F编号 |
| 新模块/装配/依赖 | 02、03 | composition root、真实import、接口注入；逻辑域和独立进程分开 |
| 表/字段/约束/迁移 | 04，并检查05/09/10 | 当前迁移链、PK/FK/UQ/索引、版本、tenant/school、RLS |
| API/OpenAPI/SDK/页面传输 | 08，并检查01/05/10 | 注册方法路径、动态参数、guard顺序、真实调用、二进制/流式响应 |
| 识别/模板/配准/切题 | 06、05、08、09 | Worker与Go入口、输入输出、校正失效、人工恢复和租约提交 |
| AI配置/评分/仲裁 | 07、05、08、09 | activeAdapter与Panel实际接入、冻结版本、结算、成本及人工终态 |
| 任务状态/并发/幂等/恢复 | 09，并检查05–08/10 | 枚举与SQL条件、lease/generation、锁顺序、同事务和收据重放 |
| 权限/凭据/部署 | 10、02、08 | 当前角色、资源scope、加密使用链、Compose profile、实际代理边界 |

## 文档来源与编辑方式

`specs/*.json`是本批生成器的可维护源：每页有稳定id、标题、尺寸、节点和连线；节点有label/status/evidence，连线有真实方向、说明、出处和可选拐点。`diagrams/*.drawio`是原生交付文件。`build_diagrams.py`会覆盖这10份生成图纸，因此采用规格维护时需同步保存编辑器内的修改，不能只改draw.io而遗漏规格。也可转为人工维护，但需明确新的权威来源并停用覆盖式生成。

在新代码已核对后编辑对应JSON，保留稳定节点id和F编号；新增事实必须有文件与有效行号，重要依赖边也保留出处。状态`implemented/partial/planned/issue/infra/unknown`与README一致。`unknown`不是`planned`，没有设计依据时不能擅自列规划。图面使用`display_label`短说明，完整`label`输出到原生对象的`details`属性，源码放`evidence`。修改事实时同步核对简化脚本中的显示文案，不能让短说明和完整说明矛盾。复杂主题新增独立页面，主流程统一方向，判断用菱形。

历史迁移、generated SDK、lock、快照、带哈希提示词不为图纸直接改写；数据库变更使用正式新迁移，生成物走原工程生成流程。图表只保留字段名和脱敏协议，不嵌入口令、token、真实学生数据或账号凭据。

## 更新与检查

以下命令从仓库根执行，均生成架构文档或做只读校验，不连接持久业务数据库：

```powershell
python -m pip install -r docs/architecture/tools/requirements.txt
python docs/architecture/tools/simplify_diagrams.py
python docs/architecture/tools/fit_box_typography.py
python docs/architecture/tools/build_diagrams.py
python docs/architecture/tools/merge_review_ledger.py
python docs/architecture/tools/verify_database_catalog.py
python docs/architecture/tools/verify_delivery.py
```

`build_diagrams.py`核对证据路径/行号、重复id、节点范围/重叠、边端点并重新解析XML。错误必须修复，不能删验证规则让结果通过。`verify_database_catalog.py`静态比对PG约束与覆盖，并仅在SQLite内存库执行本地schema；其成功不代表PG迁移执行成功。

`build_diagrams.py` 的磁盘回读、`verify_delivery.py`、`verify_typography.py` 的图纸读取，以及 `build_preview.py` 的 SVG 解析使用 `defusedxml`，拒绝 DTD、自定义实体及外部实体引用。当前原生图纸和预览不需要这些声明；如遇拒绝，先检查文件来源和内容，再重新生成或修正，不能改回普通解析器绕过检查。此约束只覆盖这四个工具的 XML 读取入口，不代表所有图纸处理工具均支持不可信外部文件。

`verify_delivery.py`核对当前规格与XML的页面、节点、连线、短标签、完整属性和源码行号，检查当前XML与原生渲染hash一致，并核对全文审读文件当前hash。失败后先重新审读受影响内容；`retire_stale_reviews.py`可保留旧证据并撤销已失效的完成声明，再合并账本，它不会替代重审或自动认定新版本完成。

`simplify_diagrams.py`只缩短显示文字，保留关系、完整说明与出处；ER仍显示精选字段、唯一约束和实际索引。模块职责来源保存在[module-responsibilities.json](evidence/module-responsibilities.json)，包目录生成时须回填职责，不能只输出import数量。

字号由`build_diagrams.py`及节点的`title_font_size/body_font_size`维护。`fit_box_typography.py`用 Microsoft YaHei 的实际字宽，将普通标题放大到40–72px、正文32px，ER表名34px、字段26px，并按文字收紧方框；`typography_layout_base`保留首次调整前的几何，重复运行不会累乘缩放。新增节点或人工重排后须同步该基准，重排后重新修正连线路由；不要通过缩小正文来消除溢出。仅改排版时，在原生渲染之后运行`verify_typography.py`核对完整说明、出处、状态与关系保留；其结果不代表重新审读业务源码。

需要更新静态目录时可使用`scan_repository.py`、`extract_database_catalog.py`和`evidence/extract-frontend-api.mjs`。**这些是盘点工具，不是人工审读工具**。重新盘点会覆盖目录，先保存本批原始基线和审读证据；新审计批次应建立独立日期目录或明确替换范围。数据库/前端/root规格生成脚本会重排对应页，可能覆盖人工布局与连线路由；仅在需要重建内容时运行，然后重做视觉验证。

用draw.io打开全部受影响文件，逐页确认中文、箭头方向、页面边界、图例、判断文字和分支。本批另用实际diagrams.net EditorUi验证，方法保存在`tools/render_native.js`：临时文档服务器`preview_server.py`只监听127.0.0.1:8766，浏览器只读文档和向限定的预览/证据路径写渲染结果。先打开实际diagrams.net嵌入页面，再以已有playwright-cli运行该脚本。无需升级依赖或修改项目配置。

若原生渲染报告发现边穿过无关节点，运行`route_obstructions.py`只调整JSON连线的points/ports，再重建、重渲染。最后运行`build_preview.py`更新自包含离线HTML；contact预览用于逐页视觉QA，不能以机器未报错替代人工阅读。浏览器不可用时保留XML与静态结果，明确未验证打开/渲染，不能写“已在draw.io打开”。

## 审读与版本记录

逐文件记录保存`read_status`、`analysis_status`、理由、source_sha256和modified；complete仅在全文、业务边界与相关调用核对后填写。partial及只搜索/枚举的文件保持pending。源码hash变化使完成记录失效，不能通过重算hash自动继承旧审读。merge脚本只合并可信记录，不自动清pending。状态表与handling=protected分开，受保护不等于已审读。

用Git一起提交规格、draw.io、出处、审查报告和必要验证结果；提交说明写清源码基线/未提交工作区、事实变化、接入边界、风险与验证。更新风险时保留触发条件与动态验证范围，不把未复现当修复，不把旧风险文案机械复制为现状。本批未提交/推送或修改任何现有代码。

后续CI可做XML解析、路径/行号有效性、枚举/路由/schema变化提示与图纸更新关联检查。提示应进入人工评审，**不能仅因代码变而图纸未变就作为阻止合并的唯一理由**。语义没有变化时记录无需改图的理由；代码新增行为而图纸仍承诺旧行为时必须同步修正。

`snapshot_workspace.py --verify`用于比对本批原始3396文件；不要为让检查通过而重建旧baseline。未来批次应新建基线，并保留原有未提交修改与历史文档。
