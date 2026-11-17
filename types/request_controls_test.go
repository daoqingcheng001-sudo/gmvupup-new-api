package types

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/tidwall/gjson"
	"strings"
	"testing"
)

func TestRequestControlsNeverRetainsValues(t *testing.T) {
	body := `{"prompt_cache_key":"private-cache-key","prompt_cache_retention":{"private":"retention"},"prompt_cache_options":null,"tools":[{"type":"web_search","name":"private-tool","external_web_access":false,"user_location":{"city":"private-city"}},{"type":"web_search_2025_08_26","external_web_access":true},{"type":"web_search_preview","external_web_access":null},{"type":"web_search"},{"type":"web_search","external_web_access":"private-access"},{"type":"function","name":"web_search","parameters":{"external_web_access":false}}]}`
	d := InspectRequestControls(gjson.Parse(body))
	if !d.Valid() || d.PromptCacheKey != "string" || d.PromptCacheRetention != "object" || d.PromptCacheOptions != "null" || d.SearchTools != 5 || d.PreviewTools != 1 || d.AccessFalse != 1 || d.AccessTrue != 1 || d.AccessAbsent != 1 || d.AccessNull != 1 || d.AccessInvalid != 1 {
		t.Fatal(d)
	}
	b, err := common.Marshal(d)
	if err != nil || strings.Contains(string(b), "private") {
		t.Fatal("unsafe control metadata", string(b), err)
	}
	clone := d.Clone()
	clone.PromptCacheKey = "private-value"
	if clone.Valid() || d.PromptCacheKey != "string" {
		t.Fatal("validation or clone failure")
	}
	empty := InspectRequestControls(gjson.Parse(`{"tools":null}`))
	if !empty.Valid() || empty.SearchTools != 0 || empty.PromptCacheKey != "absent" {
		t.Fatal(empty)
	}
}

func TestRequestControlsWalkIsBounded(t *testing.T) {
	body := `{"tools":[` + strings.Repeat(`{"type":"web_search","external_web_access":false},`, 4100) + `{}]}`
	d := InspectRequestControls(gjson.Parse(body))
	if !d.Valid() || !d.Truncated || d.SearchTools != 4096 {
		t.Fatal(d)
	}
}
