from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];apply=ns['apply']

path='services/api-gateway/internal/server/e2e_postgres_test.go';name='e2eAssertHighRiskTenantRLS'
def rls(s,body):
 privileged=body.index('\n\tif privilegedLogin {');role=body.index('\n\troleName :=',privileged)
 phase=body[privileged:role];inner=phase[phase.index('{')+1:phase.rfind('\n\t}')]
 inner='\n'.join(line[1:] if line.startswith('\t\t') else line for line in inner.split('\n'))
 visibility=body.index('\n\tvar count int');api=body.index('\n\truntimeRouter :=',visibility)
 student=body[visibility:api].replace('if err = scopedDB.QueryRowContext','if err := scopedDB.QueryRowContext')
 events=body[api:].replace('if err = scopedDB.QueryRowContext','if err := scopedDB.QueryRowContext')
 body=body[:privileged]+'\n\tif privilegedLogin {\n\t\te2eAssertPrivilegedTenantRLSLoginRejected(t, dsn)\n\t}\n'+body[role:visibility]+'\n\te2eAssertStudentTenantRLSScopes(t, scopedDB, tenantID, studentID, currentRole, searchPath)\n\te2eAssertLoginFactTenantRLSScopes(t, scopedDB, tenantID, studentID)\n'
 helpers='\n\n// 即使显式启用 RLS，超级用户或 BYPASSRLS 登录也不能进入租户运行查询。\nfunc e2eAssertPrivilegedTenantRLSLoginRejected(t *testing.T, dsn string) {\n\tt.Helper()'+inner+'\n}'
 helpers+='\n\n// 无租户与错误租户看不到学生；所属租户与显式维护上下文各能读到唯一记录。\nfunc e2eAssertStudentTenantRLSScopes(t *testing.T, scopedDB *sql.DB, tenantID, studentID, currentRole, searchPath string) {\n\tt.Helper()'+student+'\n}'
 helpers+='\n\n// 先通过真实认证路由写入风险和设备事实，再验证两种事实都遵守租户隔离。\nfunc e2eAssertLoginFactTenantRLSScopes(t *testing.T, scopedDB *sql.DB, tenantID, studentID string) {\n\tt.Helper()\n\tvar count int'+events+'}'
 return replace_function(s,name,body,helpers)
apply(path,name,rls)

name='e2eSplitSQLStatements'
def splitter(s,body):
 body='''
	splitter := e2eSQLStatementSplitter{statements: []string{}}
	for i := 0; i < len(sqlText); {
		switch {
		case splitter.dollarQuoteTag != "":
			i = splitter.consumeDollarQuote(sqlText, i)
		case splitter.inSingleQuote:
			i = splitter.consumeSingleQuote(sqlText, i)
		default:
			i = splitter.consumeOutsideQuote(sqlText, i)
		}
	}
	splitter.finishStatement()
	return splitter.statements
'''
 helpers=r'''

// 迁移拆句只在引号外识别分号；保持单引号转义、dollar quote 与行注释的原始字节。
type e2eSQLStatementSplitter struct {
	statements []string
	current strings.Builder
	inSingleQuote bool
	dollarQuoteTag string
}

func (s *e2eSQLStatementSplitter) consumeDollarQuote(sqlText string, i int) int {
	if strings.HasPrefix(sqlText[i:], s.dollarQuoteTag) {
		s.current.WriteString(s.dollarQuoteTag)
		i += len(s.dollarQuoteTag)
		s.dollarQuoteTag = ""
		return i
	}
	s.current.WriteByte(sqlText[i])
	return i + 1
}

func (s *e2eSQLStatementSplitter) consumeSingleQuote(sqlText string, i int) int {
	ch := sqlText[i]
	s.current.WriteByte(ch)
	if ch == '\'' {
		if i+1 < len(sqlText) && sqlText[i+1] == '\'' {
			s.current.WriteByte(sqlText[i+1])
			return i + 2
		}
		s.inSingleQuote = false
	}
	return i + 1
}

func (s *e2eSQLStatementSplitter) consumeOutsideQuote(sqlText string, i int) int {
	ch := sqlText[i]
	if ch == '\'' {
		s.inSingleQuote = true
		s.current.WriteByte(ch)
		return i + 1
	}
	if ch == '-' && i+1 < len(sqlText) && sqlText[i+1] == '-' {
		for i < len(sqlText) && sqlText[i] != '\n' {
			s.current.WriteByte(sqlText[i])
			i++
		}
		return i
	}
	if ch == '$' {
		if tag, ok := e2eDollarQuoteTag(sqlText[i:]); ok {
			s.dollarQuoteTag = tag
			s.current.WriteString(tag)
			return i + len(tag)
		}
	}
	s.current.WriteByte(ch)
	if ch == ';' && !s.inSingleQuote {
		s.finishStatement()
	}
	return i + 1
}

func (s *e2eSQLStatementSplitter) finishStatement() {
	if statement := strings.TrimSpace(s.current.String()); statement != "" {
		s.statements = append(s.statements, statement)
	}
	s.current.Reset()
}
'''
 # raw 字符串用于保留 Go rune 与转义；只把缩进和 rune 转义恢复为 Go 源码。
 helpers=helpers.replace('\\t','\t').replace("'\\\\''", "'\\''").replace("'\\\\n'", "'\\n'")
 return replace_function(s,name,body,helpers)
apply(path,name,splitter)

path='services/api-gateway/internal/server/e2e_story056_test.go';name='TestStory056ObjectiveScoringRecoveryE2EWithPostgresTestDatabase'
def finalization(s,body):
 start=body.index('\n\tfinals, err := score.NewPostgresStore');phase=body[start:]
 body=body[:start]+'\n\te2eFinalizeAndReprocessStory056(t, db, router, adminToken, fixture, suffix, completedItems)\n'
 helper='\n\n// 全部自动/人工确认后才能形成最终成绩；重做只覆盖指定答案且保留原幂等键和版本检查。\nfunc e2eFinalizeAndReprocessStory056(t *testing.T, db *sql.DB, router http.Handler, adminToken string, fixture story056AcceptanceFixture, suffix string, completedItems []map[string]any) {\n\tt.Helper()\n\tfileStore := files.NewPostgresStore(db)'+phase+'}'
 return replace_function(s,name,body,helper)
apply(path,name,finalization)
(report/'go-server-stages9.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
