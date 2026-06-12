// Package segment 用纯 Go 分词库 gse 提供中文词级切分,供 rag / memory 的查询分词复用。
//
// 现状:rag/memory 的 tokenizer 把连续汉字串当成一整个 token(粗粒度),中文召回只能
// 整块子串匹配。本包把这种汉字块进一步切成词("登录功能" → "登录"/"功能"),提升召回。
//
// 词典走 gse 的内嵌简繁词典(NewEmbed("zh")),不依赖外部词典文件,因此从任意工作目录
// 启动都可用。加载较重(数十万词),故用 sync.Once 懒加载一次;加载失败时 Cut 返回 nil,
// 调用方自然回退到原有的整块 token,不影响功能。
package segment

import (
	"sync"
	"unicode"

	"github.com/go-ego/gse"
)

var (
	once  sync.Once
	seg   gse.Segmenter
	ready bool
)

func load() {
	s, err := gse.NewEmbed("zh")
	if err != nil {
		return
	}
	seg = s
	ready = true
}

// Cut 把一段文本切成词(HMM 开启,可识别词典外的新词)。
// 分词器未就绪(加载失败)时返回 nil,调用方应回退到原 token。
// gse 的 Cut 在词典加载完成后只读,可并发调用。
func Cut(text string) []string {
	if text == "" {
		return nil
	}
	once.Do(load)
	if !ready {
		return nil
	}
	return seg.Cut(text, true)
}

// HasHan 报告字符串是否包含汉字(用于判断某个 token 是否值得送去分词)。
func HasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
