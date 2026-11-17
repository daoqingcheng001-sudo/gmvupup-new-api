package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/websearch"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Build CPA's tools/responses-compat-fixture, then set CPA_COMPAT_FIXTURE to its
// absolute path. The child owns only loopback servers and synthetic credentials.
func TestResponsesCPACompatibilityHTTP(t *testing.T) {
	binary := os.Getenv("CPA_COMPAT_FIXTURE")
	if binary == "" {
		t.Skip("set CPA_COMPAT_FIXTURE to the paired CPA fixture binary")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.WaitDelay = 5 * time.Second
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	output, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	defer func() { input.Close(); require.NoError(t, cmd.Wait()) }()
	var ready struct {
		URL string `json:"url"`
	}
	require.NoError(t, common.DecodeJson(output, &ready))
	require.True(t, strings.HasPrefix(ready.URL, "http://127.0.0.1:"))
	stats := func() int64 {
		r, err := http.Get(ready.URL + "/fixture/stats")
		require.NoError(t, err)
		defer r.Body.Close()
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Zero(t, gjson.GetBytes(raw, "invalid").Int(), "CPA outgoing controls and model must be intact")
		return gjson.GetBytes(raw, "calls").Int()
	}
	service.InitTokenEncoders()
	t.Setenv("UNIFIED_WEB_SEARCH_ENABLED", "true")
	t.Setenv("DEEPSEEK_SEARCH_PROXY_URL", "")
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	oldRedis := common.RDB
	common.RDB = redisClient
	t.Cleanup(func() { common.RDB = oldRedis; redisClient.Close() })
	settings := model_setting.GetGlobalSettings()
	oldPass := settings.PassThroughRequestEnabled
	oldLog, oldExport := common.LogConsumeEnabled, common.DataExportEnabled
	settings.PassThroughRequestEnabled = false
	common.LogConsumeEnabled, common.DataExportEnabled = true, false
	t.Cleanup(func() {
		settings.PassThroughRequestEnabled = oldPass
		common.LogConsumeEnabled, common.DataExportEnabled = oldLog, oldExport
	})
	for _, tc := range []struct {
		name, model, tools        string
		stream, managed, rejected bool
		calls, quota              int
	}{
		{"plain_json", "public", `[]`, false, false, false, 1, 12},
		{"plain_sse", "public", `[]`, true, false, false, 1, 12},
		{"plain_prefix", "public", `[]`, false, false, false, 1, 12},
		{"history_only", "public", `[{"type":"function","name":"local_read","parameters":{"type":"object"}}]`, true, false, false, 1, 12},
		{"live_json", "public", `[{"type":"web_search","external_web_access":true}]`, false, true, false, 2, 62524},
		{"preview_false_sse", "public", `[{"type":"web_search_preview","external_web_access":false}]`, true, true, false, 2, 62524},
		{"cached_unsupported", "public", `[{"type":"web_search","external_web_access":false}]`, false, false, true, 0, 0},
		{"cached_native", "native", `[{"type":"web_search","external_web_access":false}]`, false, false, false, 1, 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := stats()
			user, token := setupMidjourneyBillingTest(t, 1000000, 1000000)
			body := fmt.Sprintf(`{"model":%q,"input":"fixture","stream":%t,"prompt_cache_key":"private-cache-fixture","prompt_cache_retention":"24h","tools":%s}`, tc.model, tc.stream, tc.tools)
			if tc.name == "plain_prefix" {
				body = strings.Replace(body, `"input":"fixture"`, `"input":"explain ws_na_fixture"`, 1)
			}
			if tc.name == "history_only" {
				store := websearch.RedisStore{Scope: fmt.Sprintf("%d:%d", user.Id, token.Id)}
				require.NoError(t, store.Put(t.Context(), "ws_na_fixture", websearch.Record{
					Call:   websearch.Raw(map[string]any{"type": "function_call", "call_id": "past_call", "name": websearch.SearchFunction, "arguments": `{"query":"fixture"}`}),
					Result: websearch.Raw(map[string]any{"results": []any{map[string]string{"url": "https://example.com", "summary": "past source"}}}),
				}))
				body = strings.Replace(body, `"input":"fixture"`, `"input":[{"type":"web_search_call","id":"ws_na_fixture","status":"completed"},{"role":"user","content":"continue"}]`, 1)
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(common.RequestIdKey, "joint-"+tc.name)
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			request, err := helper.GetAndValidateRequest(c, types.RelayFormatOpenAIResponses)
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, Request: request, OriginModelName: tc.model, RequestURLPath: "/v1/responses", UsingGroup: "default", TokenGroup: "default", UserGroup: "default", StartTime: time.Now(), IsStream: tc.stream, RelayFormat: types.RelayFormatOpenAIResponses, RelayMode: relayconstant.RelayModeResponses, ChannelMeta: &relaycommon.ChannelMeta{}, ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{BuiltInTools: map[string]*relaycommon.BuildInToolInfo{dto.BuildInToolWebSearch: {ToolName: dto.BuildInToolWebSearch}}}, UserSetting: dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningThreshold: 1}, PriceData: types.PriceData{ModelRatio: 1, CompletionRatio: 1, CacheRatio: 1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}}
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, ready.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "fixture")
			common.SetContextKey(c, constant.ContextKeyOriginalModel, tc.model)
			require.Nil(t, service.PreConsumeBilling(c, 100, info))
			run := func() *types.NewAPIError { return ResponsesHelper(c, info) }
			var apiErr *types.NewAPIError
			backend := &webTestBackend{}
			if tc.managed {
				plan, err := websearch.NewPlan([]byte(body), "responses")
				require.NoError(t, err)
				session := &websearch.Session{Backend: backend, Store: webTestStore{}}
				apiErr = runManagedWeb(c, info, run, []byte(body), plan, session)
			} else {
				apiErr = ManagedWebHelper(c, info, run)
			}
			if tc.rejected {
				require.NotNil(t, apiErr)
				require.Equal(t, 400, apiErr.StatusCode)
				require.Contains(t, apiErr.Error(), "cache-only")
				info.Billing.Refund(c)
			} else {
				require.Nil(t, apiErr)
				require.Equal(t, 200, recorder.Code)
				result, err := websearch.Snapshot("responses", recorder.Body.Bytes())
				require.NoError(t, err)
				encoded := websearch.Raw(result)
				require.Contains(t, string(encoded), "COMPAT_OK")
				require.Equal(t, "completed", gjson.GetBytes(encoded, "status").String())
			}
			require.EqualValues(t, tc.calls, stats()-before)
			require.Equal(t, map[bool]int{true: 1, false: 0}[tc.managed], backend.calls)
			wallet, remaining := persistedMidjourneyQuota(t, user.Id, token.Id)
			require.Equal(t, 1000000-tc.quota, wallet)
			require.Equal(t, wallet, remaining)
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("user_id=? AND type=?", user.Id, model.LogTypeConsume).Find(&logs).Error)
			if tc.rejected {
				require.Empty(t, logs)
			} else {
				require.Len(t, logs, 1)
				require.Equal(t, tc.quota, logs[0].Quota)
			}
		})
	}
}
