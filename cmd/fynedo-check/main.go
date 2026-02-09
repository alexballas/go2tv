package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// UI methods that mutate visible state and should run in fyne.Do when in concurrent contexts.
var dangerousMethods = map[string]bool{
	"Append":                 true,
	"ClearSelected":          true,
	"Disable":                true,
	"DisableIndex":           true,
	"DisableItem":            true,
	"Enable":                 true,
	"EnableIndex":            true,
	"EnableItem":             true,
	"Hide":                   true,
	"Move":                   true,
	"Prepend":                true,
	"Refresh":                true,
	"RefreshItem":            true,
	"Resize":                 true,
	"Select":                 true,
	"SelectIndex":            true,
	"SelectTab":              true,
	"SelectTabIndex":         true,
	"Set":                    true, // binding.Set
	"SetCell":                true,
	"SetChecked":             true,
	"SetColumnWidth":         true,
	"SetContent":             true,
	"SetCurrentTitle":        true,
	"SetDate":                true,
	"SetIcon":                true,
	"SetImage":               true,
	"SetItemHeight":          true,
	"SetItems":               true,
	"SetLocation":            true,
	"SetMaximized":           true,
	"SetMinSize":             true,
	"SetOffset":              true,
	"SetOptions":             true,
	"SetPadded":              true,
	"SetPlaceHolder":         true,
	"SetPlaceholder":         true,
	"SetResource":            true,
	"SetRow":                 true,
	"SetRowHeight":           true,
	"SetRowStyle":            true,
	"SetRune":                true,
	"SetSelected":            true,
	"SetSelectedIndex":       true,
	"SetStyle":               true,
	"SetStyleRange":          true,
	"SetSubTitle":            true,
	"SetTabLocation":         true,
	"SetText":                true,
	"SetTitle":               true,
	"SetTitleText":           true,
	"SetURI":                 true,
	"SetURL":                 true,
	"SetURLFromString":       true,
	"SetValidationError":     true,
	"SetValue":               true,
	"SetView":                true,
	"Show":                   true,
	"ShowAtPosition":         true,
	"ShowAtRelativePosition": true,
	"Unselect":               true,
	"UnselectAll":            true,
}

// Field writes on visible widgets can race too (e.g. label.Text = "...").
var dangerousFields = map[string]bool{
	"Text":        true,
	"Icon":        true,
	"Hidden":      true,
	"Checked":     true,
	"Value":       true,
	"PlaceHolder": true,
	"Placeholder": true,
	"Options":     true,
	"Selected":    true,
	"Resource":    true,
	"Title":       true,
	"SubTitle":    true,
}

// Methods on fyne.Widget/fyne.CanvasObject interface references.
var interfaceDangerousMethods = map[string]bool{
	"Hide":       true,
	"Move":       true,
	"Refresh":    true,
	"Resize":     true,
	"SetMinSize": true,
	"Show":       true,
}

var asyncInvokerNames = map[string]bool{
	"AfterFunc": true,
	"Go":        true,
	"Start":     true,
	"Submit":    true,
}

type Violation struct {
	File   string
	Line   int
	Widget string
	Method string
}

type analysisContext struct {
	filePath      string
	fset          *token.FileSet
	importAliases map[string]bool
	fyneAliases   map[string]bool
	dotImportFyne bool
}

type fileData struct {
	file *ast.File
	ctx  analysisContext
}

type FuncMeta struct {
	decl            *ast.FuncDecl
	ctx             analysisContext
	uiInterfaceVars map[string]bool
}

type visitKey struct {
	decl         *ast.FuncDecl
	insideFyneDo bool
	inConcurrent bool
}

