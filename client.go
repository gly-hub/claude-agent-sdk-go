package claudeagentsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type Client struct {
	opts            Options
	transport       Transport
	msgCh           chan Message
	errCh           chan error
	done            chan struct{}
	pendingMu       sync.Mutex
	pending         map[string]chan controlResponse
	hooksMu         sync.Mutex
	hookCallbacks   map[string]HookCallback
	nextHookID      uint64
	requestCounter  uint64
	lastResultError atomic.Value
	materialized    *MaterializedResume
	mirrorBatcher   *TranscriptMirrorBatcher
	serverInfo      map[string]any
}

type controlResponse struct {
	Response map[string]any
	Err      error
}

func NewClient(opts Options) *Client {
	return NewClientWithTransport(opts, NewSubprocessTransport(opts))
}

func NewClientWithTransport(opts Options, transport Transport) *Client {
	return &Client{
		opts:          opts,
		transport:     transport,
		msgCh:         make(chan Message, 128),
		errCh:         make(chan error, 1),
		done:          make(chan struct{}),
		pending:       map[string]chan controlResponse{},
		hookCallbacks: map[string]HookCallback{},
	}
}

func (c *Client) Connect(ctx context.Context) error {
	if err := validateSessionStoreOptions(c.opts); err != nil {
		return err
	}
	if err := validatePermissionPromptOptions(c.opts); err != nil {
		return err
	}
	if c.opts.LoadTimeoutMS == 0 {
		c.opts.LoadTimeoutMS = 60000
	}
	if c.opts.SessionStoreFlush == "" {
		c.opts.SessionStoreFlush = SessionStoreFlushBatched
	}
	if c.materialized == nil && c.opts.SessionStore != nil && (c.opts.Resume != "" || c.opts.ContinueConversation) {
		materialized, err := MaterializeResumeSession(c.opts)
		if err != nil {
			return err
		}
		c.materialized = materialized
		if materialized != nil {
			if c.opts.Env == nil {
				c.opts.Env = map[string]string{}
			}
			c.opts.Env["CLAUDE_CONFIG_DIR"] = materialized.ConfigDir
			c.opts.Resume = materialized.ResumeSessionID
			c.opts.ContinueConversation = false
			if subprocess, ok := c.transport.(*SubprocessTransport); ok {
				subprocess.opts = c.opts
			}
		}
	}
	if err := c.transport.Connect(ctx); err != nil {
		return err
	}
	if c.opts.SessionStore != nil {
		projectsDir := getProjectsDir()
		if c.materialized != nil {
			projectsDir = filepath.Join(c.materialized.ConfigDir, "projects")
		}
		c.mirrorBatcher = NewTranscriptMirrorBatcher(c.opts.SessionStore, projectsDir, c.opts.SessionStoreFlush)
	}
	go c.readLoop()
	request := map[string]any{
		"subtype": "initialize",
	}
	if hooks := c.buildHooksConfig(); len(hooks) > 0 {
		request["hooks"] = hooks
	}
	if c.opts.SystemPromptPreset != nil && c.opts.SystemPromptPreset.ExcludeDynamicSections {
		request["excludeDynamicSections"] = true
	}
	if len(c.opts.Agents) > 0 {
		request["agents"] = c.opts.Agents
	}
	if skills := c.initializeSkills(); skills != nil {
		request["skills"] = skills
	}
	response, err := c.sendControlRequest(ctx, request, 60*time.Second)
	if err != nil {
		return err
	}
	c.serverInfo = response
	return nil
}

func (c *Client) SendUser(ctx context.Context, prompt string, sessionID string) error {
	if !c.transport.IsReady() {
		return ErrNotConnected
	}
	if sessionID == "" {
		sessionID = "default"
	}
	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": prompt,
		},
		"parent_tool_use_id": nil,
		"session_id":         sessionID,
	}
	return c.writeJSON(ctx, payload)
}

