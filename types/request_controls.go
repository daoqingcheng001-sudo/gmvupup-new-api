package types

import (
	"github.com/tidwall/gjson"
	"strings"
)

// RequestControls retains only fixed enums and bounded counters. Never retain
// cache keys, tool names, locations, domains, schemas or customer text.
type RequestControls struct {
	PromptCacheKey       string `json:"prompt_cache_key_type"`
	PromptCacheRetention string `json:"prompt_cache_retention_type"`
	PromptCacheOptions   string `json:"prompt_cache_options_type"`
	SearchTools          int    `json:"search_tools"`
	PreviewTools         int    `json:"preview_tools"`
	AccessFalse          int    `json:"external_access_false"`
	AccessTrue           int    `json:"external_access_true"`
	AccessAbsent         int    `json:"external_access_absent"`
	AccessNull           int    `json:"external_access_null"`
	AccessInvalid        int    `json:"external_access_invalid"`
	Truncated            bool   `json:"truncated,omitempty"`
}

func InspectRequestControls(root gjson.Result) RequestControls {
	d := RequestControls{
		PromptCacheKey:       controlKind(root.Get("prompt_cache_key")),
		PromptCacheRetention: controlKind(root.Get("prompt_cache_retention")),
		PromptCacheOptions:   controlKind(root.Get("prompt_cache_options")),
	}
	visited := 0
	tools := root.Get("tools")
	if !tools.IsArray() {
		return d
	}
	tools.ForEach(func(_, tool gjson.Result) bool {
		visited++
		if visited > 4096 {
			d.Truncated = true
			return false
		}
		typ := tool.Get("type").String()
		if typ != "web_search" && !strings.HasPrefix(typ, "web_search_") {
			return true
		}
		d.SearchTools++
		if strings.HasPrefix(typ, "web_search_preview") {
			d.PreviewTools++
		}
		access := tool.Get("external_web_access")
		switch {
		case !access.Exists():
			d.AccessAbsent++
		case access.Type == gjson.Null:
			d.AccessNull++
		case access.Type == gjson.False:
			d.AccessFalse++
		case access.Type == gjson.True:
			d.AccessTrue++
		default:
			d.AccessInvalid++
		}
		return true
	})
	return d
}

func controlKind(v gjson.Result) string {
	switch {
	case !v.Exists():
		return "absent"
	case v.Type == gjson.Null:
		return "null"
	case v.IsArray():
		return "array"
	case v.IsObject():
		return "object"
	case v.Type == gjson.String:
		return "string"
	case v.Type == gjson.Number:
		return "number"
	default:
		return "boolean"
	}
}

func (d *RequestControls) Valid() bool {
	if d == nil {
		return true
	}
	for _, kind := range []string{d.PromptCacheKey, d.PromptCacheRetention, d.PromptCacheOptions} {
		switch kind {
		case "absent", "null", "array", "object", "string", "number", "boolean":
		default:
			return false
		}
	}
	for _, n := range []int{d.SearchTools, d.PreviewTools, d.AccessFalse, d.AccessTrue, d.AccessAbsent, d.AccessNull, d.AccessInvalid} {
		if n < 0 || n > 4096 {
			return false
		}
	}
	return d.PreviewTools <= d.SearchTools && d.AccessFalse+d.AccessTrue+d.AccessAbsent+d.AccessNull+d.AccessInvalid == d.SearchTools
}

func (d *RequestControls) Clone() *RequestControls {
	if d == nil {
		return nil
	}
	c := *d
	return &c
}