func main() {
	guiDir := "internal/gui"
	if len(os.Args) > 1 {
		guiDir = os.Args[1]
	}

	var violations []Violation
	var files []fileData

	err := filepath.Walk(guiDir, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		node, parseErr := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if parseErr != nil {
			return nil
		}

		importAliases, fyneAliases, dotImportFyne := collectImportInfo(node)
		files = append(files, fileData{
			file: node,
			ctx: analysisContext{
				filePath:      p,
				fset:          fset,
				importAliases: importAliases,
				fyneAliases:   fyneAliases,
				dotImportFyne: dotImportFyne,
			},
		})
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	functions := map[string][]FuncMeta{}
	methods := map[string][]FuncMeta{}
	seenViolations := map[string]bool{}
	for _, fd := range files {
		for _, decl := range fd.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}

			meta := FuncMeta{
				decl:            fn,
				ctx:             fd.ctx,
				uiInterfaceVars: collectFuncDeclUIVars(fn, fd.ctx),
			}
			if fn.Recv == nil {
				functions[fn.Name.Name] = append(functions[fn.Name.Name], meta)
				continue
			}
			methods[fn.Name.Name] = append(methods[fn.Name.Name], meta)
		}
	}

	for _, fd := range files {
		v := &visitor{
			ctx:        fd.ctx,
			violations: &violations,
			seen:       seenViolations,
			functions:  functions,
			methods:    methods,
			checked:    map[visitKey]bool{},
		}
		ast.Inspect(fd.file, v.visit)
	}

	if len(violations) == 0 {
		fmt.Println("✅ No fyne.Do violations found!")
		os.Exit(0)
	}

	fmt.Printf("\n❌ Found %d fyne.Do violations:\n\n", len(violations))
	for _, violation := range violations {
		fmt.Printf("%s:%d\n  %s.%s\n\n", violation.File, violation.Line, violation.Widget, violation.Method)
	}
	os.Exit(1)
}

type visitor struct {
	ctx        analysisContext
	violations *[]Violation
	seen       map[string]bool
	functions  map[string][]FuncMeta
	methods    map[string][]FuncMeta
	checked    map[visitKey]bool
}

func (v *visitor) visit(n ast.Node) bool {
	if goStmt, ok := n.(*ast.GoStmt); ok {
		v.checkGoroutine(goStmt.Call, false, true, v.ctx, map[string]bool{}, map[*ast.FuncDecl]bool{})
	}

	// More aggressive: detect async invokers even outside explicit "go".
	if call, ok := n.(*ast.CallExpr); ok && isAsyncInvokerCall(call) {
		for _, arg := range call.Args {
			fn, ok := arg.(*ast.FuncLit)
			if !ok {
				continue
			}
			uiVars := collectFuncLitUIVars(fn, v.ctx)
			v.checkBody(fn.Body, false, true, v.ctx, uiVars, map[*ast.FuncDecl]bool{})
		}
	}

	return true
}

func (v *visitor) checkGoroutine(expr ast.Expr, insideFyneDo bool, inConcurrent bool, ctx analysisContext, uiVars map[string]bool, stack map[*ast.FuncDecl]bool) {
	switch e := expr.(type) {
	case *ast.FuncLit:
		localUIVars := mergeUIVars(uiVars, collectFuncLitUIVars(e, ctx))
		v.checkBody(e.Body, insideFyneDo, inConcurrent, ctx, localUIVars, stack)
	case *ast.CallExpr:
		if fnLit, ok := e.Fun.(*ast.FuncLit); ok {
			localUIVars := mergeUIVars(uiVars, collectFuncLitUIVars(fnLit, ctx))
			v.checkBody(fnLit.Body, insideFyneDo, inConcurrent, ctx, localUIVars, stack)
			return
		}
		v.checkCallTargets(e, insideFyneDo, inConcurrent, ctx, uiVars, stack)
	}
}

func (v *visitor) checkBody(body *ast.BlockStmt, insideFyneDo bool, inConcurrent bool, ctx analysisContext, uiVars map[string]bool, stack map[*ast.FuncDecl]bool) {
	if body == nil {
		return
	}
	localUIVars := mergeUIVars(uiVars, collectUIVarsInBlock(body, ctx))
	for _, stmt := range body.List {
		v.checkStmt(stmt, insideFyneDo, inConcurrent, ctx, localUIVars, stack)
	}
}

