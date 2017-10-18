package translator

import (
	"fmt"
	"go/ast"
	"go/types"
	"path"
	"reflect"
	"strconv"
	"strings"

	"golang.org/x/tools/go/loader"

	"github.com/apex/log"
	"github.com/matthewmueller/golly/golang/db"
	"github.com/matthewmueller/golly/golang/def"
	"github.com/matthewmueller/golly/golang/def/fn"
	"github.com/matthewmueller/golly/golang/def/method"
	"github.com/matthewmueller/golly/golang/def/struc"
	"github.com/matthewmueller/golly/golang/def/value"
	"github.com/matthewmueller/golly/golang/index"
	"github.com/matthewmueller/golly/golang/scope"
	"github.com/matthewmueller/golly/jsast"
	"github.com/pkg/errors"
)

// context struct
type context struct {
	index *db.DB
	info  *loader.PackageInfo
	def   def.Definition
}

// Result of the translation
type Result struct {
	Node         jsast.INode
	Exported     bool
	Dependencies []string
}

// Translator struct
type Translator struct {
	// program *loader.Program
	index *index.Index
}

// New fn
func New(index *index.Index) *Translator {
	return &Translator{index}
}

// Translate fn
func (tr *Translator) Translate(d def.Definition) (jsast.INode, error) {
	// sp := scope.New(d.Node())

	// if this declaration has the global option,
	// don't include it in the build
	if d.Omitted() {
		return jsast.CreateEmptyStatement(), nil
	}

	// NOTE: order matters here
	// e.g. Method can also be a Function
	switch t := d.(type) {
	case method.Method:
		return tr.methods(t)
	case fn.Function:
		return tr.functions(t)
	// case iface.Interface:
	// 	return tr.Interface(t, sp)
	case struc.Struct:
		return tr.structs(t)
	case value.Value:
		return tr.values(t)
	default:
		return nil, unhandled("Translate", d)
	}
}

// // Function fn
// func (tr *Translator) Function(d fn.Function, sp *scope.Scope) (jsast.INode, error) {
// 	n, ok := d.Node().(*ast.FuncDecl)
// 	if !ok {
// 		return nil, errors.New("Function: expected def.Function's node to be an *ast.FuncDecl")
// 	}

// 	return tr.funcDecl(d, sp, n)
// }

// Function fn
func (tr *Translator) functions(d fn.Function) (jsast.INode, error) {
	n := d.Node()
	sp := scope.New(n)

	// e.g. func hi()
	if n.Body == nil {
		return jsast.CreateEmptyStatement(), nil
	}

	// build argument list
	// var args
	var params []jsast.IPattern
	if n.Type != nil && n.Type.Params != nil {
		fields := n.Type.Params.List
		for _, field := range fields {
			// names because: (a, b string, c int) = [[a, b], c]
			for _, name := range field.Names {
				id := jsast.CreateIdentifier(name.Name)
				params = append(params, id)
			}
		}
	}

	// create the body
	var body []interface{}
	for _, stmt := range n.Body.List {
		jsStmt, e := tr.statement(d, sp, stmt)
		if e != nil {
			return nil, e
		}
		body = append(body, jsStmt)
	}

	fnname := jsast.CreateIdentifier(n.Name.Name)

	// async function
	isAsync, e := d.IsAsync()
	if e != nil {
		return nil, e
	}

	if isAsync {
		return jsast.CreateAsyncFunction(
			&fnname,
			params,
			jsast.CreateFunctionBody(body...),
		), nil
	}

	return jsast.CreateFunction(
		&fnname,
		params,
		jsast.CreateFunctionBody(body...),
	), nil
}

// Method fn
func (tr *Translator) methods(d method.Method) (jsast.INode, error) {
	n := d.Node()
	sp := scope.New(n)

	// e.g. func hi()
	if n.Body == nil {
		return jsast.CreateEmptyStatement(), nil
	}

	// build argument list
	// var args
	var params []jsast.IPattern
	if n.Type != nil && n.Type.Params != nil {
		fields := n.Type.Params.List
		for _, field := range fields {
			// names because: (a, b string, c int) = [[a, b], c]
			for _, name := range field.Names {
				id := jsast.CreateIdentifier(name.Name)
				params = append(params, id)
			}
		}
	}

	// create the body
	var body []interface{}
	for _, stmt := range n.Body.List {
		jsStmt, e := tr.statement(d, sp, stmt)
		if e != nil {
			return nil, e
		}
		body = append(body, jsStmt)
	}

	fnname := jsast.CreateIdentifier(d.Name())

	// if len(n.Recv.List) != 1 {
	// 	return nil, fmt.Errorf("function<recv>: only expected 1 func receiver but got %d", len(n.Recv.List))
	// }

	// remove prototypes where the receiver is global
	if d.Recv().Omitted() {
		return jsast.CreateEmptyStatement(), nil
	}

	// x, e := tr.expression(d, sp, d.Recv())
	// if e != nil {
	// 	return nil, e
	// }

	// if len(recv.Names) > 1 {
	// 	return nil, fmt.Errorf("function<recv>: only expected 1 func receiver name but got %d", len(recv.Names))
	// }

	// Links the function receiver to "this",
	// Placing it on top of the function body
	// e.g. var d = this
	//
	// TODO: be smarter here and rename the inner body variables to "this"

	field := n.Recv.List[0]
	if len(field.Names) == 1 {
		body = append([]interface{}{jsast.CreateVariableDeclaration(
			"var",
			jsast.CreateVariableDeclarator(
				jsast.CreateIdentifier(field.Names[0].Name),
				jsast.CreateThisExpression(),
			),
		)}, body...)
	}

	isAsync, e := d.IsAsync()
	if e != nil {
		return nil, e
	}

	var fn jsast.FunctionExpression
	if isAsync {
		fn = jsast.CreateAsyncFunctionExpression(
			&fnname,
			params,
			jsast.CreateFunctionBody(body...),
		)
	} else {
		fn = jsast.CreateFunctionExpression(
			&fnname,
			params,
			jsast.CreateFunctionBody(body...),
		)
	}

	// {recv}.prototype.{name} = function ({params}) {
	//   {body}
	// }
	return jsast.CreateExpressionStatement(
		jsast.CreateAssignmentExpression(
			jsast.CreateMemberExpression(
				jsast.CreateMemberExpression(
					jsast.CreateIdentifier(d.Recv().Name()),
					jsast.CreateIdentifier("prototype"),
					false,
				),
				fnname,
				false,
			),
			jsast.AssignmentOperator("="),
			fn,
		),
	), nil
}

// // Interface fn
// func (tr *Translator) Interface(d iface.Interface, sp *scope.Scope) (jsast.INode, error) {
// 	n, ok := d.Node().(*ast.TypeSpec)
// 	if !ok {
// 		return nil, errors.New("Interface: expected def.Inferface's node to be an *ast.TypeSpec")
// 	}
// 	_ = n

// 	return nil, nil
// }

// // Struct fn
// func (tr *Translator) Struct(d struc.Struct, sp *scope.Scope) (jsast.INode, error) {
// 	n, ok := d.Node().(*ast.TypeSpec)
// 	if !ok {
// 		return nil, errors.New("Interface: expected def.Struct's node to be an *ast.TypeSpec")
// 	}

// 	return tr.structTypeSpec(d, sp, n)
// }

// Value fn
func (tr *Translator) values(d value.Value) (jsast.INode, error) {
	n := d.Node()
	sp := scope.New(n)

	// handle balanced destructuring
	var vars []jsast.VariableDeclarator
	for i, ident := range n.Names {
		lh := jsast.CreateIdentifier(ident.Name)

		var rh jsast.IExpression
		if i < len(n.Values) {
			r, e := tr.expression(d, sp, n.Values[i])
			if e != nil {
				return nil, e
			}
			rh = r
		} else {
			r, e := tr.defaultValue(d, sp, n.Type)
			if e != nil {
				return nil, e
			}
			rh = r
		}

		v := jsast.CreateVariableDeclarator(lh, rh)
		vars = append(vars, v)
	}

	return jsast.CreateVariableDeclaration("var", vars...), nil
}

