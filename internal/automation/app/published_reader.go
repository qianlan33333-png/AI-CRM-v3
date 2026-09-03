package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

func (s *Service) PublishedAgent(ctx context.Context, id automationport.AgentID) (automationport.PublishedAgent, bool, error) {
	if !ready(s) || id < 1 {
		return automationport.PublishedAgent{}, false, ErrAgentUnavailable
	}
	var agent automationport.Agent
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; agent, e = s.store.Get(tx, id); return e })
	if err != nil {
		return automationport.PublishedAgent{}, false, classify(err)
	}
	if agent.Status == automationport.AgentStatusArchived || agent.PublishedVersion < 1 {
		return automationport.PublishedAgent{}, false, nil
	}
	contentInput := map[string]any{"automation_type": agent.AutomationType, "published_role_prompt": agent.PublishedRolePrompt, "published_task_prompt": agent.PublishedTaskPrompt, "content_text": agent.FixedContentPackage.ContentText}
	contentRaw, _ := json.Marshal(contentInput)
	materialsInput := map[string]any{"images": agent.FixedContentPackage.ImageLibraryIDs, "mini_programs": agent.FixedContentPackage.MiniprogramLibraryIDs, "attachments": agent.FixedContentPackage.AttachmentLibraryIDs, "group_invites": agent.FixedContentPackage.GroupInviteLibraryIDs, "dynamic_card": agent.FixedContentPackage.DynamicMiniprogramCard}
	materialsRaw, _ := json.Marshal(materialsInput)
	return automationport.PublishedAgent{AgentID: agent.ID, AutomationType: agent.AutomationType, Status: agent.Status, PublishedVersion: agent.PublishedVersion, ContentDigest: sha256.Sum256(contentRaw), MaterialsDigest: sha256.Sum256(materialsRaw)}, true, nil
}

var _ automationport.PublishedAgentReader = (*Service)(nil)