func (v *visitor) checkStmt(stmt ast.Stmt, insideFyneDo bool, inConcurrent bool, ctx analysisContext, uiVars map[string]bool, stack map[*ast.FuncDecl]bool) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			v.checkCallExpr(call, insideFyneDo, inConcurrent, ctx, uiVars, stack)
		}
	case *ast.BlockStmt:
		v.checkBody(s, insideFyneDo, inConcurrent, ctx, uiVars, stack)
	case *ast.IfStmt:
		v.checkStmt(s.Body, insideFyneDo, inConcurrent, ctx, uiVars, stack)
		if s.Else != nil {
			v.checkStmt(s.Else, insideFyneDo, inConcurrent, ctx, uiVars, stack)
		}
	case *ast.ForStmt:
		v.checkStmt(s.Body, insideFyneDo, inConcurrent, ctx, uiVars, stack)
	case *ast.RangeStmt:
		v.checkStmt(s.Body, insideFyneDo, inConcurrent, ctx, uiVars, stack)
	case *ast.SwitchStmt:
		v.checkStmt(s.Body, insideFyneDo, inConcurrent, ctx, uiVars, stack)
	case *ast.TypeSwitchStmt:
		v.checkStmt(s.Body, insideFyneDo, inConcurrent, ctx, uiVars, stack)
	case *ast.SelectStmt:
		v.checkStmt(s.Body, insideFyneDo, inConcurrent, ctx, uiVars, stack)
	case *ast.CaseClause:
		for _, caseStmt := range s.Body {
			v.checkStmt(caseStmt, insideFyneDo, inConcurrent, ctx, uiVars, stack)
		}
	case *ast.CommClause:
		for _, commStmt := range s.Body {
			v.checkStmt(commStmt, insideFyneDo, inConcurrent, ctx, uiVars, stack)
		}
	case *ast.GoStmt:
		v.checkGoroutine(s.Call, false, true, ctx, uiVars, stack)
	case *ast.AssignStmt:
		if inConcurrent && !insideFyneDo {
			v.checkFieldAssignments(s.Lhs, ctx, uiVars)
		}
		for _, expr := range s.Rhs {
			if call, ok := expr.(*ast.CallExpr); ok {
				v.checkCallExpr(call, insideFyneDo, inConcurrent, ctx, uiVars, stack)
			}
		}
	case *ast.IncDecStmt:
		if inConcurrent && !insideFyneDo {
			v.checkFieldAssignments([]ast.Expr{s.X}, ctx, uiVars)
		}
	case *ast.DeferStmt:
		v.checkCallExpr(s.Call, insideFyneDo, inConcurrent, ctx, uiVars, stack)
	}
}

func (v *visitor) checkCallExpr(call *ast.CallExpr, insideFyneDo bool, inConcurrent bool, ctx analysisContext, uiVars map[string]bool, stack map[*ast.FuncDecl]bool) {
	if v.isFyneDo(call, ctx) {
		for _, arg := range call.Args {
			fn, ok := arg.(*ast.FuncLit)
			if !ok {
				continue
			}
			localUIVars := mergeUIVars(uiVars, collectFuncLitUIVars(fn, ctx))
			v.checkBody(fn.Body, true, inConcurrent, ctx, localUIVars, stack)
		}
		return
	}

	if inConcurrent && !insideFyneDo {
		v.checkDangerousCall(call, ctx, uiVars)
	}

	if isAsyncInvokerCall(call) {
		for _, arg := range call.Args {
			fn, ok := arg.(*ast.FuncLit)
			if !ok {
				continue
			}
			localUIVars := mergeUIVars(uiVars, collectFuncLitUIVars(fn, ctx))
			v.checkBody(fn.Body, false, true, ctx, localUIVars, stack)
		}
	}

	v.checkCallTargets(call, insideFyneDo, inConcurrent, ctx, uiVars, stack)

	for _, arg := range call.Args {
		switch a := arg.(type) {
		case *ast.FuncLit:
			localUIVars := mergeUIVars(uiVars, collectFuncLitUIVars(a, ctx))
			v.checkBody(a.Body, insideFyneDo, inConcurrent, ctx, localUIVars, stack)
		case *ast.CallExpr:
			v.checkCallExpr(a, insideFyneDo, inConcurrent, ctx, uiVars, stack)
		}
	}
}

func (v *visitor) checkDangerousCall(call *ast.CallExpr, ctx analysisContext, uiVars map[string]bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}

	method := sel.Sel.Name
	if dangerousMethods[method] || (interfaceDangerousMethods[method] && v.isFyneInterfaceReceiver(sel.X, uiVars)) {
		pos := ctx.fset.Position(call.Pos())
		widget := v.getReceiver(sel.X)
		v.addViolation(Violation{
			File:   ctx.filePath,
			Line:   pos.Line,
			Widget: widget,
			Method: method + "()",
		})
	}
}

