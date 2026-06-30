package project

import (
	"go/ast"
	"go/token"
)

type Project struct {
	Root        string
	ModulePath  string
	Packages    map[string]*Package
	Diagnostics []LoadDiagnostic
}

type LoadDiagnostic struct {
	Code    string
	File    string
	Message string
}

type Package struct {
	Path  string
	Files []*File
}

type File struct {
	Package *Package
	Path    string
	FileSet *token.FileSet
	AST     *ast.File
	Imports map[string]string
}
