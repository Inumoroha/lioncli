package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/mark3labs/mcp-go/client"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// Resource 是对外暴露的、与 mcp 库类型解耦的资源描述。
// Key 是带 server 前缀的稳定标识，用于在 ReadResource 时路由回原始 (server, URI)。
type Resource struct {
	Key         string `json:"key"`
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ResourceContent 是 ReadResource 返回的内容片段。
// MCP 协议下一个资源可能返回多段内容（文本 + 二进制混合），所以是切片。
// 文本内容填 Text，二进制填 Blob（base64），两者互斥。
type ResourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// resourceEntry 记录一个资源属于哪个 server，以及它在 server 内的原始 URI。
type resourceEntry struct {
	serverName  string
	originalURI string
	resource    Resource
}

func toResource(serverName string, r mcplib.Resource) Resource {
	return Resource{
		Key:         keyOf(serverName, r.URI),
		URI:         r.URI,
		Name:        r.Name,
		Description: r.Description,
		MIMEType:    r.MIMEType,
	}
}

// toResourceContents 把 mcp 库返回的 ResourceContents 接口切片转成对外的 ResourceContent。
// 协议里 contents 是 oneof（Text/Blob），但实际反序列化常见到指针或值两种形态，所以这里
// 同时匹配两种，再兜底走 JSON 反序列化以容错。
func toResourceContents(in []mcplib.ResourceContents) []ResourceContent {
	out := make([]ResourceContent, 0, len(in))
	for _, c := range in {
		switch v := c.(type) {
		case mcplib.TextResourceContents:
			out = append(out, ResourceContent{URI: v.URI, MIMEType: v.MIMEType, Text: v.Text})
		case *mcplib.TextResourceContents:
			out = append(out, ResourceContent{URI: v.URI, MIMEType: v.MIMEType, Text: v.Text})
		case mcplib.BlobResourceContents:
			out = append(out, ResourceContent{URI: v.URI, MIMEType: v.MIMEType, Blob: v.Blob})
		case *mcplib.BlobResourceContents:
			out = append(out, ResourceContent{URI: v.URI, MIMEType: v.MIMEType, Blob: v.Blob})
		default:
			var fallback ResourceContent
			if raw, err := json.Marshal(c); err == nil {
				_ = json.Unmarshal(raw, &fallback)
			}
			out = append(out, fallback)
		}
	}
	return out
}

// loadResources 拉取每个声明了 resources capability 的 server 的资源清单（分页），
// 写入 m.resources。Initialize 阶段一次性调用，每页写入持 m.mu 写锁。
func (m *MCPManager) loadResources(ctx context.Context) {
	log.Println("=== 📚 正在获取所有 MCP Server 提供的资源列表 ===")
	for serverName, s := range m.servers {
		if s.capabilities.Resources == nil {
			continue
		}
		resources, err := listAllResources(ctx, s.client)
		if err != nil {
			log.Printf("❌ 获取 MCP Server '%s' 的资源列表失败: %v", serverName, err)
			continue
		}
		m.mu.Lock()
		for _, r := range resources {
			rr := toResource(serverName, r)
			m.resources[rr.Key] = resourceEntry{
				serverName:  serverName,
				originalURI: r.URI,
				resource:    rr,
			}
			log.Printf("✅ MCP Server '%s' 提供资源: %s", serverName, rr.Key)
		}
		m.mu.Unlock()
	}
}

// listAllResources 走完所有分页，返回 server 上的全部资源。见 listAllTools 的注释。
func listAllResources(ctx context.Context, c *client.Client) ([]mcplib.Resource, error) {
	var out []mcplib.Resource
	var cursor mcplib.Cursor
	for page := 0; page < maxListPages; page++ {
		req := mcplib.ListResourcesRequest{}
		req.Params.Cursor = cursor
		res, err := c.ListResources(ctx, req)
		if err != nil {
			return nil, err
		}
		out = append(out, res.Resources...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
	return nil, fmt.Errorf("分页超出上限 %d，疑似 server 游标行为异常", maxListPages)
}

// AllResources 返回所有 server 提供的资源描述（带前缀 key）。
func (m *MCPManager) AllResources() []Resource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Resource, 0, len(m.resources))
	for _, e := range m.resources {
		out = append(out, e.resource)
	}
	return out
}

// ReadResource 通过带前缀的 key 读取资源内容，路由到对应 server。
func (m *MCPManager) ReadResource(ctx context.Context, key string) ([]ResourceContent, error) {
	m.mu.RLock()
	entry, ok := m.resources[key]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown resource: %s", key)
	}
	s, ok := m.servers[entry.serverName]
	if !ok {
		return nil, fmt.Errorf("server %s not connected", entry.serverName)
	}

	req := mcplib.ReadResourceRequest{}
	req.Params.URI = entry.originalURI

	res, err := s.client.ReadResource(ctx, req)
	if err != nil {
		return nil, err
	}
	return toResourceContents(res.Contents), nil
}
