package agent

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type compactArgs struct {
	Focus string `json:"focus,omitempty" jsonschema_description:"Optional focus for what the compacted history should preserve"`
}

func buildCompactTool(controller *CompactController) (tool.BaseTool, error) {
	return utils.InferTool[*compactArgs, string]("compact", "Compact earlier conversation history so the next model call has more context budget.", func(ctx context.Context, input *compactArgs) (string, error) {
		controller.Request()
		if input != nil && input.Focus != "" {
			return "Compaction requested. Future summary should preserve: " + input.Focus, nil
		}
		return "Compaction requested.", nil
	})
}