func (v *visitor) checkFieldAssignments(lhs []ast.Expr, ctx analysisContext, uiVars map[string]bool) {
	for _, target := range lhs {
		sel, ok := target.(*ast.SelectorExpr)
		if !ok {
			continue
		}

		field := sel.Sel.Name
		if !dangerousFields[field] && !v.isFyneInterfaceReceiver(sel.X, uiVars) {
			continue
		}

		pos := ctx.fset.Position(sel.Pos())
		widget := v.getReceiver(sel.X)
		v.addViolation(Violation{
			File:   ctx.filePath,
			Line:   pos.Line,
			Widget: widget,
			Method: field + " =",
		})
	}
}

func (v *visitor) checkCallTargets(call *ast.CallExpr, insideFyneDo bool, inConcurrent bool, ctx analysisContext, uiVars map[string]bool, stack map[*ast.FuncDecl]bool) {
	for _, target := range v.resolveCallTargets(call, ctx) {
		v.checkFunction(target, insideFyneDo, inConcurrent, stack)
	}
}

func (v *visitor) resolveCallTargets(call *ast.CallExpr, ctx analysisContext) []FuncMeta {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return v.functions[fn.Name]
	case *ast.SelectorExpr:
		if isPackageSelector(fn, ctx) {
			return nil
		}
		// Avoid broad cross-type false positives from nested selectors
		// (e.g. external widget methods like s.PlayPause.Tapped).
		if _, ok := fn.X.(*ast.Ident); !ok {
			return nil
		}
		return v.methods[fn.Sel.Name]
	default:
		return nil
	}
}

func (v *visitor) checkFunction(meta FuncMeta, insideFyneDo bool, inConcurrent bool, stack map[*ast.FuncDecl]bool) {
	if meta.decl == nil || meta.decl.Body == nil {
		return
	}

	key := visitKey{
		decl:         meta.decl,
		insideFyneDo: insideFyneDo,
		inConcurrent: inConcurrent,
	}
	if v.checked[key] || stack[meta.decl] {
		return
	}

	v.checked[key] = true
	stack[meta.decl] = true
	v.checkBody(meta.decl.Body, insideFyneDo, inConcurrent, meta.ctx, meta.uiInterfaceVars, stack)
	delete(stack, meta.decl)
}

func (v *visitor) isFyneDo(call *ast.CallExpr, ctx analysisContext) bool {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if sel.Sel.Name != "Do" && sel.Sel.Name != "DoAndWait" {
			return false
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return false
		}
		return ctx.fyneAliases[ident.Name]
	}

	if ident, ok := call.Fun.(*ast.Ident); ok {
		if ident.Name != "Do" && ident.Name != "DoAndWait" {
			return false
		}
		return ctx.dotImportFyne
	}

	return false
}

func (v *visitor) addViolation(violation Violation) {
	key := fmt.Sprintf("%s:%d:%s:%s", violation.File, violation.Line, violation.Widget, violation.Method)
	if v.seen[key] {
		return
	}
	v.seen[key] = true
	*v.violations = append(*v.violations, violation)
}

func (v *visitor) isFyneInterfaceReceiver(expr ast.Expr, uiVars map[string]bool) bool {
	root := getRootIdent(expr)
	if root == "" {
		return false
	}
	return uiVars[root]
}

func (v *visitor) getReceiver(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		left := v.getReceiver(e.X)
		if left == "" {
			return e.Sel.Name
		}
		return left + "." + e.Sel.Name
	default:
		return "?"
	}
}

func collectImportInfo(file *ast.File) (map[string]bool, map[string]bool, bool) {
	importAliases := map[string]bool{}
	fyneAliases := map[string]bool{}
	dotImportFyne := false

	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, "\"")
		if importPath == "" {
			continue
		}

		var alias string
		if spec.Name != nil {
			alias = spec.Name.Name
		} else {
			alias = defaultImportName(importPath)
		}

		if alias != "" && alias != "_" && alias != "." {
			importAliases[alias] = true
		}

		if importPath != "fyne.io/fyne/v2" {
			continue
		}
		if spec.Name == nil {
			fyneAliases["fyne"] = true
			continue
		}
		if spec.Name.Name == "." {
			dotImportFyne = true
			continue
		}
		if spec.Name.Name != "_" {
			fyneAliases[spec.Name.Name] = true
		}
	}

	return importAliases, fyneAliases, dotImportFyne
}

