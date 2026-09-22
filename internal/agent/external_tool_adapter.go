package agent

import (
	"context"
	"encoding/json"
	"errors"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tools"
)

// externalToolAdapter adapts a public agent.ExternalTool to the internal
// tools.Tool interface so host-provided tools can run inside the agent loop.
type externalToolAdapter struct {
	inner agentpkg.ExternalTool
}

// newExternalToolAdapter wraps a public ExternalTool.
func newExternalToolAdapter(t agentpkg.ExternalTool) tools.Tool {
	return &externalToolAdapter{inner: t}
}

func (a *externalToolAdapter) Name() string        { return a.inner.Name() }
func (a *externalToolAdapter) Description() string { return a.inner.Description() }

func (a *externalToolAdapter) PromptSnippet() string {
	if pi, ok := a.inner.(agentpkg.ExternalToolPromptInfo); ok {
		if s := pi.PromptSnippet(); s != "" {
			return s
		}
	}
	return a.inner.Description()
}

func (a *externalToolAdapter) PromptGuidelines() []string {
	if pi, ok := a.inner.(agentpkg.ExternalToolPromptInfo); ok {
		return pi.PromptGuidelines()
	}
	return nil
}

func (a *externalToolAdapter) Parameters() json.RawMessage {
	raw := a.inner.Parameters()
	if len(raw) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return json.RawMessage(raw)
}

func (a *externalToolAdapter) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	res, err := a.inner.Execute(ctx, params)
	if err != nil {
		return tools.ToolResult{}, err
	}
	if res.IsError {
		// The agent loop signals tool errors via a non-nil error return.
		msg := res.Text
		if msg == "" {
			msg = "tool reported an error"
		}
		return tools.ToolResult{}, errors.New(msg)
	}

	result := tools.ToolResult{Text: res.Text}
	if len(res.Contents) > 0 {
		result.Contents = contentBlocksToProvider(res.Contents)
	}
	return result, nil
}

// contentBlocksToProvider maps public content blocks to internal provider blocks.
func contentBlocksToProvider(blocks []agentpkg.ContentBlock) []provider.ContentBlock {
	out := make([]provider.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		pb := provider.ContentBlock{
			Type:      b.Type,
			Text:      b.Text,
			Thinking:  b.Thinking,
			Signature: b.Signature,
		}
		if b.Image != nil {
			pb.Image = &provider.ImageContent{
				MimeType:       b.Image.MimeType,
				Data:           b.Image.Data,
				Width:          b.Image.Width,
				Height:         b.Image.Height,
				Bytes:          b.Image.Bytes,
				OriginalWidth:  b.Image.OriginalWidth,
				OriginalHeight: b.Image.OriginalHeight,
				OriginalBytes:  b.Image.OriginalBytes,
				Detail:         b.Image.Detail,
				Scale:          b.Image.Scale,
				Cropped:        b.Image.Cropped,
				CropX:          b.Image.CropX,
				CropY:          b.Image.CropY,
				CropWidth:      b.Image.CropWidth,
				CropHeight:     b.Image.CropHeight,
			}
		}
		out = append(out, pb)
	}
	return out
}
