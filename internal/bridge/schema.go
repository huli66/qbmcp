package bridge

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type compiledTool struct {
	input, output *jsonschema.Schema
}

type denyLoader struct{}

func (denyLoader) Load(string) (any, error) {
	return nil, errors.New("schema 仅允许内部引用，不加载外部资源")
}

var toolName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,128}$`)

func compileSchema(doc map[string]any) (*jsonschema.Schema, error) {
	if doc == nil || doc["type"] != "object" {
		return nil, errors.New("schema 根类型必须是 object")
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(denyLoader{})
	if err := c.AddResource("https://qbmcp.invalid/schema", doc); err != nil {
		return nil, err
	}
	return c.Compile("https://qbmcp.invalid/schema")
}

func compileTools(defs []ToolDef) (map[string]compiledTool, error) {
	if len(defs) > 256 {
		return nil, errors.New("最多注册 256 个工具")
	}
	result := make(map[string]compiledTool, len(defs))
	for _, d := range defs {
		if !toolName.MatchString(d.Name) {
			return nil, fmt.Errorf("非法工具名: %s", d.Name)
		}
		if _, ok := result[d.Name]; ok {
			return nil, fmt.Errorf("重复工具名: %s", d.Name)
		}
		if d.Kind != "api" && d.Kind != "dom" {
			return nil, errors.New("kind 必须为 api 或 dom")
		}
		in, err := compileSchema(d.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("%s inputSchema: %w", d.Name, err)
		}
		var out *jsonschema.Schema
		if d.OutputSchema != nil {
			out, err = compileSchema(d.OutputSchema)
			if err != nil {
				return nil, fmt.Errorf("%s outputSchema: %w", d.Name, err)
			}
		}
		result[d.Name] = compiledTool{in, out}
	}
	return result, nil
}
