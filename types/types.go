package types

import (
	"go/ast"

	"github.com/fatih/structtag"
)

// Script struct
type Script struct {
	Name     string
	Packages []*Package
	RawFiles []*File
}

// Package struct
type Package struct {
	Path         string
	Declarations []*Declaration
	Exports      []*Declaration

	// maps[alias]: packagePath
	Dependencies map[string]*Package
}

// Declaration struct
type Declaration struct {
	Exported bool
	From     string
	ID       string
	Node     ast.Decl
	Name     string

	// Just names for now because
	// we only need the names right
	// now for js.Rewrite
	Params []string

	// Note that this represents *all*
	// imports in the file, not just
	// the ones that the declaration
	// uses. We use this information
	// to fill in the package dependencies
	// are *only* the imports the
	// declarations use
	// Imports map[string]string

	// Includes []*File

	// The following are available after inspection
	JSTag        *structtag.Tag
	Dependencies []*Declaration
	Rewrite      *Rewrite
	Async        bool
}

// Rewrite struct for js.Rewrite
type Rewrite struct {
	Expression string
	Variables  []string
}

// File struct
type File struct {
	Name   string
	Source string
}