func (c *Client) Send(ctx context.Context, message map[string]any) error {
	if !c.transport.IsReady() {
		return ErrNotConnected
	}
	return c.writeJSON(ctx, message)
}

func (c *Client) EndInput() error {
	return c.transport.EndInput()
}

func (c *Client) Next(ctx context.Context) (Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-c.msgCh:
		if !ok {
			select {
			case err := <-c.errCh:
				if err != nil {
					return nil, err
				}
			default:
			}
			return nil, io.EOF
		}
		return msg, nil
	}
}

func (c *Client) Interrupt(ctx context.Context) error {
	_, err := c.sendControlRequest(ctx, map[string]any{"subtype": "interrupt"}, 30*time.Second)
	return err
}

func (c *Client) SetPermissionMode(ctx context.Context, mode PermissionMode) error {
	_, err := c.sendControlRequest(ctx, map[string]any{
		"subtype": "set_permission_mode",
		"mode":    mode,
	}, 30*time.Second)
	return err
}

func (c *Client) SetModel(ctx context.Context, model string) error {
	_, err := c.sendControlRequest(ctx, map[string]any{
		"subtype": "set_model",
		"model":   model,
	}, 30*time.Second)
	return err
}

func (c *Client) RewindFiles(ctx context.Context, userMessageID string) error {
	_, err := c.sendControlRequest(ctx, map[string]any{
		"subtype":         "rewind_files",
		"user_message_id": userMessageID,
	}, 30*time.Second)
	return err
}

func (c *Client) ReconnectMCPServer(ctx context.Context, serverName string) error {
	_, err := c.sendControlRequest(ctx, map[string]any{
		"subtype":    "mcp_reconnect",
		"serverName": serverName,
	}, 30*time.Second)
	return err
}

func (c *Client) ToggleMCPServer(ctx context.Context, serverName string, enabled bool) error {
	_, err := c.sendControlRequest(ctx, map[string]any{
		"subtype":    "mcp_toggle",
		"serverName": serverName,
		"enabled":    enabled,
	}, 30*time.Second)
	return err
}

func (c *Client) StopTask(ctx context.Context, taskID string) error {
	_, err := c.sendControlRequest(ctx, map[string]any{
		"subtype": "stop_task",
		"taskId":  taskID,
	}, 30*time.Second)
	return err
}

func (c *Client) GetMCPStatus(ctx context.Context) (map[string]any, error) {
	return c.sendControlRequest(ctx, map[string]any{"subtype": "mcp_status"}, 30*time.Second)
}

