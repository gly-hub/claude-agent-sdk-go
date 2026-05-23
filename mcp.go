package claudeagentsdk

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type MCPToolAnnotations struct {
	Title              string `json:"title,omitempty"`
	ReadOnly           bool   `json:"readOnly,omitempty"`
	Destructive        bool   `json:"destructive,omitempty"`
	OpenWorld          bool   `json:"openWorld,omitempty"`
	MaxResultSizeChars int    `json:"-"`
}

type MCPToolContent struct {
	Type        string         `json:"type"`
	Text        string         `json:"text,omitempty"`
	Data        string         `json:"data,omitempty"`
	MIMEType    string         `json:"mimeType,omitempty"`
	Name        string         `json:"name,omitempty"`
	URI         string         `json:"uri,omitempty"`
	Description string         `json:"description,omitempty"`
	Resource    map[string]any `json:"resource,omitempty"`
}

type MCPToolResult struct {
	Content []MCPToolContent `json:"content"`
	IsError bool             `json:"is_error,omitempty"`
}

type MCPToolHandler func(args map[string]any) (MCPToolResult, error)

type MCPTool struct {
	Name        string
	Description string
	InputSchema any
	Handler     MCPToolHandler
	Annotations *MCPToolAnnotations
}

type SDKMCPServer struct {
	Name               string
	Version            string
	tools              map[string]MCPTool
	initialized        bool
	negotiatedProtocol string
}

func NewMCPTool(name string, description string, inputSchema any, handler MCPToolHandler) MCPTool {
	return MCPTool{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
		Handler:     handler,
	}
}

func CreateSDKMCPServer(name string, version string, tools []MCPTool) SDKMCPServerConfig {
	if version == "" {
		version = "1.0.0"
	}
	server := &SDKMCPServer{
		Name:    name,
		Version: version,
		tools:   map[string]MCPTool{},
	}
	for _, tool := range tools {
		server.tools[tool.Name] = tool
	}
	return SDKMCPServerConfig{
		Type:     "sdk",
		Name:     name,
		Instance: server,
	}
}

func (s *SDKMCPServer) HandleMessage(message map[string]any) map[string]any {
	method := stringFromAny(message["method"])
	id := message["id"]
	params, _ := message["params"].(map[string]any)

	switch method {
	case "initialize":
		protocolVersion := "2024-11-05"
		if params != nil {
			if requested := stringFromAny(params["protocolVersion"]); requested != "" {
				protocolVersion = requested
			}
		}
		s.initialized = true
		s.negotiatedProtocol = protocolVersion
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result": map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities": map[string]any{
					"tools": map[string]any{
						"listChanged": false,
					},
				},
				"serverInfo": map[string]any{
					"name":    s.Name,
					"version": s.Version,
				},
			},
		}
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.tools))
		for _, tool := range s.tools {
			item := map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"inputSchema": buildMCPToolSchema(tool),
			}
			if tool.Annotations != nil {
				item["annotations"] = map[string]any{
					"title":       tool.Annotations.Title,
					"readOnly":    tool.Annotations.ReadOnly,
					"destructive": tool.Annotations.Destructive,
					"openWorld":   tool.Annotations.OpenWorld,
				}
				if meta := buildMCPToolMeta(tool); meta != nil {
					item["_meta"] = meta
				}
			}
			tools = append(tools, item)
		}
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  map[string]any{"tools": tools},
		}
	case "tools/call":
		name := stringFromAny(params["name"])
		arguments, _ := params["arguments"].(map[string]any)
		tool, ok := s.tools[name]
		if !ok {
			return mcpMethodNotFound(id, fmt.Sprintf("Tool '%s' not found", name))
		}
		result, err := tool.Handler(arguments)
		if err != nil {
			return map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error": map[string]any{
					"code":    -32603,
					"message": err.Error(),
				},
			}
		}
		content := make([]map[string]any, 0, len(result.Content))
		for _, item := range result.Content {
			switch item.Type {
			case "text":
				content = append(content, map[string]any{"type": "text", "text": item.Text})
			case "image":
				content = append(content, map[string]any{"type": "image", "data": item.Data, "mimeType": item.MIMEType})
			case "resource_link":
				text := item.Name
				if item.URI != "" {
					if text != "" {
						text += "\n"
					}
					text += item.URI
				}
				if item.Description != "" {
					if text != "" {
						text += "\n"
					}
					text += item.Description
				}
				if text == "" {
					text = "Resource link"
				}
				content = append(content, map[string]any{"type": "text", "text": text})
			case "resource":
				if item.Resource != nil {
					if text, _ := item.Resource["text"].(string); text != "" {
						content = append(content, map[string]any{"type": "text", "text": text})
					}
				}
			}
		}
		response := map[string]any{
			"content": content,
		}
		if result.IsError {
			response["isError"] = true
		}
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  response,
		}
	case "notifications/initialized":
		return nil
	default:
		return mcpMethodNotFound(id, fmt.Sprintf("Method '%s' not found", method))
	}
}