func (tr *Translator) statement(d def.Definition, sp *scope.Scope, n ast.Stmt) (j jsast.IStatement, err error) {
	switch t := n.(type) {
	case nil:
		return nil, nil
	case *ast.AssignStmt:
		return tr.assignStatement(d, sp, t)
	case *ast.IncDecStmt:
		return tr.incDecStmt(d, sp, t)
	case *ast.ExprStmt:
		return tr.exprStatement(d, sp, t)
	case *ast.IfStmt:
		return tr.ifStmt(d, sp, t)
	case *ast.BranchStmt:
		return tr.branchStmt(d, sp, t)
	case *ast.ReturnStmt:
		return tr.returnStmt(d, sp, t)
	case *ast.SendStmt:
		return tr.sendStmt(d, sp, t)
	case *ast.BlockStmt:
		return tr.blockStmt(d, sp, t)
	case *ast.DeclStmt:
		return tr.declStmt(d, sp, t)
	case *ast.ForStmt:
		return tr.forStmt(d, sp, t)
	case *ast.RangeStmt:
		return tr.rangeStmt(d, sp, t)
	case *ast.GoStmt:
		return tr.goStmt(d, sp, t)
	default:
		return nil, unhandled("statement", n)
	}
}

// (tr *Translator) func funcDecl(d def.Definition, sp *scope.Scope, n *ast.FuncDecl) (jsast.IStatement, error) {
// 	d, ok := ctx.def.(def.Function)
// 	if !ok {
// 		return nil, errors.New("funcDecl: expected the definition to be a function")
// 	}

// 	// e.g. func hi()
// 	if n.Body == nil {
// 		return jsast.CreateEmptyStatement(), nil
// 	}

// 	// build argument list
// 	// var args
// 	var params []jsast.IPattern
// 	if n.Type != nil && n.Type.Params != nil {
// 		fields := n.Type.Params.List
// 		for _, field := range fields {
// 			// names because: (a, b string, c int) = [[a, b], c]
// 			for _, name := range field.Names {
// 				id := jsast.CreateIdentifier(name.Name)
// 				params = append(params, id)
// 			}
// 		}
// 	}

// 	// create the body
// 	var body []interface{}
// 	for _, stmt := range n.Body.List {
// 		jsStmt, e := tr.funcStatement(d, sp,stmt)
// 		if e != nil {
// 			return nil, e
// 		}
// 		body = append(body, jsStmt)
// 	}

// 	fnname := jsast.CreateIdentifier(n.Name.Name)

// 	// async function
// 	if n.Recv == nil && d.IsAsync() {
// 		return jsast.CreateAsyncFunction(
// 			&fnname,
// 			params,
// 			jsast.CreateFunctionBody(body...),
// 		), nil
// 	}

// 	// regular function
// 	if n.Recv == nil && !d.IsAsync() {
// 		return jsast.CreateFunction(
// 			&fnname,
// 			params,
// 			jsast.CreateFunctionBody(body...),
// 		), nil
// 	}

// 	if len(n.Recv.List) != 1 {
// 		return nil, fmt.Errorf("function<recv>: only expected 1 func receiver but got %d", len(n.Recv.List))
// 	}

// 	recv := n.Recv.List[0]
// 	recvDecl, err := tr.db.DefinitionOf(ctx.info, recv.Type)
// 	if err != nil {
// 		return nil, err
// 	} else if recvDecl != nil {
// 		// remove prototypes where the class is global
// 		if recvDecl.JSTag != nil && recvDecl.JSTag.HasOption("global") {
// 			return jsast.CreateEmptyStatement(), nil
// 		}
// 	}

// 	x, e := tr.expression(d, sp,recv.Type)
// 	if e != nil {
// 		return nil, e
// 	}

// 	if len(recv.Names) > 1 {
// 		return nil, fmt.Errorf("function<recv>: only expected 1 func receiver name but got %d", len(recv.Names))
// 	}

// 	// Links the function receiver to "this",
// 	// Placing it on top of the function body
// 	// e.g. var d = this
// 	//
// 	// TODO: be smarter here and rename the inner body variables to "this"
// 	if len(recv.Names) == 1 {
// 		body = append([]interface{}{jsast.CreateVariableDeclaration(
// 			"var",
// 			jsast.CreateVariableDeclarator(
// 				jsast.CreateIdentifier(recv.Names[0].Name),
// 				jsast.CreateThisExpression(),
// 			),
// 		)}, body...)
// 	}

// 	var fn jsast.FunctionExpression
// 	if d.IsAsync() {
// 		fn = jsast.CreateAsyncFunctionExpression(
// 			&fnname,
// 			params,
// 			jsast.CreateFunctionBody(body...),
// 		)
// 	} else {
// 		fn = jsast.CreateFunctionExpression(
// 			&fnname,
// 			params,
// 			jsast.CreateFunctionBody(body...),
// 		)
// 	}

// 	// {recv}.prototype.{name} = function ({params}) {
// 	//   {body}
// 	// }
// 	return jsast.CreateExpressionStatement(
// 		jsast.CreateAssignmentExpression(
// 			jsast.CreateMemberExpression(
// 				jsast.CreateMemberExpression(
// 					x,
// 					jsast.CreateIdentifier("prototype"),
// 					false,
// 				),
// 				fnname,
// 				false,
// 			),
// 			jsast.AssignmentOperator("="),
// 			fn,
// 		),
// 	), nil
// }

func (tr *Translator) genDecl(d def.Definition, sp *scope.Scope, n *ast.GenDecl) (j jsast.IStatement, err error) {
	switch n.Tok.String() {
	case "import":
		return tr.importSpec(d, sp, n)
	case "type":
		return tr.typeSpec(d, sp, n)
	case "var":
		return tr.varSpec(d, sp, n)
	default:
		return nil, fmt.Errorf("genDecl: unhandled token %s", n.Tok.String())
	}

	// // specs := n.Specs
	// for _, spec := range n.Specs {
	// 	switch t := spec.(type) {
	// 	// case *ast.ImportSpec:
	// 	// 	return importSpec(pkg, f, t)
	// 	case *ast.TypeSpec:
	// 		// type defs will only have 1 spec
	// 		return typeSpec(pkg, f, t)
	// 	default:
	// 		return nil, unhandled("genDecl", spec)
	// 	}
	// }

	// return jsast.CreateEmptyStatement(), nil
}

// func importSpec(pkg *loader.PackageInfo,  f *ast.File, n *ast.ImportSpec) (j jsast.IStatement, err error) {
// 	return nil, nil
// }

func (tr *Translator) typeSpec(d def.Definition, sp *scope.Scope, n *ast.GenDecl) (j jsast.IStatement, err error) {
	if len(n.Specs) != 1 {
		return nil, fmt.Errorf("genDecl: expected type to only have 1 spec but it has %d", len(n.Specs))
	}

	s, ok := n.Specs[0].(*ast.TypeSpec)
	if !ok {
		return nil, unhandled("typeSpec<TypeSpec>", n.Specs[0])
	}

	var st *ast.StructType
	switch t := s.Type.(type) {
	case *ast.StructType:
		st = t
	case *ast.InterfaceType:
		return jsast.CreateEmptyStatement(), nil
	default:
		return nil, unhandled("typeSpec<StructType>", s.Type)
	}

	// decl, err := tr.db.DefinitionOf(ctx.info, s.Name)
	// if err != nil {
	// 	return nil, err
	// } else if decl == nil {
	// 	return nil, errors.New("typeSpec expected a declaration")
	// }

	// // don't include in the build if it has the global option
	// if decl.JSTag != nil && decl.JSTag.HasOption("global") {
	// 	return jsast.CreateEmptyStatement(), nil
	// }

	// tag, e := getCommentTag(n.Doc)
	// if e != nil {
	// 	return nil, e
	// }

	// fieldtags, e :=

	// store the tag for later renaming
	// objectof := ctx.info.ObjectOf(s.Name)
	// typeof := ctx.info.TypeOf(s.Name)
	// if tag != nil && objectof != nil {
	// 	ctx.aliases[objectof.String()] = tag
	// 	ctx.aliases[typeof.String()] = tag
	// 	// TODO: not sure if this is a good idea or not
	// 	// but it's to handle pointer receivers in 1 spot
	// 	ctx.aliases["*"+typeof.String()] = tag
	// }

	var ivars []interface{}
	o := jsast.CreateIdentifier("o")
	expr := jsast.CreateAssignmentExpression(o, jsast.AssignmentOperator("="), defaulted("o", jsast.CreateObjectExpression(nil)))
	ivars = append(ivars, jsast.CreateExpressionStatement(expr))

	// get the fields
	for _, field := range st.Fields.List {
		names := field.Names

		// just the type e.g struct { *dep.Settings }
		if len(field.Names) == 0 {
			id, e := getIdentifier(field.Type)
			if e != nil {
				return nil, e
			}
			names = append(names, id)
		}

		// otherwise range over the names
		for _, id := range names {
			// get the name, using the alias if there is one
			// name := maybeAlias(decl, id.Name)
			name := id.Name

			// get the default value
			value, e := tr.zeroed(d, sp, field.Type, name)
			if e != nil {
				return nil, e
			}

			// this.$name = o.$name || <default>
			ivars = append(ivars, jsast.CreateExpressionStatement(
				jsast.CreateAssignmentExpression(
					jsast.CreateMemberExpression(
						jsast.CreateThisExpression(),
						jsast.CreateIdentifier(name),
						false,
					),
					jsast.AssignmentOperator("="),
					jsast.CreateMemberExpression(
						jsast.CreateIdentifier("o"),
						value,
						false,
					),
				),
			))
		}
	}

	ident := jsast.CreateIdentifier(s.Name.Name)
	return jsast.CreateFunction(
		&ident,
		// TODO: make API for this
		[]jsast.IPattern{jsast.CreateIdentifier("o")},
		jsast.CreateFunctionBody(ivars...),
	), nil
}

