package claude

import "testing"

func TestNormalizeModelID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// 空串原样返回
		{"empty", "", ""},

		// 原有短 ID → 带日期官方 ID
		{"sonnet-4-5 short", "claude-sonnet-4-5", "claude-sonnet-4-5-20250929"},
		{"opus-4-5 short", "claude-opus-4-5", "claude-opus-4-5-20251101"},
		{"haiku-4-5 short", "claude-haiku-4-5", "claude-haiku-4-5-20251001"},

		// 展示名（带点）→ 官方连字符 ID（本次修复）
		{"opus-4.8 dotted", "claude-opus-4.8", "claude-opus-4-8"},
		{"opus-4.7 dotted", "claude-opus-4.7", "claude-opus-4-7"},
		{"opus-4.6 dotted", "claude-opus-4.6", "claude-opus-4-6"},
		{"sonnet-4.6 dotted", "claude-sonnet-4.6", "claude-sonnet-4-6"},
		{"opus-4.5 dotted", "claude-opus-4.5", "claude-opus-4-5-20251101"},
		{"sonnet-4.5 dotted", "claude-sonnet-4.5", "claude-sonnet-4-5-20250929"},
		{"haiku-4.5 dotted", "claude-haiku-4.5", "claude-haiku-4-5-20251001"},

		// 合法官方 ID 原样透传（不被误改）
		{"opus-4-8 passthrough", "claude-opus-4-8", "claude-opus-4-8"},
		{"opus-4-7 passthrough", "claude-opus-4-7", "claude-opus-4-7"},
		{"sonnet-5 passthrough", "claude-sonnet-5", "claude-sonnet-5"},
		{"opus-5 passthrough", "claude-opus-5", "claude-opus-5"},
		{"fable-5 passthrough", "claude-fable-5", "claude-fable-5"},

		// 未知模型原样透传
		{"unknown passthrough", "some-unknown-model", "some-unknown-model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeModelID(tc.in); got != tc.want {
				t.Fatalf("NormalizeModelID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// 反向映射不应把带点别名当作上游 ID 处理，也不应把归一目标还原成点号名。
func TestDenormalizeModelIDUnaffectedByDottedAliases(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// 带日期官方 ID → 短名（原有行为保持）
		{"claude-sonnet-4-5-20250929", "claude-sonnet-4-5"},
		{"claude-opus-4-5-20251101", "claude-opus-4-5"},
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5"},
		// 归一目标不在反向表里 → 原样返回，绝不还原成点号名
		{"claude-opus-4-8", "claude-opus-4-8"},
		{"claude-opus-4-6", "claude-opus-4-6"},
		{"claude-sonnet-4-6", "claude-sonnet-4-6"},
	}
	for _, tc := range cases {
		if got := DenormalizeModelID(tc.in); got != tc.want {
			t.Fatalf("DenormalizeModelID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
