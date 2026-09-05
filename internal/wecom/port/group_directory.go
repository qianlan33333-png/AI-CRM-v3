package port

import "context"

// GroupChatReader is a read-only external-contact boundary. It deliberately
// excludes every mutation endpoint.
type GroupChatReader interface {
	ListGroupChats(context.Context, string, string, int) (GroupChatPage, error)
	GetGroupChat(context.Context, string) (GroupChat, error)
}

type GroupChatPage struct {
	Items      []GroupChatListItem
	NextCursor string
}

type GroupChatListItem struct {
	ChatID string
	Status int
}

type GroupChat struct {
	ChatID      string
	OwnerUserID string
	Name        string
	MemberCount int
}