func (tr *Translator) structs(d struc.Struct) (j jsast.IStatement, err error) {
	n := d.Node()
	sp := scope.New(n)
	var ivars []interface{}

	o := jsast.CreateIdentifier("o")
	expr := jsast.CreateAssignmentExpression(o, jsast.AssignmentOperator("="), defaulted("o", jsast.CreateObjectExpression(nil)))
	ivars = append(ivars, jsast.CreateExpressionStatement(expr))

	// get the fields
	for _, field := range d.Fields() {
		name := field.Name()

		// get the default value
		value, e := tr.zeroed(d, sp, field.Type(), name)
		if e != nil {
			return nil, e
		}

		// this.$name = o.$name || <default>
		ivars = append(ivars, jsast.CreateExpressionStatement(
			jsast.CreateAssignmentExpression(
				jsast.CreateMemberExpression(
					jsast.CreateThisExpression(),
					jsast.CreateIdentifier(name),
					false,
				),
				jsast.AssignmentOperator("="),
				jsast.CreateMemberExpression(
					jsast.CreateIdentifier("o"),
					value,
					false,
				),
			),
		))
	}

	ident := jsast.CreateIdentifier(d.Name())
	return jsast.CreateFunction(
		&ident,
		// TODO: make API for this
		[]jsast.IPattern{jsast.CreateIdentifier("o")},
		jsast.CreateFunctionBody(ivars...),
	), nil
}

func (tr *Translator) importSpec(d def.Definition, sp *scope.Scope, n *ast.GenDecl) (j jsast.IStatement, err error) {
	var decls []jsast.VariableDeclarator

	for _, spec := range n.Specs {
		switch t := spec.(type) {
		case *ast.ImportSpec:
			var lh jsast.Identifier
			p := t.Path.Value

			// import dep "pkg/dep"
			if t.Name != nil {
				lh = jsast.CreateIdentifier(t.Name.Name)
			} else if p != "" {
				lh = jsast.CreateIdentifier(path.Base(strings.Trim(p, `"`)))
			} else {
				return nil, unhandled("importSpec<empty>", spec)
			}

			rh := jsast.CreateMemberExpression(
				jsast.CreateIdentifier("pkg"),
				jsast.CreateString(t.Path.Value),
				true,
			)

			decl := jsast.CreateVariableDeclarator(lh, rh)
			decls = append(decls, decl)
		default:
			return nil, unhandled("importSpec", spec)
		}
	}

	return jsast.CreateVariableDeclaration("var", decls...), nil
}

func (tr *Translator) varSpec(d def.Definition, sp *scope.Scope, n *ast.GenDecl) (j jsast.IStatement, err error) {
	var decls []jsast.VariableDeclarator

	for _, spec := range n.Specs {
		switch t := spec.(type) {
		case *ast.ValueSpec:
			lval := len(t.Values)

			// handle balanced destructuring
			for i, ident := range t.Names {
				lh := jsast.CreateIdentifier(ident.Name)

				var rh jsast.IExpression
				if i < lval {
					r, e := tr.expression(d, sp, t.Values[i])
					if e != nil {
						return j, e
					}
					rh = r
				} else {
					r, e := tr.defaultValue(d, sp, t.Type)
					if e != nil {
						return j, e
					}
					rh = r
				}

				decl := jsast.CreateVariableDeclarator(lh, rh)
				decls = append(decls, decl)
			}

		default:
			return nil, unhandled("varSpec", spec)
		}
	}

	return jsast.CreateVariableDeclaration("var", decls...), nil
	// return nil, nil
}

// (tr *Translator) func funcStatement(d def.Definition, sp *scope.Scope, n ast.Stmt) (j jsast.IStatement, err error) {
// 	switch t := n.(type) {
// 	case *ast.ExprStmt:
// 		return tr.exprStatement(d, sp,t)
// 	case *ast.IfStmt:
// 		return tr.ifStmt(d, sp,t)
// 	case *ast.AssignStmt:
// 		return tr.assignStatement(d, sp,t)
// 	case *ast.ReturnStmt:
// 		return tr.returnStmt(d, sp,t)
// 	case *ast.RangeStmt:
// 		return tr.rangeStmt(d, sp,t)
// 	case *ast.ForStmt:
// 		return tr.forStmt(d, sp,t)
// 	case *ast.DeclStmt:
// 		return tr.declStmt(d, sp,t)
// 	case *ast.GoStmt:
// 		return tr.goStmt(d, sp,t)
// 	case *ast.SendStmt:
// 		return tr.sendStmt(d, sp,t)
// 	default:
// 		return nil, unhandled("funcStatement", n)
// 	}
// }

func (tr *Translator) goStmt(d def.Definition, sp *scope.Scope, n *ast.GoStmt) (j jsast.IStatement, err error) {
	// build argument list
	// var args
	var args []jsast.IExpression
	for _, arg := range n.Call.Args {
		x, e := tr.expression(d, sp, arg)
		if e != nil {
			return nil, e
		}
		args = append(args, x)
	}

	// function literal go function (...) { ... }(...)
	if fn, ok := n.Call.Fun.(*ast.FuncLit); ok {
		var params []jsast.IPattern
		if fn.Type != nil && fn.Type.Params != nil {
			fields := fn.Type.Params.List
			for _, field := range fields {
				// names because: (a, b string, c int) = [[a, b], c]
				for _, name := range field.Names {
					id := jsast.CreateIdentifier(name.Name)
					params = append(params, id)
				}
			}
		}

		var body []interface{}
		for _, stmt := range fn.Body.List {
			s, e := tr.statement(d, sp, stmt)
			if e != nil {
				return nil, e
			}
			body = append(body, s)
		}

		return jsast.CreateExpressionStatement(
			jsast.CreateCallExpression(
				jsast.CreateAsyncFunctionExpression(
					nil,
					params,
					jsast.CreateFunctionBody(body...),
				),
				args,
			),
		), nil
	}

	// id, e := util.GetIdentifier(n.Call.Fun)
	// if e != nil {
	// 	return nil, e
	// }

	def, e := tr.index.DefinitionOf(d.Path(), n.Call.Fun)
	if e != nil {
		return nil, e
	}
	fn, ok := def.(fn.Function)
	if !ok {
		return nil, fmt.Errorf("goStmt: expected function but received %T", def)
	}

	isAsync, e := fn.IsAsync()
	if e != nil {
		return nil, e
	}

	if isAsync {
		return jsast.CreateExpressionStatement(
			jsast.CreateAwaitExpression(
				jsast.CreateCallExpression(
					jsast.CreateIdentifier(def.Name()),
					args,
				),
			),
		), nil
	}

	return jsast.CreateExpressionStatement(
		jsast.CreateCallExpression(
			jsast.CreateIdentifier(def.Name()),
			args,
		),
	), nil
}

func (tr *Translator) sendStmt(d def.Definition, sp *scope.Scope, n *ast.SendStmt) (j jsast.IStatement, err error) {
	ch, e := tr.expression(d, sp, n.Chan)
	if e != nil {
		return nil, e
	}

	val, e := tr.expression(d, sp, n.Value)
	if e != nil {
		return nil, e
	}

	return jsast.CreateExpressionStatement(
		jsast.CreateAwaitExpression(
			jsast.CreateCallExpression(
				jsast.CreateMemberExpression(
					ch,
					jsast.CreateIdentifier("Send"),
					false,
				),
				[]jsast.IExpression{val},
			),
		),
	), nil
}

