package service

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const cchSeed uint64 = 0x6E52736AC806831E

var billingCCHFieldRe = regexp.MustCompile(`\bcch=[^;"\\]*;`)

// signBillingHeaderCCH adds a placeholder to the Claude Code billing block,
// hashes the exact placeholder body, then replaces it with the low 20 hash bits.
// It intentionally ignores user content and bodies without a billing block.
func signBillingHeaderCCH(body []byte) []byte {
	system := gjson.GetBytes(body, "system")
	if !system.IsArray() {
		return body
	}

	result := body
	found := false
	index := 0
	system.ForEach(func(_, item gjson.Result) bool {
		text := item.Get("text")
		if text.Type != gjson.String || !strings.HasPrefix(text.String(), "x-anthropic-billing-header:") {
			index++
			return true
		}

		placeholderText := setBillingCCHValue(text.String(), "00000")
		placeholderBody, err := sjson.SetBytes(body, fmt.Sprintf("system.%d.text", index), placeholderText)
		if err != nil {
			return false
		}

		digest := xxhash.NewWithSeed(cchSeed)
		_, _ = digest.Write(placeholderBody)
		cch := fmt.Sprintf("%05x", digest.Sum64()&0xFFFFF)
		signedText := setBillingCCHValue(placeholderText, cch)
		signedBody, err := sjson.SetBytes(placeholderBody, fmt.Sprintf("system.%d.text", index), signedText)
		if err == nil {
			result = signedBody
			found = true
		}
		return false
	})

	if !found {
		return body
	}
	return result
}

func setBillingCCHValue(text, value string) string {
	field := "cch=" + value + ";"
	if billingCCHFieldRe.MatchString(text) {
		return billingCCHFieldRe.ReplaceAllString(text, field)
	}

	trimmed := strings.TrimSpace(text)
	if !strings.HasSuffix(trimmed, ";") {
		trimmed += ";"
	}
	return trimmed + " " + field
}