func (c *Client) GetMCPStatusResponse(ctx context.Context) (*MCPStatusResponse, error) {
	raw, err := c.GetMCPStatus(ctx)
	if err != nil {
		return nil, err
	}
	var response MCPStatusResponse
	if err := decodeMap(raw, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) GetContextUsage(ctx context.Context) (map[string]any, error) {
	return c.sendControlRequest(ctx, map[string]any{"subtype": "get_context_usage"}, 30*time.Second)
}

func (c *Client) GetContextUsageResponse(ctx context.Context) (*ContextUsageResponse, error) {
	raw, err := c.GetContextUsage(ctx)
	if err != nil {
		return nil, err
	}
	var response ContextUsageResponse
	if err := decodeMap(raw, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) GetServerInfo() map[string]any {
	if c.serverInfo == nil {
		return nil
	}
	out := make(map[string]any, len(c.serverInfo))
	for k, v := range c.serverInfo {
		out[k] = v
	}
	return out
}

func (c *Client) ReceiveResponse(ctx context.Context) ([]Message, error) {
	messages := []Message{}
	for {
		msg, err := c.Next(ctx)
		if err != nil {
			return messages, err
		}
		messages = append(messages, msg)
		if _, ok := msg.(*ResultMessage); ok {
			return messages, nil
		}
	}
}

func (c *Client) ReceiveResponseStream(ctx context.Context) <-chan Message {
	out := make(chan Message)
	go func() {
		defer close(out)
		for {
			msg, err := c.Next(ctx)
			if err != nil {
				return
			}
			out <- msg
			if _, ok := msg.(*ResultMessage); ok {
				return
			}
		}
	}()
	return out
}

func (c *Client) Close() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	if c.mirrorBatcher != nil {
		_ = c.mirrorBatcher.Flush()
	}
	err := c.transport.Close()
	if c.materialized != nil {
		_ = c.materialized.Cleanup()
	}
	return err
}

func (c *Client) readLoop() {
	defer func() {
		c.failPending(fmt.Errorf("claude process stream closed before control response"))
		close(c.msgCh)
	}()
	for item := range c.transport.ReadMessages() {
		if item.Err != nil {
			err := item.Err
			if _, ok := err.(*ProcessError); ok {
				if last, _ := c.lastResultError.Load().(string); last != "" {
					err = fmt.Errorf("Claude Code returned an error result: %s", last)
				}
			}
			c.failPending(err)
			select {
			case c.errCh <- err:
			default:
			}
			return
		}

		msgType := stringFromAny(item.Data["type"])
		switch msgType {
		case "control_response":
			c.handleControlResponse(item.Data)
			continue
		case "control_request":
			go c.handleControlRequest(item.Data)
			continue
		case "transcript_mirror":
			if c.mirrorBatcher != nil {
				rawEntries, _ := item.Data["entries"].([]any)
				entries := make([]SessionStoreEntry, 0, len(rawEntries))
				for _, raw := range rawEntries {
					if entry, ok := raw.(map[string]any); ok {
						entries = append(entries, SessionStoreEntry(entry))
					}
				}
				if err := c.mirrorBatcher.Enqueue(stringFromAny(item.Data["filePath"]), entries); err != nil {
					c.failPending(err)
					select {
					case c.errCh <- err:
					default:
					}
					return
				}
			}
			continue
		}

		msg, err := ParseMessage(item.Data)
		if err != nil {
			c.failPending(err)
			select {
			case c.errCh <- err:
			default:
			}
			return
		}
		if msg == nil {
			continue
		}
		if result, ok := msg.(*ResultMessage); ok && result.IsError && result.Result != "" {
			c.lastResultError.Store(result.Result)
			if c.mirrorBatcher != nil {
				_ = c.mirrorBatcher.Flush()
			}
		} else if _, ok := msg.(*ResultMessage); ok {
			if c.mirrorBatcher != nil {
				_ = c.mirrorBatcher.Flush()
			}
		} else if msgType != "system" || stringFromAny(item.Data["subtype"]) != "session_state_changed" {
			c.lastResultError.Store("")
		}
		c.msgCh <- msg
	}
}

func (c *Client) failPending(err error) {
	if err == nil {
		return
	}
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = map[string]chan controlResponse{}
	c.pendingMu.Unlock()
	for _, waiter := range pending {
		waiter <- controlResponse{Err: err}
	}
}

func (c *Client) handleControlResponse(data map[string]any) {
	response := mapFromAny(data["response"])
	requestID := stringFromAny(response["request_id"])
	c.pendingMu.Lock()
	waiter := c.pending[requestID]
	delete(c.pending, requestID)
	c.pendingMu.Unlock()
	if waiter == nil {
		return
	}
	if stringFromAny(response["subtype"]) == "error" {
		waiter <- controlResponse{Err: fmt.Errorf("%s", stringFromAny(response["error"]))}
		return
	}
	waiter <- controlResponse{Response: mapFromAny(response["response"])}
}

func (c *Client) handleControlRequest(data map[string]any) {
	requestID := stringFromAny(data["request_id"])
	request := mapFromAny(data["request"])
	subtype := stringFromAny(request["subtype"])

	var responsePayload map[string]any
	var err error

	switch subtype {
	case "can_use_tool":
		responsePayload, err = c.handleCanUseTool(request)
	case "hook_callback":
		responsePayload, err = c.handleHookCallback(request)
	case "mcp_message":
		responsePayload, err = c.handleMCPMessage(request)
	default:
		err = fmt.Errorf("unsupported control request subtype: %s", subtype)
	}

	payload := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"request_id": requestID,
		},
	}
	if err != nil {
		payload["response"].(map[string]any)["subtype"] = "error"
		payload["response"].(map[string]any)["error"] = err.Error()
	} else {
		payload["response"].(map[string]any)["subtype"] = "success"
		payload["response"].(map[string]any)["response"] = responsePayload
	}
	_ = c.writeJSON(context.Background(), payload)
}

