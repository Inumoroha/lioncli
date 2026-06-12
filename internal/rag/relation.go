package rag

// 代码关系数据模型（用于构建代码关系图谱）
type CodeRelation struct {
	FromFile     string `json:"from_file"`         // 源文件路径
	FromName     string `json:"from_name"`         // 源名称（类名或方法名）
	ToFile       string `json:"to_file,omitempty"` // 目标文件路径
	ToName       string `json:"to_name,omitempty"` // 目标名称
	RelationType string `json:"relation_type"`     // 关系类型
}
