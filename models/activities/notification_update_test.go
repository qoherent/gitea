// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities

import (
	"testing"

	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The concurrent-insert fallback clears the insert's error only when there is
// a notification to update. Updating one that is not there is not success.
func TestUpdateIssueNotificationSaysWhetherThereWasOne(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	updated, err := updateIssueNotification(t.Context(), 1, 1, 0, 2)
	require.NoError(t, err)
	assert.True(t, updated, "user 1 has a notification on issue 1 in the fixtures")

	updated, err = updateIssueNotification(t.Context(), 1, 99999, 0, 2)
	require.NoError(t, err)
	assert.False(t, updated, "there is no notification on an issue that does not exist")
}