func collectFuncDeclUIVars(fn *ast.FuncDecl, ctx analysisContext) map[string]bool {
	vars := map[string]bool{}
	collectTypeVarsFromFieldList(vars, fn.Type.Params, ctx)
	collectTypeVarsFromBlock(vars, fn.Body, ctx)
	return vars
}

func collectFuncLitUIVars(fn *ast.FuncLit, ctx analysisContext) map[string]bool {
	vars := map[string]bool{}
	collectTypeVarsFromFieldList(vars, fn.Type.Params, ctx)
	collectTypeVarsFromBlock(vars, fn.Body, ctx)
	return vars
}

func collectUIVarsInBlock(body *ast.BlockStmt, ctx analysisContext) map[string]bool {
	vars := map[string]bool{}
	collectTypeVarsFromBlock(vars, body, ctx)
	return vars
}

func collectTypeVarsFromBlock(vars map[string]bool, body *ast.BlockStmt, ctx analysisContext) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.ValueSpec:
			if !isFyneInterfaceType(s.Type, ctx) {
				return true
			}
			for _, name := range s.Names {
				if name != nil && name.Name != "_" {
					vars[name.Name] = true
				}
			}
		case *ast.AssignStmt:
			for i, rhs := range s.Rhs {
				assertion, ok := rhs.(*ast.TypeAssertExpr)
				if !ok || !isFyneInterfaceType(assertion.Type, ctx) {
					continue
				}
				if i >= len(s.Lhs) {
					continue
				}
				if ident, ok := s.Lhs[i].(*ast.Ident); ok && ident.Name != "_" {
					vars[ident.Name] = true
				}
			}
		}
		return true
	})
}

func collectTypeVarsFromFieldList(vars map[string]bool, fields *ast.FieldList, ctx analysisContext) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		if !isFyneInterfaceType(field.Type, ctx) {
			continue
		}
		for _, name := range field.Names {
			if name == nil || name.Name == "_" {
				continue
			}
			vars[name.Name] = true
		}
	}
}

func isFyneInterfaceType(expr ast.Expr, ctx analysisContext) bool {
	switch t := expr.(type) {
	case *ast.ParenExpr:
		return isFyneInterfaceType(t.X, ctx)
	case *ast.SelectorExpr:
		ident, ok := t.X.(*ast.Ident)
		if !ok || !ctx.fyneAliases[ident.Name] {
			return false
		}
		return t.Sel.Name == "Widget" || t.Sel.Name == "CanvasObject"
	case *ast.Ident:
		if !ctx.dotImportFyne {
			return false
		}
		return t.Name == "Widget" || t.Name == "CanvasObject"
	default:
		return false
	}
}

func mergeUIVars(base map[string]bool, extra map[string]bool) map[string]bool {
	if len(base) == 0 && len(extra) == 0 {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func getRootIdent(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return getRootIdent(e.X)
	case *ast.IndexExpr:
		return getRootIdent(e.X)
	case *ast.StarExpr:
		return getRootIdent(e.X)
	case *ast.ParenExpr:
		return getRootIdent(e.X)
	default:
		return ""
	}
}

func isAsyncInvokerCall(call *ast.CallExpr) bool {
	if !hasFuncLitArg(call) {
		return false
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		return asyncInvokerNames[sel.Sel.Name]
	}
	if ident, ok := call.Fun.(*ast.Ident); ok {
		return asyncInvokerNames[ident.Name]
	}
	return false
}

func hasFuncLitArg(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		if _, ok := arg.(*ast.FuncLit); ok {
			return true
		}
	}
	return false
}

var semverSuffix = regexp.MustCompile(`^v[0-9]+$`)

func defaultImportName(importPath string) string {
	base := path.Base(importPath)
	if semverSuffix.MatchString(base) {
		return path.Base(path.Dir(importPath))
	}
	return base
}

func isPackageSelector(sel *ast.SelectorExpr, ctx analysisContext) bool {
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ctx.importAliases[ident.Name]
}
