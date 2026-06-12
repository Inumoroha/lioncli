package rag

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

/**
 * 代码分块器：将代码文件切分为适合 Embedding 的粒度
 * <p>
 * 策略：
 * - 非 Go 文件：整个文件作为一个 chunk
 * - Go 文件：结构体级别 + 函数级别分块（大函数单独成块）
 */

// 单个 chunk 最大字符数
// （中文约 1 字符 = 2~3 token，2000 字符 ≈ 4000~6000 token，安全适配 8192 上下文）
const defaultMaxChunkChars = 2000

type CodeChunker struct {
	maxChunkChars int
}

// NewCodeChunker 创建一个默认的代码分块器
func NewCodeChunker() *CodeChunker {
	return &CodeChunker{maxChunkChars: defaultMaxChunkChars}
}

// ChunkFile 代码分块器的主方法，对单个代码文件进行分块
// 在 Go 中，通常用 string 来表示文件路径。
// 返回值变为了多值：第一个是切片（对应 Java 的 List），第二个是 error（对应 throws IOException）。
func (c *CodeChunker) ChunkFile(filePath string) ([]CodeChunk, error) {
	// 1.读取文件内容，os.ReadFile 返回的是 []byte 字节切片和 error
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		// 如果发生错误（比如文件不存在、没权限），直接将 error 返回给上一层
		return nil, err
	}

	// 将 []byte 转换为字符串
	content := string(contentBytes)

	// 非 Go 文件:整块按大小切分
	if filepath.Ext(filePath) != ".go" {
		return c.chunkLargeText(filePath, content)
	}

	// Go 文件:AST 解析分块
	return c.chunkGoFile(filePath, content)
}

