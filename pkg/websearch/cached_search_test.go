package websearch

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeCachedSearchPreservesProviderControls(t *testing.T) {
	for _, typ := range []string{"web_search", "web_search_2025_08_26"} {
		t.Run(typ, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"gpt-6.1-sol","input":"fixture","temperature":0,"metadata":{"n":9007199254740993},"tools":[{"type":%q,"external_web_access":false,"user_location":{"type":"approximate","city":"Paris"},"filters":{"allowed_domains":["example.com"]}},{"type":"function","name":"local_read","parameters":{"type":"object"}}],"tool_choice":{"type":%q}}`, typ, typ))
			plan, err := NewPlan(body, "responses")
			require.NoError(t, err)
			require.NotNil(t, plan)
			require.True(t, plan.NativeCachedSearch)
			require.False(t, plan.Search)
			require.False(t, plan.Fetch)
			original, err := Obj(body)
			require.NoError(t, err)
			require.Equal(t, original, plan.Body, "cached tools, scope and explicit zero values remain native")
			require.ErrorContains(t, plan.Tools(), "original provider", "cached search must never enter the DeepSeek converter")
		})
	}
}

func TestNativeCachedSearchCannotPromoteLiveTools(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate":     `{"tools":[{"type":"web_search","external_web_access":false},{"type":"web_search"}]}`,
		"live_fetch":    `{"tools":[{"type":"web_search","external_web_access":false},{"type":"web_fetch_20250910"}]}`,
		"live_options":  `{"tools":[{"type":"web_search","external_web_access":false}],"web_search_options":{}}`,
		"enable_search": `{"tools":[{"type":"web_search","external_web_access":false}],"enable_search":true}`,
		"string_false":  `{"tools":[{"type":"web_search","external_web_access":"false"}]}`,
		"numeric_zero":  `{"tools":[{"type":"web_search","external_web_access":0}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewPlan([]byte(body), "responses")
			require.Error(t, err)
		})
	}
	_, err := NewPlan([]byte(`{"tools":[{"type":"web_search_20250305","external_web_access":false}]}`), "claude")
	require.Error(t, err, "the native cached exception is scoped to Responses")
}

func TestLiveSearchStillUsesManagedDeepSeekPlan(t *testing.T) {
	for _, flag := range []string{"", `,"external_web_access":true`, `,"external_web_access":null`} {
		plan, err := NewPlan([]byte(`{"model":"gpt-6.1-sol","tools":[{"type":"web_search"`+flag+`}]}`), "responses")
		require.NoError(t, err)
		require.False(t, plan.NativeCachedSearch)
		require.True(t, plan.Search)
		require.True(t, plan.Fetch)
		require.NoError(t, plan.Tools())
	}
}

func TestResponsesPreviewIgnoresExternalAccessWithoutChangingOtherControls(t *testing.T) {
	for _, typ := range []string{"web_search_preview", "web_search_preview_2025_03_11"} {
		for _, flag := range []string{"false", "true", "null", `"false"`, "0"} {
			body := []byte(fmt.Sprintf(`{"tools":[{"type":%q,"external_web_access":%s}],"prompt_cache_key":"private-cache","prompt_cache_retention":"24h"}`, typ, flag))
			plan, err := NewPlan(body, "responses")
			require.NoError(t, err)
			require.False(t, plan.NativeCachedSearch)
			require.True(t, plan.Search)
			require.NoError(t, plan.Tools())
			require.Equal(t, "private-cache", Text(plan.Body["prompt_cache_key"]))
			require.Equal(t, "24h", Text(plan.Body["prompt_cache_retention"]))
		}
		_, err := NewPlan([]byte(fmt.Sprintf(`{"tools":[{"type":%q,"external_web_access":false,"user_location":{"city":"Paris"}}]}`, typ)), "responses")
		require.ErrorContains(t, err, "user_location")
	}
}
