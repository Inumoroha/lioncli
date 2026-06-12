package rag

import "fmt"

// CodeChunk 代码块数据模型
type CodeChunk struct {
	FilePath  string `json:"file_path"`  // 代码文件路径
	ChunkType string `json:"chunk_type"` // 代码块类型: file/type/function/method
	Name      string `json:"name"`       // 代码块名称
	Content   string `json:"content"`    // 代码块文本内容
	StartLine int    `json:"start_line"` // 代码块开始行号
	EndLine   int    `json:"end_line"`   // 代码块结束行号
}

// NewFileChunk 构造一个文件级别的代码块。
func NewFileChunk(filePath, content string) CodeChunk {
	return CodeChunk{
		FilePath:  filePath,
		ChunkType: "file",
		Name:      filePath,
		Content:   content,
	}
}

// NewTypeChunk 构造一个类型级别(struct/interface)的代码块。
func NewTypeChunk(filePath, typeName, content string, startLine, endLine int) CodeChunk {
	return CodeChunk{
		FilePath:  filePath,
		ChunkType: "type",
		Name:      typeName,
		Content:   content,
		StartLine: startLine,
		EndLine:   endLine,
	}
}

// NewMethodChunk 构造一个方法级别(带接收者的函数)的代码块。
func NewMethodChunk(filePath, methodName, content string, startLine, endLine int) CodeChunk {
	return CodeChunk{
		FilePath:  filePath,
		ChunkType: "method",
		Name:      methodName,
		Content:   content,
		StartLine: startLine,
		EndLine:   endLine,
	}
}

// NewFunctionChunk 构造一个普通函数级别(无接收者)的代码块。
func NewFunctionChunk(filePath, functionName, content string, startLine, endLine int) CodeChunk {
	return CodeChunk{
		FilePath:  filePath,
		ChunkType: "function",
		Name:      functionName,
		Content:   content,
		StartLine: startLine,
		EndLine:   endLine,
	}
}

// ToEmbeddingText 生成用于 Embedding 的文本表示。
func (c *CodeChunk) ToEmbeddingText() string {
	return fmt.Sprintf("[%s:%s] %s", c.ChunkType, c.Name, c.Content)
}
