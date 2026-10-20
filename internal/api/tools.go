package api

import (
	"context"
	"github.com/alexgorbatchev/engine-cli/internal/acquire"
)

type toolParams struct {
	Tool string `json:"tool"`
}
type dependencyParams struct {
	Tool         string   `json:"tool"`
	Dependencies []string `json:"dependencies"`
}
type providerParams struct {
	Provider string `json:"provider"`
}

func (s *Server) toolOperations() []operation {
	client := acquire.Client{Runner: s.config.ToolRunner}
	return []operation{
		defineEffect("tool.inspect", "read", []string{"tool"}, func(ctx context.Context, p toolParams) (any, error) { return client.InspectTool(ctx, p.Tool) }),
		defineEffect("tool.upgrade", "system", []string{"tool"}, func(ctx context.Context, p toolParams) (any, error) { return client.Upgrade(ctx, p.Tool) }),
		defineEffect("tool.dependencies.inspect", "read", []string{"tool"}, func(ctx context.Context, p dependencyParams) (any, error) {
			return client.Dependencies(ctx, p.Tool, "inspect", p.Dependencies)
		}),
		defineEffect("tool.dependencies.install", "system", []string{"tool"}, func(ctx context.Context, p dependencyParams) (any, error) {
			return client.Dependencies(ctx, p.Tool, "install", p.Dependencies)
		}),
		defineEffect("tool.dependencies.update", "system", []string{"tool"}, func(ctx context.Context, p dependencyParams) (any, error) {
			return client.Dependencies(ctx, p.Tool, "update", p.Dependencies)
		}),
		defineEffect("ai.list", "read", nil, func(ctx context.Context, _ emptyParams) (any, error) { return client.Providers(ctx) }),
		defineEffect("ai.inspect", "read", []string{"provider"}, func(ctx context.Context, p providerParams) (any, error) { return client.Provider(ctx, p.Provider) }),
	}
}
