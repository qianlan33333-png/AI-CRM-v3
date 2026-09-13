// Command check-enterprise-directory verifies only the application-visible
// WeCom directory envelope before an Access governance release. It never
// opens the local database or performs a Provider write.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
)

func main() {
	client, err := wecomadapter.New(wecomadapter.Config{
		Enabled: true, CorpID: os.Getenv("AICRM_WECOM_CORP_ID"), AgentID: os.Getenv("AICRM_WECOM_AGENT_ID"), Secret: os.Getenv("AICRM_WECOM_SECRET"),
		AdminCallbackURI: "https://localhost/access-directory-preflight", SidebarCallbackURI: "https://localhost/access-directory-preflight",
	})
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// Keep provider values in adapter memory only. This contains aggregates
		// and a coarse stage name, never IDs, scope envelopes, tokens, errors or
		// raw provider responses.
		preflight := client.PreflightEnterpriseDirectory(ctx)
		encoded, _ := json.Marshal(preflight)
		fmt.Println(string(encoded))
		if preflight.Complete {
			return
		}
	}
	fmt.Println(`{"complete":false,"failure_stage":"config","scope_user_count":0,"scope_department_count":0,"directory_department_count":0,"directory_component_count":0,"department_employee_count":0,"direct_employee_count":0,"employee_count":0}`)
	os.Exit(1)
}
