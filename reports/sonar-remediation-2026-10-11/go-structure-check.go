// 本地结构估算用于挑选重构阶段；最终告警状态仍以同规则的 SonarQube 复扫为准。
package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
)

type functionSummary struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Line int `json:"line"`
	Estimate int `json:"cognitive_estimate"`
	Parameters int `json:"parameters"`
}

func logicalSequence(node ast.Expr) int {
	sequence := []token.Token{}
	var visit func(ast.Expr)
	visit = func(expr ast.Expr) {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			visit(n.X)
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				visit(n.X)
				sequence = append(sequence, n.Op)
				visit(n.Y)
			}
		}
	}
	visit(node)
	count := 0
	previous := token.ILLEGAL
	for _, op := range sequence {
		if op != previous { count++; previous = op }
	}
	return count
}

func complexity(node ast.Node, nesting int) int {
	if node == nil { return 0 }
	sum := 0
	ast.Inspect(node, func(child ast.Node) bool {
		if child == nil { return false }
		switch n := child.(type) {
		case *ast.IfStmt:
			sum += 1 + nesting + complexity(n.Init, nesting) + complexity(n.Cond, nesting) + complexity(n.Body, nesting+1)
			if n.Else != nil {
				sum++
				if other, ok := n.Else.(*ast.IfStmt); ok {
					sum += complexity(other.Init, nesting) + complexity(other.Cond, nesting) + complexity(other.Body, nesting+1)
					if other.Else != nil { sum += complexity(other.Else, nesting+1) }
				} else { sum += complexity(n.Else, nesting+1) }
			}
			return false
		case *ast.ForStmt:
			sum += 1 + nesting + complexity(n.Init, nesting) + complexity(n.Cond, nesting) + complexity(n.Post, nesting) + complexity(n.Body, nesting+1)
			return false
		case *ast.RangeStmt:
			sum += 1 + nesting + complexity(n.X, nesting) + complexity(n.Body, nesting+1)
			return false
		case *ast.SwitchStmt:
			sum += 1 + nesting + complexity(n.Init, nesting) + complexity(n.Tag, nesting) + complexity(n.Body, nesting+1)
			return false
		case *ast.TypeSwitchStmt:
			sum += 1 + nesting + complexity(n.Init, nesting) + complexity(n.Assign, nesting) + complexity(n.Body, nesting+1)
			return false
		case *ast.SelectStmt:
			sum += 1 + nesting + complexity(n.Body, nesting+1)
			return false
		case *ast.FuncLit:
			sum += complexity(n.Body, nesting+1)
			return false
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				sum += logicalSequence(n)
				return false
			}
		case *ast.BranchStmt:
			if n.Label != nil { sum++ }
		}
		return true
	})
	return sum
}

func main() {
	var paths []string
	if err := json.NewDecoder(os.Stdin).Decode(&paths); err != nil { panic(err) }
	summaries := []functionSummary{}
	for _, path := range paths {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil { panic(err) }
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil { continue }
			params := 0
			for _, field := range fn.Type.Params.List {
				if len(field.Names) == 0 { params++ } else { params += len(field.Names) }
			}
			summaries = append(summaries, functionSummary{path, fn.Name.Name, fset.Position(fn.Pos()).Line, complexity(fn.Body,0), params})
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(summaries); err != nil { panic(err) }
}
