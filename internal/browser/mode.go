package browser

// Mode 是浏览器会话模式。
//   - ModeIsolated:teacli 启动一个独立浏览器,所有标签页都归 agent;
//   - ModeShared:接管用户已运行的 Chrome,需谨慎对待非 agent 创建的标签页。
type Mode int

const (
	ModeIsolated Mode = iota
	ModeShared
)

func (m Mode) String() string {
	switch m {
	case ModeShared:
		return "shared"
	default:
		return "isolated"
	}
}