// chunkLargeText 将大文本按行分段，每段长度尽量不超过 maxChunkChars
func (c *CodeChunker) chunkLargeText(filePath, content string) ([]CodeChunk, error) {

	// 1. 如果内容没超限，直接返回单个文件块
	if len(content) <= c.maxChunkChars {
		// 直接调用我们之前定义的工厂函数
		return []CodeChunk{NewFileChunk(filePath, content)}, nil
	}

	// 2. 统一换行符，避免正则带来的性能损耗，然后按行分割
	normalizedContent := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalizedContent, "\n")

	// 3. strings.Builder 是 Go 中对应 Java StringBuilder 的高效实现
	var chunks []CodeChunk
	var builder strings.Builder
	segmentIndex := 1
	startLine := 1

	for i, line := range lines {
		// len(line) + 1 是算上了即将追加的 "\n" 的长度
		if builder.Len()+len(line)+1 > c.maxChunkChars && builder.Len() > 0 {
			// 构造类似 "main.go#1" 的名称
			chunkName := fmt.Sprintf("%s#%d", filePath, segmentIndex)

			// 直接使用结构体字面量进行初始化（Go 的惯用法）
			chunk := CodeChunk{
				FilePath:  filePath,
				ChunkType: "file",
				Name:      chunkName,
				Content:   strings.TrimSpace(builder.String()),
				StartLine: startLine,
				EndLine:   i, // 对应原 Java 代码，这里的 i 恰好是上一块的结束行
			}
			chunks = append(chunks, chunk)

			// 清空 Builder 状态，准备记录下一块
			builder.Reset()
			segmentIndex++
			startLine = i + 1
		}

		builder.WriteString(line)
		builder.WriteString("\n")
	}

	// 4. 处理循环结束后剩余的内容
	if builder.Len() > 0 {
		chunkName := fmt.Sprintf("%s#%d", filePath, segmentIndex)
		chunk := CodeChunk{
			FilePath:  filePath,
			ChunkType: "file",
			Name:      chunkName,
			Content:   strings.TrimSpace(builder.String()),
			StartLine: startLine,
			EndLine:   len(lines),
		}
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// chunkGoFile 通过 AST 解析对 Go 文件进行分块
func (c *CodeChunker) chunkGoFile(filePath, content string) ([]CodeChunk, error) {

	// 1. 创建 FileSet，这是 Go AST 用来记录所有节点在文件中对应行号的核心工具
	fset := token.NewFileSet()

	// 2. 调用标准库 parser 解析代码字符串, 解析模式选 0 即可（也可以用 parser.ParseComments 保留注释）
	file, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil || file == nil {
		// 解析失败（比如代码有语法错误），兜底使用按行数切块
		return c.chunkLargeText(filePath, content)
	}

	var chunks []CodeChunk

	// 3. 遍历 AST 树中的所有顶级声明 (Declarations)
	for _, decl := range file.Decls {
		switch node := decl.(type) {

		// 情况 A：通用声明 (GenDecl) —— 包含 import、const、var 以及 type (结构体/接口)
		case *ast.GenDecl:
			// 我们只关心 type 声明 (结构体或接口)
			if node.Tok == token.TYPE {
				for _, spec := range node.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok {
						// 获取类型名称 (比如 "CodeChunk")
						typeName := typeSpec.Name.Name

						// 通过 fset 获取在源码中的真实起始和结束行号
						startLine := fset.Position(node.Pos()).Line
						endLine := fset.Position(node.End()).Line

						typeContent := extractLines(content, startLine, endLine)

						// 记录：类型级别 chunk (对应 Java 里的 class)
						chunks = append(chunks, NewTypeChunk( // 如果你修改了数据模型，这里可以叫 NewTypeChunk
							filePath,
							typeName,
							typeContent,
							startLine,
							endLine,
						))
					}
				}
			}

		// 情况 B：函数或方法声明 (FuncDecl)
		case *ast.FuncDecl:
			funcName := node.Name.Name
			startLine := fset.Position(node.Pos()).Line
			endLine := fset.Position(node.End()).Line
			funcContent := extractLines(content, startLine, endLine)

			// 有接收者 (Receiver) 才是方法,否则是普通函数。
			// 旧实现对两者都调 NewMethodChunk,导致普通函数被错标成 "method",
			// 而 NewFunctionChunk 成了永不被调用的死代码。
			if node.Recv != nil && len(node.Recv.List) > 0 {
				chunkName := funcName
				if recvName := getReceiverTypeName(node.Recv.List[0].Type); recvName != "" {
					chunkName = recvName + "." + funcName // 拼成类似 "*CodeChunk.ToEmbeddingText"
				}
				chunks = append(chunks, NewMethodChunk(filePath, chunkName, funcContent, startLine, endLine))
			} else {
				chunks = append(chunks, NewFunctionChunk(filePath, funcName, funcContent, startLine, endLine))
			}
		}
	}

	// 4. 如果没提取到任何类型或函数，走兜底逻辑
	if len(chunks) == 0 {
		return c.chunkLargeText(filePath, content)
	}

	return chunks, nil
}

// 提取特定行文本的基础工具
func extractLines(content string, startLine, endLine int) string {
	normalizedContent := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalizedContent, "\n")
	if startLine < 1 {
		startLine = 1
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > endLine {
		return ""
	}

	var builder strings.Builder
	for i := startLine - 1; i < endLine; i++ {
		if i < 0 || i >= len(lines) {
			continue
		}
		builder.WriteString(lines[i])
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}

// getReceiverTypeName 从接收者 AST 节点提取类型名:
// `(c *CodeChunk)` → `CodeChunk`，并处理泛型接收者 `(c *Foo[T])`。
func getReceiverTypeName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.StarExpr:
		return getReceiverTypeName(node.X)
	case *ast.IndexExpr:
		return getReceiverTypeName(node.X)
	case *ast.IndexListExpr:
		return getReceiverTypeName(node.X)
	default:
		return exprString(node)
	}
}

func exprString(expr ast.Expr) string {
	var builder strings.Builder
	_ = printer.Fprint(&builder, token.NewFileSet(), expr)
	return builder.String()
}
