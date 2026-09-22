package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeMinimalIdentityPreservesCallerFieldsAndIsIdempotent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		system string
		blocks int
	}{
		{name: "missing", blocks: 1},
		{name: "null", system: `null`, blocks: 1},
		{name: "empty string", system: `""`, blocks: 1},
		{name: "empty array", system: `[]`, blocks: 1},
		{name: "string", system: `" \n客户原文\t Today’s date is 2026/09/22. "`, blocks: 2},
		{name: "blocks", system: `[{"type":"text","text":" x-anthropic-billing-header: cc_version=client; cch=abcde; ","cache_control":{"type":"ephemeral","ttl":"1h"}},{ "type":"text", "text":"第二块\n<client>&</client>", "client_number":9007199254740993 }]`, blocks: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"Leave this unchanged."}],"tools":[{"name":"probe","description":"Client tool description."}],"metadata":{"user_id":"client"}}`)
			if tc.system != "" {
				var ok bool
				body, ok = setJSONRawBytes(body, "system", []byte(tc.system))
				require.True(t, ok)
			}
			before := append([]byte(nil), body...)
			out := ensureClaudeOAuthIdentityPrompt(body)
			require.Equal(t, before, body, "shared request buffers must not be mutated")
			blocks := gjson.GetBytes(out, "system").Array()
			require.Len(t, blocks, tc.blocks)
			require.JSONEq(t, `{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."}`, blocks[0].Raw)
			for _, field := range []string{"model", "messages", "tools", "metadata"} {
				require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(out, field).Raw)
			}
			original := gjson.GetBytes(body, "system")
			if original.IsArray() {
				for i, block := range original.Array() {
					var expected, actual json.RawMessage
					require.NoError(t, json.Unmarshal([]byte(block.Raw), &expected))
					require.NoError(t, json.Unmarshal([]byte(blocks[i+1].Raw), &actual))
					require.Equal(t, expected, actual)
				}
			} else if original.Type == gjson.String && original.String() != "" {
				require.Equal(t, original.String(), blocks[1].Get("text").String())
			}
			require.Equal(t, out, ensureClaudeOAuthIdentityPrompt(out), "rebuilding a retry must not add another identity")
		})
	}
}

func TestClaudeMinimalIdentityLeavesExistingIdentityAndInvalidSystemUntouched(t *testing.T) {
	for _, system := range []string{
		`"You are Claude Code, Anthropic's official CLI for Claude.\nClient instructions."`,
		`"Client billing line.\n\n You are Claude Code, Anthropic's official CLI for Claude.\nClient instructions."`,
		`[{"type":"text","text":"Client billing"},{"type":"text","text":" \nYou are Claude Code, Anthropic's official CLI for Claude.","cache_control":{"type":"ephemeral"}}]`,
		`42`, `true`, `{"text":"invalid system shape"}`,
	} {
		body := []byte(`{"system":` + system + `,"messages":[{"role":"user","content":"Hello"}]}`)
		require.Equal(t, body, ensureClaudeOAuthIdentityPrompt(body))
	}
	for _, body := range []string{``, `not json`, `[]`, `null`, `{"system":`} {
		require.Equal(t, []byte(body), ensureClaudeOAuthIdentityPrompt([]byte(body)))
	}
}