func mcpMethodNotFound(id any, message string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32601,
			"message": message,
		},
	}
}

func buildMCPToolMeta(tool MCPTool) map[string]any {
	if tool.Annotations == nil || tool.Annotations.MaxResultSizeChars == 0 {
		return nil
	}
	return map[string]any{
		"anthropic/maxResultSizeChars": tool.Annotations.MaxResultSizeChars,
	}
}

func buildMCPToolSchema(tool MCPTool) map[string]any {
	switch schema := tool.InputSchema.(type) {
	case map[string]any:
		if schema["type"] != nil && schema["properties"] != nil {
			return schema
		}
		properties := map[string]any{}
		required := make([]string, 0, len(schema))
		for key, value := range schema {
			properties[key] = goValueToJSONSchema(value)
			required = append(required, key)
		}
		return map[string]any{
			"type":       "object",
			"properties": properties,
			"required":   required,
		}
	case map[string]string:
		properties := map[string]any{}
		required := make([]string, 0, len(schema))
		for key, value := range schema {
			properties[key] = map[string]any{"type": value}
			required = append(required, key)
		}
		return map[string]any{"type": "object", "properties": properties, "required": required}
	default:
		if schema == nil {
			return map[string]any{"type": "object", "properties": map[string]any{}}
		}
		return reflectTypeToJSONSchema(reflect.TypeOf(schema))
	}
}

func goValueToJSONSchema(v any) map[string]any {
	switch val := v.(type) {
	case string:
		switch val {
		case "string", "number", "integer", "boolean", "object", "array":
			return map[string]any{"type": val}
		default:
			return map[string]any{"type": "string"}
		}
	default:
		if v == nil {
			return map[string]any{"type": "string"}
		}
		return reflectTypeToJSONSchema(reflect.TypeOf(v))
	}
}

func reflectTypeToJSONSchema(t reflect.Type) map[string]any {
	if t == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": reflectTypeToJSONSchema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object"}
	case reflect.Struct:
		properties := map[string]any{}
		required := make([]string, 0)
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name := field.Tag.Get("json")
			if name == "" {
				name = field.Name
			} else {
				name = strings.Split(name, ",")[0]
			}
			if name == "-" || name == "" {
				continue
			}
			properties[name] = reflectTypeToJSONSchema(field.Type)
			required = append(required, name)
		}
		result := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			result["required"] = required
		}
		return result
	default:
		return map[string]any{"type": "string"}
	}
}

func marshalMCPServerConfig(config MCPServerConfig) (map[string]any, error) {
	switch cfg := config.(type) {
	case SDKMCPServerConfig:
		return map[string]any{
			"type": "sdk",
			"name": cfg.Name,
		}, nil
	case MCPStdioServerConfig:
		raw, err := json.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	case MCPHTTPServerConfig:
		raw, err := json.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported MCP server config type %T", config)
	}
}
