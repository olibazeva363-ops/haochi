package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageCacheReadPrice_CustomPricingUsesConfiguredCacheRate(t *testing.T) {
	const model = "gpt-image-2.5-flare"
	for _, cachePrice := range []float64{9e-6, 0} {
		for _, path := range []string{"channel flat", "channel interval", "group", "legacy channel"} {
			t.Run(fmt.Sprintf("%s/%g", path, cachePrice), func(t *testing.T) {
				catalog := &LiteLLMModelPricing{CacheReadInputTokenCost: 1.25e-6, CacheReadInputImageTokenCost: 2e-6}
				billing := &BillingService{pricingService: &PricingService{pricingData: map[string]*LiteLLMModelPricing{model: catalog}}}
				resolver := NewModelPricingResolver(nil, billing)
				config := &ChannelModelPricing{Models: []string{model}, BillingMode: BillingModeToken, CacheReadPrice: &cachePrice}
				var pricing *ModelPricing
				switch path {
				case "legacy channel":
					var err error
					pricing, err = billing.GetModelPricingWithChannel(model, config)
					require.NoError(t, err)
				case "group":
					pricing = resolver.Resolve(context.Background(), PricingInput{
						Model: model, Group: &Group{ModelPricing: []ChannelModelPricing{*config}},
					}).BasePricing
				default:
					if path == "channel interval" {
						baseCachePrice := 3e-6
						config.CacheReadPrice = &baseCachePrice
						config.Intervals = []PricingInterval{{MinTokens: 0, CacheReadPrice: &cachePrice}}
					}
					resolved := resolver.resolveConfiguredPricing(config, model, PricingSourceChannel)
					pricing = resolver.GetIntervalPricing(resolved, 50)
				}
				cost := billing.computeTokenBreakdown(pricing, UsageTokens{
					CacheReadTokens: 50, ImageCacheReadTokens: 40,
				}, 0.5, "", false)
				require.InDelta(t, 50*cachePrice, cost.CacheReadCost, 1e-12,
					"an operator cache price must apply to both text and image cache tokens")
				require.InDelta(t, 25*cachePrice, cost.ActualCost, 1e-12)
				require.Equal(t, 2e-6, catalog.CacheReadInputImageTokenCost, "custom pricing must not mutate the shared catalogue")
				require.Equal(t, 1.25e-6, catalog.CacheReadInputTokenCost)
			})
		}
	}
}

func TestOpenAIRecordUsage_ImageCacheCostsAndMetadata(t *testing.T) {
	const model = "gpt-image-2.5-flare"
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("custom=%t", configured), func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
			svc.billingService = &BillingService{pricingService: &PricingService{pricingData: map[string]*LiteLLMModelPricing{model: {
				InputCostPerToken: 5e-6, InputCostPerImageToken: 8e-6,
				CacheReadInputTokenCost: 1.25e-6, CacheReadInputImageTokenCost: 2e-6,
				OutputCostPerImageToken: 30e-6,
			}}}}
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			group := &Group{ID: 318, Platform: PlatformOpenAI, RateMultiplier: 0.5}
			inputPrice, imageInputPrice, imageOutputPrice, cacheReadPrice := 5e-6, 8e-6, 30e-6, 9e-6
			if configured {
				group.ModelPricing = []ChannelModelPricing{{
					Models: []string{model}, BillingMode: BillingModeToken,
					InputPrice: &inputPrice, ImageInputPrice: &imageInputPrice,
					ImageOutputPrice: &imageOutputPrice, CacheReadPrice: &cacheReadPrice,
				}}
			}
			usage, ok := codexDirectImagesUsage([]byte(`{"usage":{"input_tokens":100,"input_tokens_details":{"image_tokens":80,"cached_tokens":50,"cached_tokens_details":{"image_tokens":40,"text_tokens":10}},"output_tokens":200}}`))
			require.True(t, ok)
			originalBreakdown := map[string]int{ImageBillingSize1K: 1}
			result := &OpenAIForwardResult{
				RequestID: "image_cache_accounting", Model: model, UpstreamModel: model,
				Usage: usage, ImageSizeBreakdown: originalBreakdown, Duration: time.Second,
			}
			if configured {
				// Configured token pricing must remain token-based after a real image is counted.
				result.ImageCount = 1
				result.ImageOutputSizes = []string{"1024x1024"}
			}
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: result,
				APIKey: &APIKey{ID: 319, GroupID: &group.ID, Group: group},
				User:   &User{ID: 320}, Account: &Account{ID: 321, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
				ChannelUsageFields: ChannelUsageFields{OriginalModel: "public-image"},
			})
			require.NoError(t, err)
			require.Equal(t, 1, usageRepo.calls)
			log := usageRepo.lastLog
			require.NotNil(t, log)
			require.Equal(t, "public-image", log.RequestedModel)
			require.Equal(t, 50, log.InputTokens)
			require.Equal(t, 80, log.ImageInputTokens, "persist the upstream image-input total, including its cache subset")
			require.Equal(t, 50, log.CacheReadTokens)
			require.InDelta(t, 10*5e-6, log.InputCost, 1e-12)
			require.InDelta(t, 40*8e-6, log.ImageInputCost, 1e-12)
			require.Zero(t, log.OutputCost)
			require.InDelta(t, 200*30e-6, log.ImageOutputCost, 1e-12)
			wantCacheCost := 10*1.25e-6 + 40*2e-6
			if configured {
				wantCacheCost = 50 * cacheReadPrice
			}
			require.InDelta(t, wantCacheCost, log.CacheReadCost, 1e-12)
			wantTotal := 10*5e-6 + 40*8e-6 + 200*30e-6 + wantCacheCost
			require.InDelta(t, wantTotal, log.TotalCost, 1e-12)
			require.InDelta(t, wantTotal*0.5, log.ActualCost, 1e-12)
			require.Equal(t, 1, userRepo.deductCalls)
			require.InDelta(t, wantTotal*0.5, userRepo.lastAmount, 1e-12)
			require.Equal(t, map[string]int{ImageBillingSize1K: 1, "image_cache_read_tokens": 40}, log.ImageSizeBreakdown)
			require.Equal(t, map[string]int{ImageBillingSize1K: 1}, originalBreakdown)
			require.NotContains(t, result.ImageSizeBreakdown, "image_cache_read_tokens")
			log.ImageSizeBreakdown[ImageBillingSize1K] = 7
			require.Equal(t, 1, result.ImageSizeBreakdown[ImageBillingSize1K], "persisted metadata must not alias the forward result")
		})
	}
}
