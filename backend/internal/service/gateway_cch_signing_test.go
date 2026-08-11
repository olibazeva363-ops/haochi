//go:build unit

package service

import (
	"fmt"
	"testing"

	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestSignBillingHeaderCCHMatchesReferenceAlgorithm(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.91.abc; cc_entrypoint=cli;"},{"type":"text","text":"You are Claude Code."}],"messages":[{"role":"user","content":"hello"}]}`)

	signed := signBillingHeaderCCH(body)
	billingText := gjson.GetBytes(signed, "system.0.text").String()
	require.Regexp(t, `cch=[0-9a-f]{5};`, billingText)

	placeholderText := setBillingCCHValue(billingText, "00000")
	placeholderBody, err := sjson.SetBytes(signed, "system.0.text", placeholderText)
	require.NoError(t, err)
	digest := xxhash.NewWithSeed(cchSeed)
	_, _ = digest.Write(placeholderBody)
	want := fmt.Sprintf("%05x", digest.Sum64()&0xFFFFF)
	require.Contains(t, billingText, "cch="+want+";")
}

func TestSignBillingHeaderCCHReplacesExistingValueAndPreservesUserContent(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.91.abc; cc_entrypoint=cli; cch=abcde;"}],"messages":[{"role":"user","content":"keep cch=00000; here"}]}`)

	signed := signBillingHeaderCCH(body)

	require.NotContains(t, gjson.GetBytes(signed, "system.0.text").String(), "cch=abcde;")
	require.Equal(t, "keep cch=00000; here", gjson.GetBytes(signed, "messages.0.content").String())
}

func TestSignBillingHeaderCCHLeavesUnrelatedBodiesUnchanged(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"You are helpful."}],"messages":[]}`)
	require.Equal(t, body, signBillingHeaderCCH(body))
}