func (c *Client) handleCanUseTool(request map[string]any) (map[string]any, error) {
	if c.opts.CanUseTool == nil {
		return nil, fmt.Errorf("can use tool callback is not configured")
	}
	suggestions := make([]PermissionUpdate, 0)
	if raw, ok := request["permission_suggestions"].([]any); ok {
		for _, item := range raw {
			blob, _ := json.Marshal(item)
			var update PermissionUpdate
			if err := json.Unmarshal(blob, &update); err == nil {
				suggestions = append(suggestions, update)
			}
		}
	}
	req := ToolPermissionRequest{
		ToolName:              stringFromAny(request["tool_name"]),
		Input:                 mapFromAny(request["input"]),
		PermissionSuggestions: suggestions,
		ToolUseID:             stringFromAny(request["tool_use_id"]),
		AgentID:               stringFromAny(request["agent_id"]),
		BlockedPath:           stringFromAny(request["blocked_path"]),
		DecisionReason:        stringFromAny(request["decision_reason"]),
		Title:                 stringFromAny(request["title"]),
		DisplayName:           stringFromAny(request["display_name"]),
		Description:           stringFromAny(request["description"]),
	}
	decision, err := c.opts.CanUseTool(req)
	if err != nil {
		return nil, err
	}
	response := map[string]any{
		"behavior": decision.Behavior,
	}
	if decision.Behavior == "allow" {
		if decision.UpdatedInput != nil {
			response["updatedInput"] = decision.UpdatedInput
		} else {
			response["updatedInput"] = req.Input
		}
		if len(decision.UpdatedPermissions) > 0 {
			response["updatedPermissions"] = decision.UpdatedPermissions
		}
	}
	if decision.Behavior == "deny" {
		response["message"] = decision.Message
		if decision.Interrupt {
			response["interrupt"] = true
		}
	}
	return response, nil
}

func (c *Client) handleHookCallback(request map[string]any) (map[string]any, error) {
	callbackID := stringFromAny(request["callback_id"])
	c.hooksMu.Lock()
	callback := c.hookCallbacks[callbackID]
	c.hooksMu.Unlock()
	if callback == nil {
		return nil, fmt.Errorf("no hook callback found for id: %s", callbackID)
	}

	output, err := callback(
		mapFromAny(request["input"]),
		stringFromAny(request["tool_use_id"]),
		HookContext{Signal: nil},
	)
	if err != nil {
		return nil, err
	}
	return convertHookOutput(output), nil
}

func (c *Client) handleMCPMessage(request map[string]any) (map[string]any, error) {
	serverName := stringFromAny(request["server_name"])
	message, _ := request["message"].(map[string]any)
	if serverName == "" || message == nil {
		return nil, fmt.Errorf("missing server_name or message for MCP request")
	}
	c.logDebug("sdk mcp request server=%s method=%s payload=%s", serverName, stringFromAny(message["method"]), toJSONDebug(message))
	config, ok := c.opts.MCPServers[serverName]
	if !ok {
		response := map[string]any{
			"mcp_response": mcpMethodNotFound(message["id"], fmt.Sprintf("Server '%s' not found", serverName)),
		}
		c.logDebug("sdk mcp response server=%s payload=%s", serverName, toJSONDebug(response["mcp_response"]))
		return response, nil
	}

	var server *SDKMCPServer
	switch cfg := config.(type) {
	case SDKMCPServerConfig:
		server = cfg.Instance
	default:
		return nil, fmt.Errorf("MCP control requests only support SDK servers, got %T", config)
	}
	if server == nil {
		return nil, fmt.Errorf("SDK MCP server %q has no instance", serverName)
	}
	mcpResponse := server.HandleMessage(message)
	response := map[string]any{}
	if mcpResponse != nil {
		response["mcp_response"] = mcpResponse
	}
	c.logDebug("sdk mcp response server=%s payload=%s", serverName, toJSONDebug(mcpResponse))
	return response, nil
}

