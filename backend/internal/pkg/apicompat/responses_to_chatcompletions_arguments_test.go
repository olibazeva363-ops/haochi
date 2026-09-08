package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBufferedResponseAccumulator_SupplementArgumentsMatchesCallIdentity(t *testing.T) {
	t.Parallel()

	firstArgs := `{"id":"first"}`
	secondArgs := `{"id":"second"}`
	tests := []struct {
		name      string
		streamIDs [2]string
		outputIDs []string
		wantArgs  []string
	}{
		{
			name:      "compressed output retains second call arguments",
			streamIDs: [2]string{"call_first", "call_second"},
			outputIDs: []string{"call_second"},
			wantArgs:  []string{secondArgs},
		},
		{
			name:      "reordered output follows call ids",
			streamIDs: [2]string{"call_first", "call_second"},
			outputIDs: []string{"call_second", "call_first"},
			wantArgs:  []string{secondArgs, firstArgs},
		},
		{
			name:      "unknown call id does not borrow positional arguments",
			streamIDs: [2]string{"call_first", "call_second"},
			outputIDs: []string{"call_unknown"},
			wantArgs:  []string{""},
		},
		{
			name:      "missing terminal call id retains positional fallback",
			streamIDs: [2]string{"call_first", "call_second"},
			outputIDs: []string{""},
			wantArgs:  []string{firstArgs},
		},
		{
			name:      "matching id wins over earlier fallback without id",
			streamIDs: [2]string{"", "call_second"},
			outputIDs: []string{"call_second"},
			wantArgs:  []string{secondArgs},
		},
		{
			name:      "missing stream call id retains positional fallback",
			streamIDs: [2]string{"", "call_second"},
			outputIDs: []string{"call_first"},
			wantArgs:  []string{firstArgs},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			acc := NewBufferedResponseAccumulator()
			for index, callID := range tt.streamIDs {
				acc.ProcessEvent(&ResponsesStreamEvent{
					Type:        "response.output_item.added",
					OutputIndex: index,
					Item:        &ResponsesOutput{Type: "function_call", CallID: callID, Name: "lookup"},
				})
				acc.ProcessEvent(&ResponsesStreamEvent{
					Type:        "response.function_call_arguments.done",
					OutputIndex: index,
					Arguments:   [2]string{firstArgs, secondArgs}[index],
				})
			}
			resp := &ResponsesResponse{}
			for _, callID := range tt.outputIDs {
				resp.Output = append(resp.Output, ResponsesOutput{Type: "function_call", CallID: callID, Name: "lookup"})
			}

			acc.SupplementResponseOutput(resp)

			require.Len(t, resp.Output, len(tt.wantArgs))
			for index, args := range tt.wantArgs {
				require.Equal(t, args, resp.Output[index].Arguments, "output %d", index)
				require.Equal(t, tt.outputIDs[index], resp.Output[index].CallID)
			}
		})
	}
}
