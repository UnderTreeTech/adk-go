package safetoolset

import (
	"github.com/UnderTreeTech/waterdrop/pkg/log"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
)

// SafeToolset 是一个包装器，用于捕获 Toolset.Tools() 的错误
// 如果底层 toolset 在加载工具时失败，它会记录警告但不会中断流程
type SafeToolset struct {
	underlying tool.Toolset
	name       string
}

// New 创建一个新的 SafeToolset 包装器
func New(underlying tool.Toolset) tool.Toolset {
	return &SafeToolset{
		underlying: underlying,
		name:       underlying.Name(),
	}
}

// Name 返回 toolset 的名称
func (s *SafeToolset) Name() string {
	return s.name
}

// Tools 尝试获取工具列表，如果失败则返回空列表而不是错误
func (s *SafeToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	tools, err := s.underlying.Tools(ctx)
	if err != nil {
		log.Warn(ctx, "failed to load tools from toolset, returning empty list",
			log.String("toolset_name", s.name),
			log.String("error", err.Error()))

		// 返回空列表而不是错误，允许流程继续
		return nil, nil
	}
	return tools, nil
}
