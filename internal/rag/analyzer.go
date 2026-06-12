package rag

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type GoTypeKind string

const (
	GoTypeStruct    GoTypeKind = "struct"
	GoTypeInterface GoTypeKind = "interface"
	GoTypeOther     GoTypeKind = "other"
)

type GoTypeInfo struct {
	FilePath      string
	PackageDir    string
	PackageName   string
	Name          string
	Kind          GoTypeKind
	Methods       []string
	EmbeddedTypes []string
}

type AnalysisResult struct {
	Relations []CodeRelation
	Types     []GoTypeInfo
}

type CodeAnalyzer struct {
}

func NewCodeAnalyzer() *CodeAnalyzer {
	return &CodeAnalyzer{}
}

func (a *CodeAnalyzer) AnalyzeFile(filePath string) (AnalysisResult, error) {
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		return AnalysisResult{}, err
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, contentBytes, parser.ParseComments)
	if err != nil {
		return AnalysisResult{}, err
	}

	result := AnalysisResult{}
	pkgDir := filepath.Dir(filePath)
	methodsByType := make(map[string][]string)
	typeInfos := make(map[string]*GoTypeInfo)

	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		result.Relations = append(result.Relations, CodeRelation{
			FromFile:     filePath,
			FromName:     file.Name.Name,
			ToName:       importPath,
			RelationType: "imports",
		})
	}

	for _, decl := range file.Decls {
		switch node := decl.(type) {
		case *ast.GenDecl:
			if node.Tok != token.TYPE {
				continue
			}
			for _, spec := range node.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				info := &GoTypeInfo{
					FilePath:    filePath,
					PackageDir:  pkgDir,
					PackageName: file.Name.Name,
					Name:        typeSpec.Name.Name,
					Kind:        GoTypeOther,
				}
				switch typeNode := typeSpec.Type.(type) {
				case *ast.StructType:
					info.Kind = GoTypeStruct
					for _, field := range typeNode.Fields.List {
						if len(field.Names) > 0 {
							continue
						}
						embedded := exprName(field.Type)
						if embedded == "" {
							continue
						}
						info.EmbeddedTypes = append(info.EmbeddedTypes, embedded)
						result.Relations = append(result.Relations, CodeRelation{
							FromFile:     filePath,
							FromName:     info.Name,
							ToFile:       filePath,
							ToName:       embedded,
							RelationType: "embeds",
						})
					}
				case *ast.InterfaceType:
					info.Kind = GoTypeInterface
					for _, field := range typeNode.Methods.List {
						if len(field.Names) == 0 {
							embedded := exprName(field.Type)
							if embedded == "" {
								continue
							}
							info.EmbeddedTypes = append(info.EmbeddedTypes, embedded)
							result.Relations = append(result.Relations, CodeRelation{
								FromFile:     filePath,
								FromName:     info.Name,
								ToFile:       filePath,
								ToName:       embedded,
								RelationType: "embeds",
							})
							continue
						}
						for _, name := range field.Names {
							info.Methods = append(info.Methods, name.Name)
						}
					}
				}
				typeInfos[info.Name] = info
			}
		case *ast.FuncDecl:
			caller := file.Name.Name + "." + node.Name.Name
			if node.Recv != nil && len(node.Recv.List) > 0 {
				recvName := getReceiverTypeName(node.Recv.List[0].Type)
				caller = recvName + "." + node.Name.Name
				methodsByType[recvName] = append(methodsByType[recvName], node.Name.Name)
			}
			if node.Body == nil {
				continue
			}
			ast.Inspect(node.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				callee := calledName(call.Fun)
				if callee == "" {
					return true
				}
				result.Relations = append(result.Relations, CodeRelation{
					FromFile:     filePath,
					FromName:     caller,
					ToName:       callee,
					RelationType: "calls",
				})
				return true
			})
		}
	}

	for typeName, methods := range methodsByType {
		info, ok := typeInfos[typeName]
		if !ok {
			info = &GoTypeInfo{
				FilePath:    filePath,
				PackageDir:  pkgDir,
				PackageName: file.Name.Name,
				Name:        typeName,
				Kind:        GoTypeOther,
			}
			typeInfos[typeName] = info
		}
		info.Methods = append(info.Methods, methods...)
		for _, method := range methods {
			result.Relations = append(result.Relations, CodeRelation{
				FromFile:     filePath,
				FromName:     typeName,
				ToFile:       filePath,
				ToName:       typeName + "." + method,
				RelationType: "contains",
			})
		}
	}

	for _, info := range typeInfos {
		info.Methods = dedupeStrings(info.Methods)
		info.EmbeddedTypes = dedupeStrings(info.EmbeddedTypes)
		result.Types = append(result.Types, *info)
	}

	return result, nil
}