func (tr *Translator) rangeStmt(d def.Definition, sp *scope.Scope, n *ast.RangeStmt) (j jsast.IStatement, err error) {
	id, ok := n.Key.(*ast.Ident)
	if !ok {
		return nil, unhandled("rangeStmt<ident>", n.Key)
	}

	if id.Obj == nil {
		return nil, unhandled("rangeStmt<obj>", id.Obj)
	}

	asn, ok := id.Obj.Decl.(*ast.AssignStmt)
	if !ok {
		return nil, unhandled("rangeStmt<decl>", id.Obj)
	}

	if len(asn.Lhs) == 0 {
		return nil, fmt.Errorf("rangeStmt: didn't expect len(lhs) == 0")
	}

	// create the condition
	if len(asn.Rhs) == 0 {
		return nil, fmt.Errorf("rangeStmt: didn't expect len(rhs) == 0")
	}

	rh, e := tr.expression(d, sp, asn.Rhs[0])
	if e != nil {
		return nil, e
	}

	var inits []jsast.VariableDeclarator
	idx, ok := asn.Lhs[0].(*ast.Ident)
	if !ok {
		return nil, unhandled("rangeStmt<idx>", asn.Lhs[0])
	}
	inits = append(inits, jsast.CreateVariableDeclarator(
		jsast.CreateIdentifier(idx.Name),
		jsast.CreateInt(0),
	))

	var val *ast.Ident
	if len(asn.Lhs) >= 2 {
		val, ok = asn.Lhs[1].(*ast.Ident)
		if !ok {
			return nil, unhandled("rangeStmt<val>", asn.Lhs[1])
		}
		inits = append(inits, jsast.CreateVariableDeclarator(
			jsast.CreateIdentifier(val.Name),
			nil,
		))
	}

	init := jsast.CreateVariableDeclaration("var", inits...)

	cond := jsast.CreateBinaryExpression(
		jsast.CreateIdentifier(idx.Name),
		jsast.BinaryOperator("<"),
		jsast.CreateMemberExpression(
			rh,
			jsast.CreateIdentifier("length"),
			false,
		),
	)

	postexpr := jsast.CreateUpdateExpression(
		jsast.CreateIdentifier(idx.Name),
		jsast.UpdateOperator("++"),
		false,
	)

	// build the body
	var stmts []jsast.IStatement
	if val != nil {
		stmts = append(stmts, jsast.CreateVariableDeclaration(
			"var",
			jsast.CreateVariableDeclarator(
				jsast.CreateIdentifier(val.Name),
				jsast.CreateMemberExpression(
					rh,
					jsast.CreateIdentifier(idx.Name),
					true,
				),
			),
		))
	}
	for _, stmt := range n.Body.List {
		v, e := tr.statement(d, sp, stmt)
		if e != nil {
			return nil, errors.Wrap(e, "rangeStmt<body>")
		}
		stmts = append(stmts, v)
	}
	body := jsast.CreateBlockStatement(stmts...)

	// TODO:
	// Range expression                              1st value          2nd value
	// [x] array or slice  a  [n]E, *[n]E, or []E    index    i  int    a[i]       E
	// [ ] string          s  string type            index    i  int    see below  rune
	// [ ] map             m  map[K]V                key      k  K      m[k]       V
	// [ ] channel         c  chan E, <-chan E       element  e  E

	kind, e := tr.index.TypeOf(d.Path(), asn.Rhs[0])
	if e != nil {
		return nil, e
	}
	switch kind.(type) {
	case *types.Array, *types.Slice:
		return jsast.CreateForStatement(
			init,
			cond,
			postexpr,
			body,
		), nil
	default:
		return nil, unhandled("rangeStmt<rhs.obj.type>", asn.Rhs[0])
	}
}

func (tr *Translator) declStmt(d def.Definition, sp *scope.Scope, n *ast.DeclStmt) (j jsast.IStatement, err error) {
	switch t := n.Decl.(type) {
	case *ast.GenDecl:
		return tr.genDecl(d, sp, t)
	default:
		return nil, unhandled("declStmt", n)
	}
}

func (tr *Translator) exprStatement(d def.Definition, sp *scope.Scope, expr *ast.ExprStmt) (j jsast.IStatement, err error) {
	switch t := expr.X.(type) {
	case *ast.CallExpr:
		if expr, e := tr.maybeBuiltinStmt(d, sp, t); expr != nil || e != nil {
			return expr, e
		}

		x, e := tr.callExpr(d, sp, t)
		if e != nil {
			return nil, e
		}
		return jsast.CreateExpressionStatement(x), nil
	default:
		return nil, fmt.Errorf("exprStatement: unhandled type: %s", reflect.TypeOf(expr))
	}
}

func (tr *Translator) ifStmt(d def.Definition, sp *scope.Scope, n *ast.IfStmt) (j jsast.IStatement, err error) {
	var multi []jsast.IStatement
	var e error

	// init: if x, e := expression; e != nil { ... }
	if n.Init != nil {
		init, e := tr.statement(d, sp, n.Init)
		if e != nil {
			return nil, e
		}
		multi = append(multi, init)
	}

	// condition: if [(...)] { ... } else { ... }
	test, e := tr.expression(d, sp, n.Cond)
	if e != nil {
		return nil, e
	}

	// body : if (...) [{ ... }] else { ... }
	body, e := tr.blockStmt(d, sp, n.Body)
	if e != nil {
		return nil, e
	}

	// else: if (...) { ... } else [{ ... }]
	alt, e := tr.statement(d, sp, n.Else)
	if e != nil {
		return nil, e
	}

	// create the if statement
	ifstmt := jsast.CreateIfStatement(
		test,
		body,
		alt,
	)
	multi = append(multi, ifstmt)

	// join the statements into a single statement
	return jsast.CreateMultiStatement(multi...), nil
}

func (tr *Translator) branchStmt(d def.Definition, sp *scope.Scope, n *ast.BranchStmt) (j jsast.IStatement, err error) {
	switch n.Tok.String() {
	case "break":
		return jsast.CreateBreakStatement(nil), nil
	default:
		return nil, fmt.Errorf("unhandled branchStmt: %s", n.Tok.String())
	}
}

func (tr *Translator) forStmt(d def.Definition, sp *scope.Scope, n *ast.ForStmt) (j jsast.IStatement, err error) {

	init, e := tr.statement(d, sp, n.Init)
	if e != nil {
		return nil, errors.Wrap(e, "forStmt")
	}

	cond, e := tr.expression(d, sp, n.Cond)
	if e != nil {
		return nil, errors.Wrap(e, "forStmt")
	}

	post, e := tr.statement(d, sp, n.Post)
	if e != nil {
		return nil, errors.Wrap(e, "forStmt")
	}

	body, e := tr.blockStmt(d, sp, n.Body)
	if e != nil {
		return nil, errors.Wrap(e, "forStmt")
	}

	// In Go the post condition is a statement,
	// in JS it's an expression
	//
	// it can also be nil in the case of for { ... }
	var postexpr jsast.IExpression
	switch t := post.(type) {
	case jsast.ExpressionStatement:
		postexpr = t.Expression
	case nil:
		postexpr = nil
	default:
		return nil, unhandled("forStmt<post>", post)
	}

	return jsast.CreateForStatement(
		init,
		cond,
		postexpr,
		body,
	), nil
}

// (tr *Translator) func statement(d def.Definition, sp *scope.Scope, n ast.Stmt) (j jsast.IStatement, err error) {
// 	switch t := n.(type) {
// 	case nil:
// 		return nil, nil
// 	case *ast.AssignStmt:
// 		return tr.assignStatement(d, sp,t)
// 	case *ast.IncDecStmt:
// 		return tr.incDecStmt(d, sp,t)
// 	case *ast.ExprStmt:
// 		return tr.exprStatement(d, sp,t)
// 	case *ast.IfStmt:
// 		return tr.ifStmt(d, sp,t)
// 	case *ast.BranchStmt:
// 		return tr.branchStmt(d, sp,t)
// 	case *ast.ReturnStmt:
// 		return tr.returnStmt(d, sp,t)
// 	case *ast.SendStmt:
// 		return tr.sendStmt(d, sp,t)
// 	case *ast.BlockStmt:
// 		return tr.blockStmt(d, sp,t)
// 	default:
// 		return nil, unhandled("statement", n)
// 	}
// }

func (tr *Translator) blockStmt(d def.Definition, sp *scope.Scope, n *ast.BlockStmt) (j jsast.IBlockStatement, err error) {
	var stmts []jsast.IStatement

	for _, stmt := range n.List {
		v, e := tr.statement(d, sp, stmt)
		if e != nil {
			return nil, errors.Wrap(e, "blockStmt")
		}
		stmts = append(stmts, v)
	}

	return jsast.CreateBlockStatement(stmts...), nil
}

func (tr *Translator) assignStatement(d def.Definition, sp *scope.Scope, as *ast.AssignStmt) (j jsast.IStatement, err error) {
	// TODO: these are separate, but very similar functions.
	// the reason they're separate is because the JS AST's are
	// different. It'd be good to come up with a way to consolidate
	// this logic though because this variable conversion is a bit tricky
	switch as.Tok.String() {
	case "=":
		return tr.jsAssignStmt(d, sp, as)
	case ":=":
		return tr.jsVariableDecl(d, sp, as)
	default:
		return nil, fmt.Errorf("unhandled assignStatement<tok>: %s", as.Tok.String())
	}
}

