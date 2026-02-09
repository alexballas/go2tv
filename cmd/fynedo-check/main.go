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

// ALL UI widget methods that require fyne.Do when called from goroutines
// These are methods on fyne widget types: Button, Entry, Label, Slider, Check, Select, List, Card, Icon
// Also binding.String.Set()
var dangerousMethods = map[string]bool{
	"SetText": true, "SetIcon": true, "Enable": true, "Disable": true,
	"SetValue": true, "SetPlaceHolder": true, "SetChecked": true,
	"ClearSelected": true, "Unselect": true, "UnselectAll": true,
	"Select": true, "Hide": true, "Show": true, "Refresh": true, "Set": true,
}

// Helper function names that are known to use fyne.Do internally
// These won't be flagged when called from goroutines
var safeHelpers = map[string]bool{
	"setMuteUnmuteView":            true,
	"setPlayPauseView":             true,
	"check":                        true,
	"updateScreenState":            true,
	"startAfreshPlayButton":        true,
	"checkChromecastCompatibility": true,
	"autoSelectNextSubs":           true,
	"silentCheckVersion":           true,
	"checkVersion":                 true,
	"showVersionPopup":             true,
	"refreshDevList":               true,
	"checkMutefunc":                true,
	"sliderUpdate":                 true,
	"chromecastStatusWatcher":      true,
	"chromecastPlayAction":         true,
	"gaplessMediaWatcher":          true,
	"GaplessMediaWatcher":          true,
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
	decl *ast.FuncDecl
	ctx  analysisContext
}

type visitKey struct {
	decl         *ast.FuncDecl
	insideFyneDo bool
}

func main() {
	guiDir := "internal/gui"
	if len(os.Args) > 1 {
		guiDir = os.Args[1]
	}

	var violations []Violation
	var files []fileData

	// Walk GUI directory and parse files first.
	err := filepath.Walk(guiDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}

		importAliases, fyneAliases, dotImportFyne := collectImportInfo(node)
		files = append(files, fileData{
			file: node,
			ctx: analysisContext{
				filePath:      path,
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
			meta := FuncMeta{decl: fn, ctx: fd.ctx}
			if fn.Recv == nil {
				functions[fn.Name.Name] = append(functions[fn.Name.Name], meta)
				continue
			}
			methods[fn.Name.Name] = append(methods[fn.Name.Name], meta)
		}
	}

	for _, fd := range files {
		v := &visitor{
			ctx:         fd.ctx,
			violations:  &violations,
			seen:        seenViolations,
			safeHelpers: safeHelpers,
			functions:   functions,
			methods:     methods,
			checked:     map[visitKey]bool{},
		}
		ast.Inspect(fd.file, v.visit)
	}

	// Report
	if len(violations) == 0 {
		fmt.Println("✅ No fyne.Do violations found!")
		os.Exit(0)
	}

	fmt.Printf("\n❌ Found %d fyne.Do violations:\n\n", len(violations))
	for _, v := range violations {
		fmt.Printf("%s:%d\n  %s.%s()\n\n", v.File, v.Line, v.Widget, v.Method)
	}
	os.Exit(1)
}

type visitor struct {
	ctx         analysisContext
	violations  *[]Violation
	seen        map[string]bool
	safeHelpers map[string]bool
	functions   map[string][]FuncMeta
	methods     map[string][]FuncMeta
	checked     map[visitKey]bool
}

func (v *visitor) visit(n ast.Node) bool {
	// Find go statements
	if goStmt, ok := n.(*ast.GoStmt); ok {
		v.checkGoroutine(goStmt.Call, false, v.ctx, map[*ast.FuncDecl]bool{})
	}
	return true
}

func (v *visitor) checkGoroutine(expr ast.Expr, insideFyneDo bool, ctx analysisContext, stack map[*ast.FuncDecl]bool) {
	switch e := expr.(type) {
	case *ast.FuncLit:
		// go func() { ... }
		v.checkBody(e.Body, insideFyneDo, ctx, stack)
	case *ast.CallExpr:
		// go func() { ... }()  OR  go someFunc()
		if fnLit, ok := e.Fun.(*ast.FuncLit); ok {
			// Anonymous function being called immediately: go func() { ... }()
			v.checkBody(fnLit.Body, insideFyneDo, ctx, stack)
		} else {
			// Named/function call: go someFunc(), go obj.Method()
			if v.isSafeHelper(e) {
				return
			}
			v.checkCallTargets(e, insideFyneDo, ctx, stack)
		}
	}
}

func (v *visitor) checkBody(body *ast.BlockStmt, insideFyneDo bool, ctx analysisContext, stack map[*ast.FuncDecl]bool) {
	if body == nil {
		return
	}
	for _, stmt := range body.List {
		v.checkStmt(stmt, insideFyneDo, ctx, stack)
	}
}

func (v *visitor) checkStmt(stmt ast.Stmt, insideFyneDo bool, ctx analysisContext, stack map[*ast.FuncDecl]bool) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			v.checkCallExpr(call, insideFyneDo, ctx, stack)
		}
	case *ast.BlockStmt:
		v.checkBody(s, insideFyneDo, ctx, stack)
	case *ast.IfStmt:
		v.checkStmt(s.Body, insideFyneDo, ctx, stack)
		if s.Else != nil {
			v.checkStmt(s.Else, insideFyneDo, ctx, stack)
		}
	case *ast.ForStmt:
		v.checkStmt(s.Body, insideFyneDo, ctx, stack)
	case *ast.RangeStmt:
		v.checkStmt(s.Body, insideFyneDo, ctx, stack)
	case *ast.SwitchStmt:
		v.checkStmt(s.Body, insideFyneDo, ctx, stack)
	case *ast.TypeSwitchStmt:
		v.checkStmt(s.Body, insideFyneDo, ctx, stack)
	case *ast.SelectStmt:
		v.checkStmt(s.Body, insideFyneDo, ctx, stack)
	case *ast.CaseClause:
		for _, bodyStmt := range s.Body {
			v.checkStmt(bodyStmt, insideFyneDo, ctx, stack)
		}
	case *ast.CommClause:
		for _, bodyStmt := range s.Body {
			v.checkStmt(bodyStmt, insideFyneDo, ctx, stack)
		}
	case *ast.GoStmt:
		// Nested goroutine - check recursively (not inside fyne.Do)
		v.checkGoroutine(s.Call, false, ctx, stack)
	case *ast.AssignStmt:
		// Check RHS of assignments for function calls
		for _, expr := range s.Rhs {
			if call, ok := expr.(*ast.CallExpr); ok {
				v.checkCallExpr(call, insideFyneDo, ctx, stack)
			}
		}
	case *ast.DeferStmt:
		v.checkCallExpr(s.Call, insideFyneDo, ctx, stack)
	}
}

