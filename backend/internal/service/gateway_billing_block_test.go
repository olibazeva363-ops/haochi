package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComputeClaudeCodeFingerprintMatchesJavaScriptUTF16Indexing(t *testing.T) {
	const version = "2.1.220"
	tests := []struct {
		name      string
		firstText string
		want      string
	}{
		{name: "ascii", firstText: "abcdefghijklmnopqrstuvwxyz", want: "ac9"},
		{name: "chinese", firstText: "你好世界这是一个中文提示词用于测试指纹", want: "a86"},
		{name: "unpaired selected surrogate", firstText: "abcd😀efghijklmnopqrstuv", want: "e25"},
		{name: "selected surrogate pair rejoins", firstText: "😀😀😀😀😀😀😀😀😀😀😀", want: "597"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.firstText)
			require.NoError(t, err)
			body := []byte(`{"messages":[{"role":"user","content":` + string(encoded) + `}]}`)
			require.Equal(t, tt.want, computeClaudeCodeFingerprint(body, version))
		})
	}
}