func (tr *Translator) jsAssignStmt(d def.Definition, sp *scope.Scope, as *ast.AssignStmt) (j jsast.IStatement, err error) {
	var exprs []jsast.IExpression
	lhs := as.Lhs
	rhs := as.Rhs
	llhs := len(lhs)
	lrhs := len(rhs)

	// ensure we're not in an invalid state
	if llhs != lrhs && lrhs > 1 {
		return nil, fmt.Errorf("invalid golang assignment (AFAIK)")
	}

	// nothing on right side
	if lrhs == 0 {
		for _, lh := range lhs {
			l, e := tr.expression(d, sp, lh)
			if e != nil {
				return nil, e
			}
			exprs = append(exprs, l)
		}
	}

	// balanced on both sides
	if llhs == lrhs {
		for i, lh := range lhs {
			if isUnderscoreVariable(lh) {
				continue
			}

			l, e := tr.expression(d, sp, lh)
			if e != nil {
				return nil, e
			}

			r, e := tr.expression(d, sp, rhs[i])
			if e != nil {
				return nil, e
			}

			exprs = append(exprs, jsast.CreateAssignmentExpression(
				l,
				jsast.AssignmentOperator("="),
				r,
			))
		}
	}

	// unbalanced
	if llhs != lrhs {
		var lname string

		if isUnderscoreVariable(lhs[0]) {
			return nil, unhandled("jsAssignStmt<underscore>", lhs[0])
		}

		switch t := lhs[0].(type) {
		case *ast.Ident:
			lname = "$" + t.Name
		// case *ast.SelectorExpr:
		// return unhandled("jsAssignStmt<selectorExpr>", t)
		default:
			return nil, unhandled("jsAssignStmt<unbalanced>", lhs[0])
		}

		r, e := tr.expression(d, sp, rhs[0])
		if e != nil {
			return nil, e
		}

		exprs = append(exprs, jsast.CreateAssignmentExpression(
			jsast.CreateIdentifier(lname),
			jsast.AssignmentOperator("="),
			r,
		))

		for i, l := range lhs {
			if isUnderscoreVariable(lhs[0]) {
				continue
			}

			x, e := tr.expression(d, sp, l)
			if e != nil {
				return nil, e
			}

			exprs = append(exprs, jsast.CreateAssignmentExpression(
				x,
				jsast.AssignmentOperator("="),
				jsast.CreateMemberExpression(
					jsast.CreateIdentifier(lname),
					jsast.CreateInt(i),
					true,
				),
			))
		}
	}

	return jsast.CreateExpressionStatement(
		jsast.CreateSequenceExpression(exprs...),
	), nil
}

func (tr *Translator) jsVariableDecl(d def.Definition, sp *scope.Scope, as *ast.AssignStmt) (j jsast.IStatement, err error) {
	var stmts []jsast.VariableDeclarator

	lhs := as.Lhs
	rhs := as.Rhs
	llhs := len(lhs)
	lrhs := len(rhs)

	// ensure we're not in an invalid state
	if llhs != lrhs && lrhs > 1 {
		return nil, fmt.Errorf("invalid golang assignment (AFAIK)")
	}

	// nothing on right side
	if lrhs == 0 {
		for _, lh := range lhs {
			l, ok := lh.(*ast.Ident)
			if !ok {
				return nil, fmt.Errorf("jsVariableDecl<zero>: unhandled type: %s", reflect.TypeOf(lh))
			}

			stmts = append(stmts, jsast.CreateVariableDeclarator(
				jsast.CreateIdentifier(l.Name),
				nil,
			))
		}
	}

	// balanced on both sides
	if llhs == lrhs {
		for i, lh := range lhs {
			l, ok := lh.(*ast.Ident)
			if !ok {
				return nil, fmt.Errorf("jsVariableDecl<balanced>: unhandled type: %s", reflect.TypeOf(lh))
			}

			r, e := tr.expression(d, sp, rhs[i])
			if e != nil {
				return nil, e
			}

			stmts = append(stmts, jsast.CreateVariableDeclarator(
				jsast.CreateIdentifier(l.Name),
				r,
			))
		}
	}

	// unbalanced
	if llhs != lrhs {
		l, ok := lhs[0].(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("jsVariableDecl<unbalanced>: unhandled type: %s", reflect.TypeOf(lhs[0]))
		}
		lname := "$" + l.Name

		r, e := tr.expression(d, sp, rhs[0])
		if e != nil {
			return nil, e
		}

		stmts = append(stmts, jsast.CreateVariableDeclarator(
			jsast.CreateIdentifier(lname),
			r,
		))

		for i, l := range lhs {
			x, ok := l.(*ast.Ident)
			if !ok {
				return nil, fmt.Errorf("jsVariableDecl<unbalanced>: unhandled type: %s", reflect.TypeOf(x))
			}

			stmts = append(stmts, jsast.CreateVariableDeclarator(
				jsast.CreateIdentifier(x.Name),
				jsast.CreateMemberExpression(
					jsast.CreateIdentifier(lname),
					jsast.CreateInt(i),
					true,
				),
			))
		}
	}

	return jsast.CreateVariableDeclaration("var", stmts...), nil
}

func (tr *Translator) incDecStmt(d def.Definition, sp *scope.Scope, n *ast.IncDecStmt) (j jsast.IStatement, err error) {
	var op jsast.UpdateOperator

	x, e := tr.expression(d, sp, n.X)
	if e != nil {
		return nil, errors.Wrap(e, "incDecStmt")
	}

	switch n.Tok.String() {
	case "++":
		op = jsast.UpdateOperator("++")
	case "--":
		op = jsast.UpdateOperator("--")
	default:
		return nil, unhandled("incDecStmt", n.Tok)
	}

	return jsast.CreateExpressionStatement(
		jsast.CreateUpdateExpression(x, op, false),
	), nil
}

func (tr *Translator) returnStmt(d def.Definition, sp *scope.Scope, n *ast.ReturnStmt) (j jsast.IStatement, err error) {
	// no return values
	if len(n.Results) == 0 {
		return jsast.CreateReturnStatement(nil), nil
	}

	var args []jsast.IExpression
	for _, arg := range n.Results {
		a, e := tr.expression(d, sp, arg)
		if e != nil {
			return nil, e
		}
		args = append(args, a)
	}

	// return an array
	if len(n.Results) > 1 {
		return jsast.CreateReturnStatement(jsast.CreateArrayExpression(args...)), nil
	}

	// return the value by itself
	return jsast.CreateReturnStatement(args[0]), nil
}

func (tr *Translator) callExpr(d def.Definition, sp *scope.Scope, n *ast.CallExpr) (j jsast.IExpression, err error) {
	// create an expression for built-in golang functions like append
	// TODO: turn this into an ast.Walk()-like thing
	if expr, e := tr.maybeBuiltinExpr(d, sp, n); expr != nil || e != nil {
		return expr, e
	}

	if expr, e := tr.maybeJSRaw(d, sp, n); expr != nil || e != nil {
		return expr, e
	}

	// if expr, e := tr.maybeJSRewrite(d, sp, n); expr != nil || e != nil {
	// 	return expr, e
	// }

	if expr, e := tr.maybeError(d, sp, n); expr != nil || e != nil {
		return expr, e
	}

	if expr, e := tr.maybeAwait(d, sp, n); expr != nil || e != nil {
		return expr, e
	}

	var args []jsast.IExpression
	for _, arg := range n.Args {
		v, e := tr.expression(d, sp, arg)
		if e != nil {
			return j, e
		}
		args = append(args, v)
	}

	// convert array conversions
	// e.g. []byte(`test`) => `test`
	if len(n.Args) == 1 {
		if _, ok := n.Fun.(*ast.ArrayType); ok {
			return args[0], nil
		}
	}

	callee, e := tr.expression(d, sp, n.Fun)
	if e != nil {
		return j, e
	}

	return jsast.CreateCallExpression(
		callee,
		args,
	), nil
}

