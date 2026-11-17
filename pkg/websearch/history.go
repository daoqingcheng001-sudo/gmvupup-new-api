package websearch

import (
	"strings"

	"github.com/tidwall/gjson"
)

// Only protocol-owned reference positions identify platform search history.
// Text, metadata, schemas and client function IDs are ordinary caller data.
func managedHistoryReference(protocol string, item gjson.Result) string {
	field := "id"
	switch protocol {
	case "responses":
		switch item.Get("type").String() {
		case "web_search_call", "item_reference":
		default:
			return ""
		}
	case "claude":
		switch item.Get("type").String() {
		case "server_tool_use":
			name := item.Get("name").String()
			if name != "web_search" && name != "web_fetch" {
				return ""
			}
		case "web_search_tool_result", "web_fetch_tool_result":
			field = "tool_use_id"
		default:
			return ""
		}
	default:
		return ""
	}
	id := item.Get(field)
	if id.Type != gjson.String || !strings.HasPrefix(id.String(), "ws_na_") {
		return ""
	}
	return id.String()
}

func (p *Plan) HasManagedHistory() bool {
	if p == nil {
		return false
	}
	switch p.Protocol {
	case "responses":
		for _, item := range Arr(p.Body["input"]) {
			if managedHistoryReference(p.Protocol, gjson.ParseBytes(item)) != "" {
				return true
			}
		}
	case "claude":
		for _, message := range Arr(p.Body["messages"]) {
			m := gjson.ParseBytes(message)
			if m.Get("role").String() != "assistant" || !m.Get("content").IsArray() {
				continue
			}
			for _, item := range m.Get("content").Array() {
				if managedHistoryReference(p.Protocol, item) != "" {
					return true
				}
			}
		}
	}
	return false
}