func (c *Client) sendControlRequest(ctx context.Context, request map[string]any, timeout time.Duration) (map[string]any, error) {
	if !c.transport.IsReady() {
		return nil, ErrNotConnected
	}
	requestID := fmt.Sprintf("req_%d", atomic.AddUint64(&c.requestCounter, 1))
	waiter := make(chan controlResponse, 1)

	c.pendingMu.Lock()
	c.pending[requestID] = waiter
	c.pendingMu.Unlock()

	payload := map[string]any{
		"type":       "control_request",
		"request_id": requestID,
		"request":    request,
	}
	if err := c.writeJSON(ctx, payload); err != nil {
		c.removePending(requestID)
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		c.removePending(requestID)
		return nil, ctx.Err()
	case <-timer.C:
		c.removePending(requestID)
		return nil, fmt.Errorf("control request timeout: %s", stringFromAny(request["subtype"]))
	case err := <-c.errCh:
		c.removePending(requestID)
		return nil, err
	case res := <-waiter:
		return res.Response, res.Err
	}
}

func (c *Client) removePending(requestID string) {
	c.pendingMu.Lock()
	delete(c.pending, requestID)
	c.pendingMu.Unlock()
}

func (c *Client) writeJSON(ctx context.Context, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return c.transport.Write(ctx, data)
}

func (c *Client) initializeSkills() any {
	switch skills := c.opts.Skills.(type) {
	case nil:
		return nil
	case string:
		if skills == "all" {
			return nil
		}
		return nil
	case []string:
		return skills
	}
	return nil
}

func (c *Client) buildHooksConfig() map[string]any {
	if len(c.opts.Hooks) == 0 {
		return nil
	}

	result := make(map[string]any, len(c.opts.Hooks))
	for event, matchers := range c.opts.Hooks {
		configs := make([]any, 0, len(matchers))
		for _, matcher := range matchers {
			callbackIDs := make([]string, 0, len(matcher.Hooks))
			for _, hook := range matcher.Hooks {
				id := c.registerHookCallback(hook)
				callbackIDs = append(callbackIDs, id)
			}
			cfg := map[string]any{
				"matcher":         nil,
				"hookCallbackIds": callbackIDs,
			}
			if matcher.Matcher != "" {
				cfg["matcher"] = matcher.Matcher
			}
			if matcher.Timeout > 0 {
				cfg["timeout"] = matcher.Timeout
			}
			configs = append(configs, cfg)
		}
		result[event] = configs
	}
	return result
}

func (c *Client) registerHookCallback(callback HookCallback) string {
	id := fmt.Sprintf("hook_%d", atomic.AddUint64(&c.nextHookID, 1))
	c.hooksMu.Lock()
	c.hookCallbacks[id] = callback
	c.hooksMu.Unlock()
	return id
}

func convertHookOutput(output HookOutput) map[string]any {
	if output == nil {
		return map[string]any{}
	}
	converted := make(map[string]any, len(output))
	for key, value := range output {
		switch key {
		case "async_":
			converted["async"] = value
		case "continue_":
			converted["continue"] = value
		default:
			converted[key] = value
		}
	}
	return converted
}

func (c *Client) logDebug(format string, args ...any) {
	if c.opts.Stderr == nil {
		return
	}
	c.opts.Stderr(fmt.Sprintf("[claude-agent-sdk-go] "+format, args...))
}

func toJSONDebug(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(data)
}

func decodeMap(src map[string]any, dst any) error {
	data, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}