func (tr *Translator) expression(d def.Definition, sp *scope.Scope, expr ast.Expr) (j jsast.IExpression, err error) {
	switch t := expr.(type) {
	case *ast.Ident:
		return tr.identifier(d, sp, t)
	case *ast.BasicLit:
		return tr.basiclit(d, sp, t)
	case *ast.CallExpr:
		return tr.callExpr(d, sp, t)
	case *ast.BinaryExpr:
		return tr.binaryExpression(d, sp, t)
	case *ast.CompositeLit:
		return tr.compositeLiteral(d, sp, t)
	case *ast.SelectorExpr:
		return tr.selectorExpr(d, sp, t)
	case *ast.IndexExpr:
		return tr.indexExpr(d, sp, t)
	case *ast.StarExpr:
		return tr.starExpr(d, sp, t)
	case *ast.UnaryExpr:
		return tr.unaryExpr(d, sp, t)
	case *ast.FuncLit:
		return tr.funcLit(d, sp, t)
	// case *ast.ArrayType:
	// 	return tr.arrayType(d, sp, t)
	case *ast.ChanType:
		return tr.chanType(d, sp, t)
	case *ast.SliceExpr:
		return tr.sliceExpr(d, sp, t)
	case *ast.TypeAssertExpr:
		return tr.typeAssertExpr(d, sp, t)
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("expression(): unhandled type: %s", reflect.TypeOf(expr))
	}
}

func (tr *Translator) typeAssertExpr(d def.Definition, sp *scope.Scope, n *ast.TypeAssertExpr) (jsast.IExpression, error) {
	return tr.expression(d, sp, n.X)
}

func (tr *Translator) sliceExpr(d def.Definition, sp *scope.Scope, n *ast.SliceExpr) (jsast.IExpression, error) {
	var high jsast.IExpression
	var low jsast.IExpression

	// create the low expression
	if n.Low != nil {
		l, e := tr.expression(d, sp, n.Low)
		if e != nil {
			return nil, e
		}
		low = l
	} else {
		low = jsast.CreateInt(0)
	}

	// create the high side
	if n.High != nil {
		h, e := tr.expression(d, sp, n.High)
		if e != nil {
			return nil, e
		}
		high = h
	}

	x, e := tr.expression(d, sp, n.X)
	if e != nil {
		return nil, e
	}

	args := []jsast.IExpression{low}
	if high != nil {
		args = append(args, high)
	}

	return jsast.CreateCallExpression(
		jsast.CreateMemberExpression(
			x,
			jsast.CreateIdentifier("slice"),
			false,
		),
		args,
	), nil
}

func expressionToString(expr ast.Expr) (string, error) {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name, nil
	case *ast.SelectorExpr:
		x, e := expressionToString(t.X)
		if e != nil {
			return "", e
		}
		return x + "." + t.Sel.Name, nil
	case *ast.BasicLit:
		return t.Value, nil
	default:
		return "", nil
	}
}

func (tr *Translator) funcLit(d def.Definition, sp *scope.Scope, n *ast.FuncLit) (j jsast.IExpression, err error) {
	// build argument list
	// var args
	var params []jsast.IPattern
	if n.Type != nil && n.Type.Params != nil {
		fields := n.Type.Params.List
		for _, field := range fields {
			// names because: (a, b string, c int) = [[a, b], c]
			for _, name := range field.Names {
				id := jsast.CreateIdentifier(name.Name)
				params = append(params, id)
			}
		}
	}

	// create the body
	var body []interface{}
	for _, stmt := range n.Body.List {
		jsStmt, e := tr.statement(d, sp, stmt)
		if e != nil {
			return j, e
		}
		body = append(body, jsStmt)
	}

	fn, ok := d.(fn.Function)
	if !ok {
		return nil, fmt.Errorf("funcLit: expected inside a function declaration")
	}

	isAsync, e := fn.IsAsync()
	if e != nil {
		return nil, e
	}

	if isAsync {
		return jsast.CreateAwaitExpression(
			jsast.CreateAsyncFunctionExpression(
				nil,
				params,
				jsast.CreateFunctionBody(body...),
			),
		), nil
	}

	return jsast.CreateFunctionExpression(
		nil,
		params,
		jsast.CreateFunctionBody(body...),
	), nil
}

// binary expressions in Go can be either:
//    Binaryexpression || LogicalExpression
// in jsast.
func (tr *Translator) binaryExpression(d def.Definition, sp *scope.Scope, b *ast.BinaryExpr) (j jsast.IExpression, err error) {
	x, e := tr.expression(d, sp, b.X)
	if e != nil {
		return nil, e
	}
	y, e := tr.expression(d, sp, b.Y)
	if e != nil {
		return nil, e
	}

	if !b.Op.IsOperator() {
		return nil, unhandled("binaryExpression<not op>", b.Op)
	}

	op := b.Op.String()
	switch op {
	case "||", "&&":
		return jsast.CreateLogicalExpression(x, jsast.LogicalOperator(op), y), nil
	// TODO: prune. should be only values that are possible in Go
	case "==", "!=", "===", "!==",
		"<", "<=", ">", ">=", "<<",
		">>", ">>>", "+", "-", "*",
		"/", "%", "|", "^", "&",
		"in", "instanceof":
		return jsast.CreateBinaryExpression(x, jsast.BinaryOperator(op), y), nil
	default:
		return nil, unhandled("binaryExpression<unknown op>", op)
	}
}

func (tr *Translator) identifier(d def.Definition, sp *scope.Scope, n *ast.Ident) (j jsast.IExpression, err error) {
	// decl, err := tr.db.DefinitionOf(ctx.info, n)
	// if err != nil {
	// 	return nil, err
	// }
	// name := n.Name

	// // if decl is nil, it's a local variable
	// // or a predefined identifier like
	// // nil or error
	// if decl != nil {
	// 	// use the alias if we have a JS tag
	// 	if decl.JSTag != nil {
	// 		name = decl.JSTag.Name
	// 	}
	// }

	// log.Infof("name %s %+v", n.Name, ctx.info.ObjectOf(n).Type())

	switch n.Name {
	case "nil":
		return jsast.CreateNull(), nil
	default:
		return jsast.CreateIdentifier(n.Name), nil
	}
}

func (tr *Translator) starExpr(d def.Definition, sp *scope.Scope, n *ast.StarExpr) (j jsast.IExpression, err error) {
	// for now, we're ignoring the pointer
	x, e := tr.expression(d, sp, n.X)
	if e != nil {
		return nil, e
	}

	return x, nil
}

func (tr *Translator) unaryExpr(d def.Definition, sp *scope.Scope, n *ast.UnaryExpr) (j jsast.IExpression, err error) {
	// for now, we're ignoring the pointer
	x, e := tr.expression(d, sp, n.X)
	if e != nil {
		return nil, e
	}

	switch n.Op.String() {
	case "<-":
		return jsast.CreateAwaitExpression(
			jsast.CreateCallExpression(
				jsast.CreateMemberExpression(
					x,
					jsast.CreateIdentifier("Recv"),
					false,
				),
				[]jsast.IExpression{},
			),
		), nil
	default:
		return x, nil
	}
}

func (tr *Translator) basiclit(d def.Definition, sp *scope.Scope, lit *ast.BasicLit) (j jsast.IExpression, err error) {
	value := lit.Value
	l := len(value)

	// replace ` with " and escape the inner quotes
	if value[0] == '`' && value[len(value)-1] == '`' {
		value = strconv.Quote(value[1 : l-1])
	}

	return jsast.CreateLiteral(value), nil
}

func (tr *Translator) compositeLiteral(d def.Definition, sp *scope.Scope, n *ast.CompositeLit) (j jsast.IExpression, err error) {
	switch n.Type.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		return tr.jsNewFunction(d, sp, n)
	case *ast.ArrayType:
		return tr.jsArrayExpression(d, sp, n)
	case *ast.MapType:
		return tr.jsObjectExpression(d, sp, n)
	default:
		return nil, unhandled("compositeLiteral<type>", n.Type)
	}
}

// map[string]string => { ... }
func (tr *Translator) jsObjectExpression(d def.Definition, sp *scope.Scope, n *ast.CompositeLit) (j jsast.ObjectExpression, err error) {
	var props []jsast.Property

	for _, elt := range n.Elts {
		switch t := elt.(type) {
		case *ast.KeyValueExpr:
			prop, e := tr.keyValueExpr(d, sp, n, t)
			if e != nil {
				return j, e
			}
			props = append(props, prop)
		default:
			return j, unhandled("jsObjectExpression", elt)
		}
	}

	return jsast.CreateObjectExpression(props), nil
}

func (tr *Translator) jsNewFunction(d def.Definition, sp *scope.Scope, n *ast.CompositeLit) (j jsast.IExpression, err error) {
	fn, e := tr.expression(d, sp, n.Type)
	if e != nil {
		return nil, e
	}

	var props []jsast.Property
	for i, elt := range n.Elts {
		prop, e := tr.property(d, sp, n, i, elt)
		if e != nil {
			return nil, e
		}
		props = append(props, prop)
	}

	return jsast.CreateNewExpression(
		fn,
		[]jsast.IExpression{jsast.CreateObjectExpression(props)},
	), nil
}

