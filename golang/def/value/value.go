package value

import (
	"errors"
	"go/ast"
	"go/types"
	"strings"

	"github.com/matthewmueller/golly/golang/def"
	"github.com/matthewmueller/golly/golang/index"

	"golang.org/x/tools/go/loader"
)

// Value interface
type Value interface {
	def.Definition
	Node() *ast.ValueSpec
}

var _ Value = (*valuedef)(nil)

type valuedef struct {
	exported  bool
	path      string
	name      string
	id        string
	index     *index.Index
	node      *ast.ValueSpec
	kind      types.Type
	processed bool
	deps      []def.Definition
}

// NewValue fn
func NewValue(index *index.Index, info *loader.PackageInfo, n *ast.ValueSpec) (def.Definition, error) {
	packagePath := info.Pkg.Path()
	names := []string{}
	exported := false

	for _, ident := range n.Names {
		obj := info.ObjectOf(ident)
		if obj.Exported() {
			exported = true
		}
		names = append(names, ident.Name)
	}

	name := strings.Join(names, ",")
	idParts := []string{packagePath, name}
	id := strings.Join(idParts, " ")

	return &valuedef{
		exported: exported,
		path:     packagePath,
		name:     name,
		id:       id,
		index:    index,
		node:     n,
	}, nil
}

func (d *valuedef) process() (err error) {
	seen := map[string]bool{}
	_ = seen

	ast.Inspect(d.node, func(n ast.Node) bool {
		switch t := n.(type) {
		}

		return true
	})

	d.processed = true
	return err
}

func (d *valuedef) ID() string {
	return d.id
}

func (d *valuedef) Name() string {
	return d.name
}

func (d *valuedef) Path() string {
	return d.path
}

func (d *valuedef) Dependencies() ([]def.Definition, error) {
	return nil, errors.New("valuedef.Dependencies() not implemented yet")
}

func (d *valuedef) Exported() bool {
	return d.exported
}

func (d *valuedef) Omitted() bool {
	return false
}

func (d *valuedef) Node() *ast.ValueSpec {
	return d.node
}

func (d *valuedef) Type() types.Type {
	return d.kind
}

func (d *valuedef) Imports() map[string]string {
	return d.index.GetImports(d.path)
}

func (d *valuedef) FromRuntime() bool {
	return false
}
