// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package websocket

import (
	"testing"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A state event waiting in a connection's queue carries the state as it was
// when it was published. Sent after a newer snapshot -- as on a reconnect --
// it would replace that snapshot with older state, so it is read again as it
// is sent.
func TestCurrentReadsStateAsItIsSent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	unread, err := db.Count[activities_model.Notification](t.Context(), activities_model.FindNotificationOptions{
		UserID: user.ID,
		Status: []activities_model.NotificationStatus{activities_model.NotificationStatusUnread},
	})
	require.NoError(t, err)

	stale := makeEventMessage(eventNotificationCount, "", struct {
		Count int64 `json:"count"`
	}{unread + 41})
	var sent struct {
		EventData struct {
			Count int64 `json:"count"`
		} `json:"eventData"`
	}
	require.NoError(t, json.Unmarshal(EventForSession(Current(t.Context(), user, stale), "any-session"), &sent))
	assert.Equal(t, unread, sent.EventData.Count, "the queued count was sent instead of the current one")

	// An event that carries no state is sent as it was published.
	logout := makeEventMessage(eventLogout, "sess-A", nil)
	assert.Equal(t, logout, Current(t.Context(), user, logout))
}
