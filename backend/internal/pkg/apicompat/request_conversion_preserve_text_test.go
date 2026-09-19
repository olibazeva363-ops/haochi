package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesToAnthropicRequest_PreserveClientInstructions(t *testing.T) {
	for _, instructions := range []string{" \n client <thinking>literal</thinking> (empty) \t", " \n\t"} {
		t.Run(instructions, func(t *testing.T) {
			req := &ResponsesRequest{Model: "claude-sonnet-4", Instructions: instructions, Input: json.RawMessage(`"hello"`)}
			preserved, err := ResponsesToAnthropicRequestWithOptions(req, RequestConversionOptions{PreserveClientText: true})
			require.NoError(t, err)
			var system string
			require.NoError(t, json.Unmarshal(preserved.System, &system))
			require.Equal(t, instructions, system)

			legacy, err := ResponsesToAnthropicRequest(req)
			require.NoError(t, err)
			if strings.TrimSpace(instructions) == "" {
				require.Empty(t, legacy.System)
			} else {
				require.NoError(t, json.Unmarshal(legacy.System, &system))
				require.Equal(t, strings.TrimSpace(instructions), system)
			}
		})
	}
}

func TestResponsesToAnthropicRequest_PreserveEmptyToolResults(t *testing.T) {
	for _, tt := range []struct {
		name   string
		output string
		want   string
	}{
		{name: "empty string", output: `""`},
		{name: "empty array", output: `[]`},
		{name: "caller placeholder", output: `"(empty)"`, want: "(empty)"},
		{name: "caller reasoning tags", output: `"<thinking>literal</thinking>"`, want: "<thinking>literal</thinking>"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := &ResponsesRequest{Model: "claude-sonnet-4", Input: json.RawMessage(`[
				{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},
				{"role":"user","content":"interleaved client text"},
				{"type":"function_call_output","call_id":"call_1","output":` + tt.output + `}
			]`)}
			preserved, err := ResponsesToAnthropicRequestWithOptions(req, RequestConversionOptions{PreserveClientText: true})
			require.NoError(t, err)
			require.Len(t, preserved.Messages, 2)
			blocks := parseContentBlocks(preserved.Messages[1].Content)
			require.Len(t, blocks, 2)
			require.Equal(t, "tool_result", blocks[0].Type)
			var output string
			require.NoError(t, json.Unmarshal(blocks[0].Content, &output))
			require.Equal(t, tt.want, output)
			require.Equal(t, "interleaved client text", blocks[1].Text)

			legacy, err := ResponsesToAnthropicRequest(req)
			require.NoError(t, err)
			legacyBlocks := parseContentBlocks(legacy.Messages[1].Content)
			require.NoError(t, json.Unmarshal(legacyBlocks[0].Content, &output))
			wantLegacy := tt.want
			if wantLegacy == "" {
				wantLegacy = "(empty)"
			}
			require.Equal(t, wantLegacy, output)
		})
	}
}