func (tr *Translator) jsArrayExpression(d def.Definition, sp *scope.Scope, n *ast.CompositeLit) (j jsast.ArrayExpression, err error) {
	var elements []jsast.IExpression

	for _, elt := range n.Elts {
		el, e := tr.expression(d, sp, elt)
		if e != nil {
			return j, e
		}
		elements = append(elements, el)
	}

	return jsast.CreateArrayExpression(elements...), nil
}

// property formats initializing structs in all the various ways
// e.g. User{"matt"},User{*name},User{name},User{Name:name}, etc.
func (tr *Translator) property(d def.Definition, sp *scope.Scope, c *ast.CompositeLit, idx int, n ast.Expr) (j jsast.Property, err error) {
	// fast-track: User{Name:name}, User{&name}
	switch t := n.(type) {
	case *ast.UnaryExpr:
		return tr.property(d, sp, c, idx, t.X)
	case *ast.KeyValueExpr:
		return tr.keyValueExpr(d, sp, c, t)
	}

	def, e := tr.index.DefinitionOf(d.Path(), c.Type)
	if e != nil {
		return j, e
	}
	st, ok := def.(struc.Struct)
	if !ok {
		return j, fmt.Errorf("property: expected a struct, but got a %T", def)
	}

	var fields []string
	for _, field := range st.Fields() {
		fields = append(fields, field.Name())
	}
	if idx >= len(fields) {
		return j, fmt.Errorf("property: expected idx=%d to be less than len(fields)=%d", idx, len(fields))
	}

	// User{"matt"},User{name},User{Settings{...}}
	key := jsast.CreateIdentifier(fields[idx])
	switch t := n.(type) {
	case *ast.Ident:
		val, e := tr.identifier(d, sp, t)
		if e != nil {
			return j, e
		}
		return jsast.CreateProperty(key, val, "init"), nil
	case *ast.BasicLit:
		val, e := tr.basiclit(d, sp, t)
		if e != nil {
			return j, e
		}
		return jsast.CreateProperty(key, val, "init"), nil
	// recurse
	case *ast.CompositeLit:
		val, e := tr.compositeLiteral(d, sp, t)
		if e != nil {
			return j, e
		}
		return jsast.CreateProperty(key, val, "init"), nil
	default:
		return j, unhandled("property", n)
	}
}

func (tr *Translator) keyValueExpr(d def.Definition, sp *scope.Scope, c *ast.CompositeLit, n *ast.KeyValueExpr) (j jsast.Property, err error) {
	// get the value
	val, e := tr.expression(d, sp, n.Value)
	if e != nil {
		return j, e
	}

	// get the definition of the composite type
	def, err := tr.index.DefinitionOf(d.Path(), c.Type)
	if err != nil {
		return j, err
	}
	st, ok := def.(struc.Struct)
	if !ok {
		return j, fmt.Errorf("keyValueExpr: expected struct, but got %T", def)
	}

	// turn into a property
	switch t := n.Key.(type) {
	case *ast.Ident:
		field := st.Field(t.Name)
		if field == nil {
			return j, fmt.Errorf("keyValueExpr: didn't expect field to be nil")
		}
		key := jsast.CreateIdentifier(field.Name())
		return jsast.CreateProperty(key, val, "init"), nil
	case *ast.BasicLit:
		key, e := tr.basiclit(d, sp, t)
		if e != nil {
			return j, e
		}
		return jsast.CreateProperty(key, val, "init"), nil
	default:
		return j, unhandled("keyValueExpr<key>", n.Key)
	}
}

func (tr *Translator) selectorExpr(d def.Definition, sp *scope.Scope, n *ast.SelectorExpr) (j jsast.MemberExpression, err error) {
	// (user.phone).number
	x, e := tr.expression(d, sp, n.X)
	if e != nil {
		return j, e
	}

	// get the definition of the selector
	def, e := tr.index.DefinitionOf(d.Path(), n)
	if e != nil {
		return j, e
	}

	log.Debugf("%s.%s -> %s", x, n.Sel.Name, def.Name())

	// alias based on the selector's definition
	name := n.Sel.Name
	switch t := def.(type) {
	case struc.Struct:
		// TODO: maybe change this later, struct fields
		// point directly to a field interface, rather
		// than original struct
		// log.Infof("%s.%s, original=%s", x, name, t.OriginalName())
		if name == t.OriginalName() {
			name = t.Name()
		} else {
			f := t.Field(name)
			if f != nil {
				name = f.Name()
			}
		}
	}

	return jsast.CreateMemberExpression(
		x,
		jsast.CreateIdentifier(name),
		false,
	), nil
}

func (tr *Translator) indexExpr(d def.Definition, sp *scope.Scope, n *ast.IndexExpr) (j jsast.MemberExpression, err error) {
	// (i)[0]
	x, e := tr.expression(d, sp, n.X)
	if e != nil {
		return j, e
	}

	// i([0])
	i, e := tr.expression(d, sp, n.Index)
	if e != nil {
		return j, e
	}

	return jsast.CreateMemberExpression(x, i, true), nil
}

func (tr *Translator) maybeBuiltinExpr(d def.Definition, sp *scope.Scope, n *ast.CallExpr) (jsast.IExpression, error) {
	id, ok := n.Fun.(*ast.Ident)
	if !ok {
		return nil, nil
	}

	switch id.Name {
	case "append":
		return tr.builtinAppend(d, sp, n.Args)
	case "len":
		return tr.builtinLen(d, sp, n.Args)
	case "copy":
		return tr.expression(d, sp, n.Args[0])
	case "make":
		return tr.expression(d, sp, n.Args[0])
	}

	return nil, nil
}

func (tr *Translator) maybeBuiltinStmt(d def.Definition, sp *scope.Scope, n *ast.CallExpr) (jsast.IStatement, error) {
	id, ok := n.Fun.(*ast.Ident)
	if !ok {
		return nil, nil
	}

	switch id.Name {
	case "println":
		return tr.builtinPrintln(d, sp, n.Args)
	case "panic":
		return tr.builtinPanic(d, sp, n.Args)
	}

	return nil, nil
}

func (tr *Translator) builtinAppend(d def.Definition, sp *scope.Scope, ns []ast.Expr) (jsast.IExpression, error) {
	var els []jsast.IExpression
	for _, n := range ns {
		x, e := tr.expression(d, sp, n)
		if e != nil {
			return nil, e
		}
		els = append(els, x)
	}

	if len(els) == 1 {
		return els[0], nil
	}

	return jsast.CreateCallExpression(
		jsast.CreateMemberExpression(
			els[0],
			jsast.CreateIdentifier("concat"),
			false,
		),
		els[1:],
	), nil
}

func (tr *Translator) builtinLen(d def.Definition, sp *scope.Scope, ns []ast.Expr) (jsast.IExpression, error) {
	var els []jsast.IExpression
	for _, n := range ns {
		x, e := tr.expression(d, sp, n)
		if e != nil {
			return nil, e
		}
		els = append(els, x)
	}

	return jsast.CreateMemberExpression(
		els[0],
		jsast.CreateIdentifier("length"),
		false,
	), nil
}

func (tr *Translator) builtinPrintln(d def.Definition, sp *scope.Scope, ns []ast.Expr) (jsast.IStatement, error) {
	var args []jsast.IExpression
	for _, n := range ns {
		x, e := tr.expression(d, sp, n)
		if e != nil {
			return nil, e
		}
		args = append(args, x)
	}

	return jsast.CreateExpressionStatement(
		jsast.CreateCallExpression(
			jsast.CreateMemberExpression(
				jsast.CreateIdentifier("console"),
				jsast.CreateIdentifier("log"),
				false,
			),
			args,
		),
	), nil
}

func (tr *Translator) builtinPanic(d def.Definition, sp *scope.Scope, ns []ast.Expr) (jsast.IStatement, error) {
	if len(ns) != 1 {
		return nil, errors.New("unhandled builtinPanic: only supports 1 argument")
	}

	x, e := tr.expression(d, sp, ns[0])
	if e != nil {
		return nil, e
	}

	return jsast.CreateThrowStatement(x), nil
}

func isUnderscoreVariable(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		if t.Name == "_" {
			return true
		}
	}
	return false
}

func (tr *Translator) arrayType(d def.Definition, sp *scope.Scope, n *ast.ArrayType) (jsast.IExpression, error) {
	return jsast.CreateArrayExpression(), nil
}

func (tr *Translator) chanType(d def.Definition, sp *scope.Scope, n *ast.ChanType) (jsast.IExpression, error) {
	return jsast.CreateNewExpression(
		jsast.CreateMemberExpression(
			jsast.CreateIdentifier("runtime"),
			jsast.CreateIdentifier("Channel"),
			false,
		),
		[]jsast.IExpression{},
	), nil
}

