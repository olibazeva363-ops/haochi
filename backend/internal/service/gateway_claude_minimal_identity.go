package service

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

// ensureClaudeOAuthIdentityPrompt adds only the identity sentence required by
// Claude's OAuth inference endpoint. Caller instructions stay in their original
// order, with their text and cache metadata intact. Retries and clients already
// carrying the sentence must not accumulate another copy.
func ensureClaudeOAuthIdentityPrompt(body []byte) []byte {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return body
	}
	system := gjson.GetBytes(body, "system")
	hasIdentity := func(text string) bool {
		// OpenAI-compatible clients may join several system blocks into one
		// string. Recognize an existing identity line anywhere in that string.
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), claudeCodeSystemPrompt) {
				return true
			}
		}
		return false
	}
	var original []json.RawMessage
	switch {
	case !system.Exists() || system.Type == gjson.Null:
	case system.Type == gjson.String:
		if hasIdentity(system.String()) {
			return body
		}
		if system.String() != "" {
			original = append(original, json.RawMessage(`{"type":"text","text":`+system.Raw+`}`))
		}
	case system.IsArray():
		for _, block := range system.Array() {
			if block.Get("type").String() == "text" && hasIdentity(block.Get("text").String()) {
				return body
			}
			original = append(original, json.RawMessage(block.Raw))
		}
	default:
		// Keep malformed client input for the existing validation/error path.
		return body
	}

	identity, _ := json.Marshal(map[string]string{"type": "text", "text": claudeCodeSystemPrompt})
	// Retain each validated client block's raw representation as well as its
	// text: re-marshalling would escape HTML and compact caller-owned objects.
	raw := append([]byte{'['}, identity...)
	for _, block := range original {
		raw = append(raw, ',')
		raw = append(raw, block...)
	}
	raw = append(raw, ']')
	out, _ := setJSONRawBytes(body, "system", raw)
	return out
}
