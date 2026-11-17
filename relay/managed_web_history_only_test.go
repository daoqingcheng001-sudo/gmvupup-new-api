package relay

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/websearch"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestManagedHistoryPrefixInOrdinaryDataDoesNotActivateSearch(t *testing.T) {
	t.Setenv("UNIFIED_WEB_SEARCH_ENABLED", "true")
	t.Setenv("DEEPSEEK_SEARCH_PROXY_URL", "")
	t.Setenv("DEEPSEEK_SEARCH_PROXY_KEY_FILE", "")
	oldRedis := common.RDB
	common.RDB = nil
	t.Cleanup(func() { common.RDB = oldRedis })
	for _, input := range []string{
		`"explain ws_na_fixture"`,
		`[{"role":"user","content":"ws_na_fixture"}]`,
		`[{"type":"message","id":"ws_na_fixture","role":"user","content":"fixture"}]`,
	} {
		for _, cached := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cached=%t", input, cached), func(t *testing.T) {
				tools := `[{"type":"function","name":"local_read","parameters":{"type":"object","description":"ws_na_fixture"}}]`
				if cached {
					tools = `[{"type":"web_search","external_web_access":false}]`
				}
				body := `{"model":"fixture","input":` + input + `,"tools":` + tools + `}`
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				t.Cleanup(func() { common.CleanupBodyStorage(c) })
				request, err := helper.GetAndValidateRequest(c, types.RelayFormatOpenAIResponses)
				require.NoError(t, err)
				info := &relaycommon.RelayInfo{Request: request, RelayFormat: types.RelayFormatOpenAIResponses, RelayMode: relayconstant.RelayModeResponses}
				calls := 0
				apiErr := ManagedWebHelper(c, info, func() *types.NewAPIError {
					calls++
					storage, err := common.GetBodyStorage(c)
					require.NoError(t, err)
					got, err := storage.Bytes()
					require.NoError(t, err)
					require.JSONEq(t, body, string(got))
					require.Nil(t, info.ManagedWeb)
					return nil
				})
				require.Nil(t, apiErr)
				require.Equal(t, 1, calls)
			})
		}
	}
}

func TestManagedHistoryOnlyPreservesFunctionsWithoutLiveSearchCredentials(t *testing.T) {
	t.Setenv("UNIFIED_WEB_SEARCH_ENABLED", "true")
	t.Setenv("DEEPSEEK_SEARCH_PROXY_URL", "")
	t.Setenv("DEEPSEEK_SEARCH_PROXY_KEY_FILE", "")
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	oldRedis := common.RDB
	common.RDB = client
	t.Cleanup(func() { common.RDB = oldRedis; client.Close() })
	store := websearch.RedisStore{Scope: "1:10"}
	require.NoError(t, store.Put(t.Context(), "ws_na_fixture", websearch.Record{
		Call:   websearch.Raw(map[string]any{"type": "function_call", "call_id": "past_call", "name": websearch.SearchFunction, "arguments": `{"query":"fixture"}`}),
		Result: websearch.Raw(map[string]any{"results": []any{map[string]string{"url": "https://example.com", "summary": "past source"}}}),
	}))
	for _, referenceType := range []string{"web_search_call", "item_reference"} {
		for _, token := range []int{10, 20} {
			t.Run(fmt.Sprintf("%s/token=%d", referenceType, token), func(t *testing.T) {
				body := fmt.Sprintf(`{"model":"fixture","input":[{"type":%q,"id":"ws_na_fixture"},{"role":"user","content":"continue"}],"tools":[{"type":"function","name":"local_read","parameters":{"type":"object"}}],"prompt_cache_key":"private-cache","stream":true}`, referenceType)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				t.Cleanup(func() { common.CleanupBodyStorage(c) })
				original, err := helper.GetAndValidateRequest(c, types.RelayFormatOpenAIResponses)
				require.NoError(t, err)
				storage, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				info := &relaycommon.RelayInfo{UserId: 1, TokenId: token, Request: original, RelayFormat: types.RelayFormatOpenAIResponses, RelayMode: relayconstant.RelayModeResponses, IsStream: true}
				calls := 0
				apiErr := ManagedWebHelper(c, info, func() *types.NewAPIError {
					calls++
					encoded, err := common.Marshal(info.Request)
					require.NoError(t, err)
					require.Contains(t, string(encoded), "past source")
					require.NotContains(t, string(encoded), "ws_na_fixture")
					require.Equal(t, "local_read", gjson.GetBytes(encoded, "tools.0.name").String())
					require.Equal(t, "private-cache", gjson.GetBytes(encoded, "prompt_cache_key").String())
					require.Equal(t, "history_only", common.GetContextKeyString(c, constant.ContextKeyWebSearchMode))
					require.Nil(t, info.ManagedWeb)
					return nil
				})
				if token == 10 {
					require.Nil(t, apiErr)
					require.Equal(t, 1, calls)
				} else {
					require.NotNil(t, apiErr)
					require.Equal(t, 409, apiErr.StatusCode)
					require.Zero(t, calls)
				}
				restored, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				require.Same(t, storage, restored)
				require.Same(t, original, info.Request)
			})
		}
	}
}
