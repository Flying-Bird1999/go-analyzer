package project

import (
	"go/ast"
	"go/token"
)

type Options struct {
	ExcludeDirs []string
}

type Project struct {
	Root       string
	ModulePath string
	Packages   map[string]*Package
}

type Package struct {
	Path  string
	Dir   string
	Name  string
	Files []*File
}

type File struct {
	Package *Package
	Path    string
	FileSet *token.FileSet
	AST     *ast.File
	Imports map[string]string
}