func TestChatCompletionsToResponses_PreserveReasoningAndEmptyOutputs(t *testing.T) {
	var req ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"claude-sonnet-4",
		"messages":[
			{"role":"assistant","reasoning_content":" first ","content":[
				{"type":"thinking","thinking":"second"},
				{"type":"reasoning","text":"third"},
				{"type":"text","text":"<thinking>client literal</thinking> (empty)"}
			]},
			{"role":"tool","tool_call_id":"empty_tool","content":""},
			{"role":"function","name":"empty_function","content":[]},
			{"role":"tool","tool_call_id":"literal_tool","content":"(empty)"}
		]
	}`), &req))

	preserved, err := ChatCompletionsToResponsesWithOptions(&req, RequestConversionOptions{PreserveClientText: true})
	require.NoError(t, err)
	var items []ResponsesInputItem
	require.NoError(t, json.Unmarshal(preserved.Input, &items))
	require.Len(t, items, 4)
	require.Equal(t, " first \nsecondthird<thinking>client literal</thinking> (empty)", gjson.GetBytes(items[0].Content, "0.text").String())
	require.Empty(t, items[1].Output)
	require.Empty(t, items[2].Output)
	require.Equal(t, "(empty)", items[3].Output)

	legacy, err := ChatCompletionsToResponses(&req)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(legacy.Input, &items))
	require.Equal(t, "<thinking> first </thinking>\n<thinking>second</thinking><thinking>third</thinking><thinking>client literal</thinking> (empty)", gjson.GetBytes(items[0].Content, "0.text").String())
	require.Equal(t, "(empty)", items[1].Output)
	require.Equal(t, "(empty)", items[2].Output)
}

func TestAdaptResponsesClientTools_PreserveClientDescriptions(t *testing.T) {
	const clientDescription = "Search and load Codex tools, plugins, connectors, and MCP namespaces for the current task."
	for _, supplied := range []bool{false, true} {
		name := "no client search description"
		if supplied {
			name = "client search description matches old default"
		}
		t.Run(name, func(t *testing.T) {
			search := map[string]any{"type": "tool_search"}
			if supplied {
				search["description"] = clientDescription
			}
			req := map[string]any{"tools": []any{
				map[string]any{"type": "custom", "name": "exec", "description": "The raw input for this tool, passed through verbatim."},
				search,
				map[string]any{"type": "function", "name": "lookup", "description": "<thinking>client</thinking>", "parameters": map[string]any{
					"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": "Search query for tools or connectors to load."}},
				}},
			}}
			mapping, changed, err := AdaptResponsesClientToolsWithOptions(req, RequestConversionOptions{PreserveClientText: true})
			require.NoError(t, err)
			require.True(t, changed)
			require.True(t, mapping.CustomTools["exec"])
			require.True(t, mapping.ToolSearch)
			encoded, err := json.Marshal(req)
			require.NoError(t, err)
			require.Equal(t, "The raw input for this tool, passed through verbatim.", gjson.GetBytes(encoded, "tools.0.description").String())
			require.JSONEq(t, `{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`, gjson.GetBytes(encoded, "tools.0.parameters").Raw)
			require.JSONEq(t, `{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}},"required":["query"]}`, gjson.GetBytes(encoded, "tools.1.parameters").Raw)
			require.Equal(t, supplied, gjson.GetBytes(encoded, "tools.1.description").Exists())
			if supplied {
				require.Equal(t, clientDescription, gjson.GetBytes(encoded, "tools.1.description").String())
			}
			require.Equal(t, "<thinking>client</thinking>", gjson.GetBytes(encoded, "tools.2.description").String())
			require.Equal(t, "Search query for tools or connectors to load.", gjson.GetBytes(encoded, "tools.2.parameters.properties.query.description").String())
		})
	}

	legacy := map[string]any{"tools": []any{map[string]any{"type": "custom", "name": "exec"}, map[string]any{"type": "tool_search"}}}
	_, _, err := AdaptResponsesClientTools(legacy)
	require.NoError(t, err)
	encoded, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.JSONEq(t, customToolInputSchema, gjson.GetBytes(encoded, "tools.0.parameters").Raw)
	require.JSONEq(t, toolSearchProxySchema, gjson.GetBytes(encoded, "tools.1.parameters").Raw)
	require.Equal(t, clientDescription, gjson.GetBytes(encoded, "tools.1.description").String())
}

func TestAdaptResponsesClientTools_PreserveHistoryAndDiscoveredDescriptions(t *testing.T) {
	var req map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
		"tools":[{"type":"custom","name":"exec"},{"type":"tool_search"}],
		"input":[
			{"type":"custom_tool_call","call_id":"call_custom","name":"exec","input":"<thinking>literal</thinking>"},
			{"type":"custom_tool_call_output","call_id":"call_custom","output":""},
			{"type":"tool_search_call","call_id":"call_search","arguments":{"query":"(empty)"}},
			{"type":"tool_search_output","call_id":"call_search","tools":[
				{"type":"namespace","name":"client","tools":[{"type":"function","name":"read","description":" caller description ","parameters":{"type":"object"}}]},
				{"type":"custom","name":"discovered_custom","description":" another caller description "}
			]}
		]
	}`), &req))
	originalInput := req["input"].([]any)
	discoveries, err := json.Marshal(originalInput[3].(map[string]any)["tools"])
	require.NoError(t, err)
	_, changed, err := AdaptResponsesClientToolsWithOptions(req, RequestConversionOptions{PreserveClientText: true})
	require.NoError(t, err)
	require.True(t, changed)
	encoded, err := json.Marshal(req)
	require.NoError(t, err)
	require.JSONEq(t, `{"input":"<thinking>literal</thinking>"}`, gjson.GetBytes(encoded, "input.0.arguments").String())
	require.Equal(t, "", gjson.GetBytes(encoded, "input.1.output").String())
	require.JSONEq(t, `{"query":"(empty)"}`, gjson.GetBytes(encoded, "input.2.arguments").String())
	require.JSONEq(t, string(discoveries), gjson.GetBytes(encoded, "input.3.output").String())
	require.Equal(t, "client__read", gjson.GetBytes(encoded, "tools.2.name").String())
	require.Equal(t, " caller description ", gjson.GetBytes(encoded, "tools.2.description").String())
	require.Equal(t, " another caller description ", gjson.GetBytes(encoded, "tools.3.description").String())
	require.False(t, gjson.GetBytes(encoded, "tools.3.parameters.properties.input.description").Exists())
}
