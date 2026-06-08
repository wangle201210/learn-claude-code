package modelenv

import (
	"fmt"
	"os"
	"strings"
)

var required = []string{
	"OPENAI_API_KEY",
	"OPENAI_MODEL",
	"OPENAI_BASE_URL",
}

// Require reports missing model env vars before the first model request blocks or fails.
func Require() error {
	var missing []string
	for _, key := range required {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("模型环境变量未配置：%s\n请先设置：\n  export OPENAI_API_KEY=\"你的 API Key\"\n  export OPENAI_MODEL=\"你的模型名\"\n  export OPENAI_BASE_URL=\"https://你的网关地址/v1\"", strings.Join(missing, ", "))
}
