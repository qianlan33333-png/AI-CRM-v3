package config

import "testing"

func TestRuntimeCatalogRetainsTwelveLegacyCategoriesAndReferencePresence(t *testing.T) {
	catalog := RuntimeCatalog(map[string]bool{
		"environment://AICRM_WECOM_SECRET": true,
	})
	if len(catalog) != 12 || catalog[0].Key != "wecom_base" || catalog[11].Key != "wechat_oauth" {
		t.Fatalf("catalog categories=%#v", catalog)
	}
	var secretConfigured, apiBase, workerLimit, scopeBound bool
	for _, category := range catalog {
		for _, field := range category.Fields {
			switch field.Key {
			case "WECOM_SECRET":
				secretConfigured = field.Configured != nil && *field.Configured
			case "WECOM_API_BASE":
				apiBase = field.Input == "deployment" && field.Unsupported != ""
			case "stability.worker_limit":
				workerLimit = field.Label == "Inbox 单次 claim 处理条数"
			case "wecom.corp_id":
				scopeBound = field.Input == "scope-bound"
			}
		}
	}
	if !secretConfigured || !apiBase || !workerLimit || !scopeBound {
		t.Fatalf("catalog presence/field mapping secret=%t apiBase=%t worker=%t scope=%t", secretConfigured, apiBase, workerLimit, scopeBound)
	}
}
