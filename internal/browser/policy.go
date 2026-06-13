package browser

import (
	"os"
	"regexp"
	"strings"
)

// defaultSensitivePatterns 是内置的敏感页面 glob 规则(从 Java 版 PaiCLI 移植)。
// 命中这些页面时,浏览器改写类操作会被强制单步审批。
var defaultSensitivePatterns = []string{
	"*://*.bank.*/*",
	"*://*.alipay.com/*",
	"*://*.paypal.com/*",
	"*://*.stripe.com/*",
	"*://github.com/settings/*",
	"*://*.github.com/settings/*",
	"*://github.com/*/settings/*",
	"*://*.github.com/*/settings/*",
	"*://paypal.com/*",
	"*://*.feishu.cn/admin/*",
	"*://*.larksuite.com/admin/*",
	"*://*.console.cloud.google.com/*",
	"*://*.console.aws.amazon.com/*",
	"*://*.portal.azure.com/*",
}

type rule struct {
	pattern string
	regex   *regexp.Regexp
}

// SensitivePagePolicy 把 glob 规则编译成正则,判断某 URL 是否为敏感页面。
type SensitivePagePolicy struct {
	rules []rule
}

// NewSensitivePagePolicy 用内置规则 + 可选的用户规则文件构建策略。
// userRulesFile 为空或读不到时只用内置规则(不阻断)。文件内每行一个 glob,# 开头为注释。
func NewSensitivePagePolicy(userRulesFile string) *SensitivePagePolicy {
	patterns := loadPatterns(userRulesFile)
	rules := make([]rule, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(globToRegex(strings.ToLower(p)))
		if err != nil {
			continue // 跳过非法 glob,不影响其余规则
		}
		rules = append(rules, rule{pattern: p, regex: re})
	}
	return &SensitivePagePolicy{rules: rules}
}

// Match 返回 url 是否命中敏感规则,以及命中的原始 glob。
func (p *SensitivePagePolicy) Match(url string) (bool, string) {
	if strings.TrimSpace(url) == "" {
		return false, ""
	}
	normalized := strings.ToLower(url)
	for _, r := range p.rules {
		if r.regex.MatchString(normalized) {
			return true, r.pattern
		}
	}
	return false, ""
}

// IsSensitive 是 Match 的布尔便捷封装。
func (p *SensitivePagePolicy) IsSensitive(url string) bool {
	matched, _ := p.Match(url)
	return matched
}

func loadPatterns(userRulesFile string) []string {
	patterns := make([]string, 0, len(defaultSensitivePatterns)+4)
	patterns = append(patterns, defaultSensitivePatterns...)
	if userRulesFile == "" {
		return patterns
	}
	data, err := os.ReadFile(userRulesFile)
	if err != nil {
		return patterns // 读取失败保留默认规则,不阻断主流程
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			patterns = append(patterns, trimmed)
		}
	}
	return patterns
}

// globToRegex 把 glob(* ?)编译成锚定整串的正则,其余正则元字符转义。
func globToRegex(glob string) string {
	var b strings.Builder
	b.WriteByte('^')
	for _, c := range glob {
		switch c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		case '.', '(', ')', '+', '|', '^', '$', '@', '%', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteRune(c)
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('$')
	return b.String()
}