// get the identifier when possible
func getObjectType(expr ast.Expr) (ast.Expr, error) {
	var id *ast.Ident
	switch t := expr.(type) {
	case *ast.UnaryExpr:
		return getObjectType(t.X)
	case *ast.SelectorExpr:
		return getObjectType(t.Sel)
	case *ast.Ident:
		id = t
	default:
		return nil, unhandled("getObjectType<expr>", expr)
	}

	obj := id.Obj
	if obj == nil {
		return nil, fmt.Errorf("getObjectType object nil")
	}

	switch t := obj.Decl.(type) {
	case *ast.ValueSpec:
		if t.Type == nil {
			return nil, fmt.Errorf("getObjectType type nil")
		}
		return t.Type, nil
	default:
		return nil, unhandled("getObjectType<decl>", expr)
	}
}

func unique(s []string) []string {
	unique := make(map[string]bool, len(s))
	us := make([]string, len(unique))
	for _, elem := range s {
		if len(elem) != 0 {
			if !unique[elem] {
				us = append(us, elem)
				unique[elem] = true
			}
		}
	}
	return us
}

// zeroed returns an expression defaulted to its zero value.
func (tr *Translator) zeroed(d def.Definition, sp *scope.Scope, expr ast.Expr, name string) (jsast.IExpression, error) {
	x, e := tr.defaultValue(d, sp, expr)
	if e != nil {
		return nil, e
	}

	return defaulted(name, x), nil
}

// defaulted returns a defaulted identifier.
func defaulted(name string, expr jsast.IExpression) jsast.IExpression {
	return jsast.CreateBinaryExpression(jsast.CreateIdentifier(name), "||", expr)
}

func (tr *Translator) defaultValue(d def.Definition, sp *scope.Scope, expr ast.Expr) (jsast.IExpression, error) {
	switch t := expr.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string":
			return jsast.EmptyString, nil
		case "bool":
			return jsast.False, nil
		case "int":
			return jsast.Zero, nil
		case "error":
			// TODO: should this be undefined?
			return jsast.Null, nil
		case "nil":
			return jsast.Null, nil
		default:
			id := jsast.CreateIdentifier(t.Name)
			return jsast.CreateNewExpression(id, nil), nil
		}
	case *ast.MapType:
		return jsast.CreateObjectExpression(nil), nil
	case *ast.ArrayType:
		return jsast.CreateArrayExpression(), nil
	case *ast.InterfaceType:
		return jsast.Null, nil
	case *ast.StarExpr:
		return tr.defaultValue(d, sp, t.X)
	case *ast.SelectorExpr:
		x, e := tr.expression(d, sp, t.X)
		if e != nil {
			return nil, e
		}
		id := jsast.CreateIdentifier(t.Sel.Name)
		return jsast.CreateNewExpression(
			jsast.CreateMemberExpression(x, id, false),
			nil,
		), nil
	default:
		return nil, unhandled("defaultValue", expr)
	}
}

func (tr *Translator) maybeJSRaw(d def.Definition, sp *scope.Scope, cx *ast.CallExpr) (jsast.IExpression, error) {
	sel, ok := cx.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, nil
	}

	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil, nil
	}

	if x.Name != "js" {
		return nil, nil
	}

	switch sel.Sel.Name {
	case "RawFile":
		return tr.jsRawFile(d, sp, cx)
	case "Raw":
		return tr.jsRaw(d, sp, cx)
	default:
		return nil, nil
	}
}

func (tr *Translator) jsRawFile(d def.Definition, sp *scope.Scope, cx *ast.CallExpr) (jsast.IExpression, error) {
	if len(cx.Args) == 0 {
		return nil, nil
	}

	lit, ok := cx.Args[0].(*ast.BasicLit)
	if !ok {
		return nil, nil
	}
	filepath := lit.Value[1 : len(lit.Value)-1]

	return jsast.CreateMemberExpression(
		jsast.CreateIdentifier("pkg"),
		jsast.CreateString(filepath),
		true,
	), nil
}

func (tr *Translator) jsRaw(d def.Definition, sp *scope.Scope, cx *ast.CallExpr) (jsast.IExpression, error) {
	if len(cx.Args) == 0 {
		return nil, nil
	}

	lit, ok := cx.Args[0].(*ast.BasicLit)
	if !ok {
		return nil, nil
	}

	src := lit.Value[1 : len(lit.Value)-1]
	return jsast.CreateRaw(src), nil
}

// func (tr *Translator) maybeJSRewrite(d def.Definition, sp *scope.Scope, n *ast.CallExpr) (jsast.IExpression, error) {
// 	sel, ok := n.Fun.(*ast.SelectorExpr)
// 	if !ok {
// 		return nil, nil
// 	}

// 	// find the corresponding declaration (if there is one)
// 	def, err := tr.db.DefinitionOf(tr.info, sel.Sel)
// 	if err != nil {
// 		return nil, err
// 	} else if def == nil {
// 		return nil, nil
// 	}

// 	// check if we have a rewrite (filled in during inspection)
// 	// rewrite := def.Rewrite
// 	// if rewrite == nil {
// 	// 	return nil, nil
// 	// }

// 	// map out the replacements
// 	replacements := map[string]string{}
// 	for i, arg := range n.Args {
// 		x, e := tr.expression(d, sp, arg)
// 		if e != nil {
// 			return nil, e
// 		}

// 		xs, ok := x.(fmt.Stringer)
// 		if !ok {
// 			return nil, errors.New("maybeJSRewrite(): expression not a stringer")
// 		}

// 		if i >= len(def.Params) {
// 			return nil, errors.New("maybeJSRewrite(): doesn't support param spreads yet")
// 		}

// 		replacements[def.Params[i]] = xs.String()
// 	}

// 	// make the substitutions
// 	expr := rewrite.Expression
// 	for i, variable := range rewrite.Variables {
// 		replacement, isset := replacements[variable]
// 		if !isset {
// 			return nil, errors.New("js.Rewrite() variable doesn't match the function parameter")
// 		}
// 		expr = strings.Replace(expr, "$"+strconv.Itoa(i+1), replacement, -1)
// 	}

// 	return jsast.CreateRaw(expr), nil
// }

func (tr *Translator) maybeError(d def.Definition, sp *scope.Scope, n *ast.CallExpr) (jsast.IExpression, error) {
	sel, ok := n.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, nil
	}

	if sel.Sel.Name != "Error" {
		return nil, nil
	}

	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil, nil
	}

	// obj := tr.info.ObjectOf(id)
	// if obj == nil {
	// 	return nil, nil
	// }

	// // TODO: better way to check the error type?
	// if obj.Type().String() != "error" {
	// 	return nil, nil
	// }

	return jsast.CreateMemberExpression(
		jsast.CreateIdentifier(id.Name),
		jsast.CreateIdentifier("message"),
		false,
	), nil
}

func (tr *Translator) maybeAwait(d def.Definition, sp *scope.Scope, n *ast.CallExpr) (jsast.IExpression, error) {

	// ignore these
	switch n.Fun.(type) {
	case *ast.FuncLit:
		return nil, nil
	case *ast.ArrayType:
		return nil, nil
	}

	def, e := tr.index.DefinitionOf(d.Path(), n.Fun)
	if e != nil {
		return nil, e
	}

	fn, ok := def.(fn.Function)
	if !ok {
		return nil, nil
	}

	isAsync, e := fn.IsAsync()
	if e != nil {
		return nil, e
	}

	if !isAsync {
		return nil, nil
	}

	callee, e := tr.expression(d, sp, n.Fun)
	if e != nil {
		return nil, e
	}

	var args []jsast.IExpression
	for _, arg := range n.Args {
		v, e := tr.expression(d, sp, arg)
		if e != nil {
			return nil, e
		}
		args = append(args, v)
	}

	return jsast.CreateAwaitExpression(
		jsast.CreateCallExpression(
			callee,
			args,
		),
	), nil
}

// func maybeAlias(decl *def.Definition, name string) (alias string) {
// 	if decl == nil {
// 		return name
// 	}

// 	for _, field := range decl.Fields {
// 		if field.Name != name {
// 			continue
// 		}

// 		if field.Tag != nil {
// 			return field.Tag.Name
// 		}
// 	}

// 	return name
// }

// tries to get the rightmost identifier from an expression
func getIdentifier(n ast.Expr) (*ast.Ident, error) {
	switch t := n.(type) {
	case *ast.Ident:
		return t, nil
	case *ast.StarExpr:
		return getIdentifier(t.X)
	case *ast.SelectorExpr:
		return t.Sel, nil
	default:
		return nil, fmt.Errorf("unhandled getIdentifier: %T", n)
	}
}

func unhandled(fn string, n interface{}) error {
	return fmt.Errorf("%s in %s() is not implemented yet", reflect.TypeOf(n), fn)
}