// InferImplementations does a local-package, method-set-based implementation inference.
func (a *CodeAnalyzer) InferImplementations(types []GoTypeInfo) []CodeRelation {
	grouped := make(map[string][]GoTypeInfo)
	for _, info := range types {
		key := info.PackageDir + "::" + info.PackageName
		grouped[key] = append(grouped[key], info)
	}

	var relations []CodeRelation
	for _, group := range grouped {
		byName := make(map[string]GoTypeInfo)
		for _, info := range group {
			byName[info.Name] = info
		}

		for _, candidate := range group {
			if candidate.Kind == GoTypeInterface {
				continue
			}
			candidateMethods := stringSet(candidate.Methods)
			for _, iface := range group {
				if iface.Kind != GoTypeInterface {
					continue
				}
				required := a.resolveInterfaceMethods(iface, byName, nil)
				if len(required) == 0 {
					continue
				}
				if hasAll(candidateMethods, required) {
					relations = append(relations, CodeRelation{
						FromFile:     candidate.FilePath,
						FromName:     candidate.Name,
						ToFile:       iface.FilePath,
						ToName:       iface.Name,
						RelationType: "implements",
					})
				}
			}
		}
	}
	return dedupeRelations(relations)
}

func (a *CodeAnalyzer) resolveInterfaceMethods(info GoTypeInfo, byName map[string]GoTypeInfo, visited map[string]struct{}) map[string]struct{} {
	if visited == nil {
		visited = make(map[string]struct{})
	}
	if _, ok := visited[info.Name]; ok {
		return nil
	}
	visited[info.Name] = struct{}{}

	methods := stringSet(info.Methods)
	for _, embedded := range info.EmbeddedTypes {
		next, ok := byName[embedded]
		if !ok || next.Kind != GoTypeInterface {
			continue
		}
		for method := range a.resolveInterfaceMethods(next, byName, visited) {
			methods[method] = struct{}{}
		}
	}
	return methods
}

func calledName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.SelectorExpr:
		left := exprName(node.X)
		if left == "" {
			return node.Sel.Name
		}
		return left + "." + node.Sel.Name
	case *ast.IndexExpr:
		return calledName(node.X)
	case *ast.IndexListExpr:
		return calledName(node.X)
	default:
		return exprName(node)
	}
}

func exprName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.StarExpr:
		return exprName(node.X)
	case *ast.SelectorExpr:
		left := exprName(node.X)
		if left == "" {
			return node.Sel.Name
		}
		return left + "." + node.Sel.Name
	case *ast.IndexExpr:
		return exprName(node.X)
	case *ast.IndexListExpr:
		return exprName(node.X)
	default:
		var builder strings.Builder
		_ = printer.Fprint(&builder, token.NewFileSet(), expr)
		return builder.String()
	}
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var result []string
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func dedupeRelations(relations []CodeRelation) []CodeRelation {
	seen := make(map[string]struct{}, len(relations))
	var result []CodeRelation
	for _, relation := range relations {
		key := relation.FromFile + "|" + relation.FromName + "|" + relation.ToFile + "|" + relation.ToName + "|" + relation.RelationType

		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, relation)
	}
	return result
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	return set
}

func hasAll(candidate map[string]struct{}, required map[string]struct{}) bool {
	for method := range required {
		if _, ok := candidate[method]; !ok {
			return false
		}
	}
	return true
}