func (v *visitor) checkCallExpr(call *ast.CallExpr, insideFyneDo bool, ctx analysisContext, stack map[*ast.FuncDecl]bool) {
	// Check if this is fyne.Do or fyne.DoAndWait
	if v.isFyneDo(call, ctx) {
		// Arguments to fyne.Do are safe
		for _, arg := range call.Args {
			if fn, ok := arg.(*ast.FuncLit); ok {
				v.checkBody(fn.Body, true, ctx, stack)
			}
		}
		return
	}

	// Check for safe helper call - don't flag or recurse
	if v.isSafeHelper(call) {
		return
	}

	// If not inside fyne.Do, check for dangerous widget calls
	if !insideFyneDo {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			method := sel.Sel.Name
			if dangerousMethods[method] {
				pos := ctx.fset.Position(call.Pos())
				widget := v.getReceiver(sel.X)
				v.addViolation(Violation{
					File:   ctx.filePath,
					Line:   pos.Line,
					Widget: widget,
					Method: method,
				})
			}
		}
	}

	v.checkCallTargets(call, insideFyneDo, ctx, stack)

	// Recursively check arguments
	for _, arg := range call.Args {
		switch a := arg.(type) {
		case *ast.FuncLit:
			v.checkBody(a.Body, insideFyneDo, ctx, stack)
		case *ast.CallExpr:
			v.checkCallExpr(a, insideFyneDo, ctx, stack)
		}
	}
}

func (v *visitor) checkCallTargets(call *ast.CallExpr, insideFyneDo bool, ctx analysisContext, stack map[*ast.FuncDecl]bool) {
	for _, target := range v.resolveCallTargets(call, ctx) {
		v.checkFunction(target, insideFyneDo, stack)
	}
}

func (v *visitor) addViolation(violation Violation) {
	key := fmt.Sprintf("%s:%d:%s:%s", violation.File, violation.Line, violation.Widget, violation.Method)
	if v.seen[key] {
		return
	}
	v.seen[key] = true
	*v.violations = append(*v.violations, violation)
}

func (v *visitor) resolveCallTargets(call *ast.CallExpr, ctx analysisContext) []FuncMeta {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return v.functions[fn.Name]
	case *ast.SelectorExpr:
		if isPackageSelector(fn, ctx) {
			return nil
		}
		if !shouldResolveLocalMethod(fn) {
			return nil
		}
		return v.methods[fn.Sel.Name]
	default:
		return nil
	}
}

func (v *visitor) checkFunction(meta FuncMeta, insideFyneDo bool, stack map[*ast.FuncDecl]bool) {
	if meta.decl == nil || meta.decl.Body == nil {
		return
	}
	key := visitKey{
		decl:         meta.decl,
		insideFyneDo: insideFyneDo,
	}
	if v.checked[key] || stack[meta.decl] {
		return
	}
	v.checked[key] = true
	stack[meta.decl] = true
	v.checkBody(meta.decl.Body, insideFyneDo, meta.ctx, stack)
	delete(stack, meta.decl)
}

func (v *visitor) isFyneDo(call *ast.CallExpr, ctx analysisContext) bool {
	// Check only fyne.Do(), fyne.DoAndWait() (or dot-import equivalent).
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

func (v *visitor) isSafeHelper(call *ast.CallExpr) bool {
	var name string
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		name = sel.Sel.Name
	} else if ident, ok := call.Fun.(*ast.Ident); ok {
		name = ident.Name
	}
	return v.safeHelpers[name]
}

func (v *visitor) getReceiver(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return v.getReceiver(e.X) + "." + e.Sel.Name
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

func shouldResolveLocalMethod(sel *ast.SelectorExpr) bool {
	receiver := getReceiverName(sel.X)
	if receiver == "" {
		return false
	}
	parts := strings.Split(receiver, ".")
	last := parts[len(parts)-1]
	return last == "screen" || last == "s" || last == "p"
}

func getReceiverName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		left := getReceiverName(e.X)
		if left == "" {
			return ""
		}
		return left + "." + e.Sel.Name
	default:
		return ""
	}
}
