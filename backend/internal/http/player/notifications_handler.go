package player

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/http/authn"
	"github.com/jamaa/gathering/backend/internal/pkg/caller"
)

// The inbox: the feed, a page at a time with the unread count, and marking
// read. docs/FRIENDS.md is the contract.

type notificationsRequest struct {
	Cursor string `query:"cursor" doc:"next_cursor from the page before; leave it out for the first page."`
	Limit  int    `query:"limit" default:"20" doc:"1 to 50 (limit: 1_to_50)."`
	Unread bool   `query:"unread" doc:"true lists the unread ones only."`
}

type notificationsResponse struct {
	Body *inbox.Feed
}

type notificationsReadRequest struct {
	Body struct {
		IDs []string `json:"ids,omitempty" doc:"Up to 100 (ids: up_to_100). Ids that are not the caller's are passed over."`
		All bool     `json:"all,omitempty" doc:"Every one, whatever ids says."`
	}
}

func (h *Handler) registerNotifications(api huma.API) {
	session := map[string]any{authn.ScopeKey: caller.ScopeSession}

	huma.Register(api, huma.Operation{
		OperationID: "notifications", Method: http.MethodGet, Path: "/v1/notifications",
		Summary: "My inbox",
		Description: "Newest first, a page at a time: hand next_cursor back as cursor until it is " +
			"null. unread is the whole unread count, whatever the page: a badge reads it from limit=1.",
		Tags: []string{"notifications"}, Metadata: session,
	}, h.notifications)

	huma.Register(api, huma.Operation{
		OperationID: "notifications-read", Method: http.MethodPost, Path: "/v1/notifications/read",
		Summary:     "Mark read",
		Description: "The ones named, or all. ids: required when there are none and all is not set.",
		Tags:        []string{"notifications"}, Metadata: session, DefaultStatus: http.StatusNoContent,
	}, h.readNotifications)
}

func (h *Handler) notifications(ctx context.Context, in *notificationsRequest) (*notificationsResponse, error) {
	feed, err := h.Inbox.Feed(ctx, caller.FromContext(ctx).Subject, in.Cursor, in.Limit, in.Unread)
	if err != nil {
		return nil, err
	}
	return &notificationsResponse{Body: feed}, nil
}

func (h *Handler) readNotifications(ctx context.Context, in *notificationsReadRequest) (*noContent, error) {
	return nil, h.Inbox.Read(ctx, caller.FromContext(ctx).Subject, in.Body.IDs, in.Body.All)
}
