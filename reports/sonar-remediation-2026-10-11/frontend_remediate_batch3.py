from pathlib import Path
import json
r=Path('.');issues=json.loads((r/'reports/platform-audit-2026-10-10/sonar/issues.json').read_text(encoding='utf-8'))['issues']
for filename in sorted({x['component'].split(':',1)[1] for x in issues if x['rule']=='typescript:S1874'}):
 p=r/filename;s=p.read_text(encoding='utf-8')
 s=s.replace('MutableRefObject','RefObject').replace('FormEvent','SubmitEvent').replace('destroyOnClose','destroyOnHidden').replace('onDropdownVisibleChange','onOpenChange')
 # 单位和快捷码是装饰文字；保留表单的直接子控件，避免包裹后丢失 AntD 字段绑定。
 if 'TeacherManagementPage' not in filename:s=s.replace('addonAfter=','suffix=').replace('addonBefore=','prefix=')
 p.write_text(s,encoding='utf-8')
p=r/'apps/web-admin/src/features/members/teachers/TeacherManagementPage.tsx';s=p.read_text(encoding='utf-8')
import re
s,n=re.subn(r'<Input readOnly value=\{(activationURL|recoveryURL)\} addonAfter=\{(<Button.*?</Button>)\} />',r'<Space.Compact block><Input readOnly value={\1} />\2</Space.Compact>',s);assert n==3,n;p.write_text(s,encoding='utf-8')
p=r/'apps/web-admin/scripts/production-route-contracts.mjs';s=p.read_text(encoding='utf-8');s=s.replace('!node.importClause?.isTypeOnly','node.importClause?.phaseModifier !== ts.SyntaxKind.TypeKeyword');p.write_text(s,encoding='utf-8')
