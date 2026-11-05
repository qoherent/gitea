// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"net/http/httptest"
	"testing"

	actions_model "gitea.dev/models/actions"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A v1-v3 download is streamed, so the first byte written settles the status
// code. Closing a zip writes the archive's central directory -- a write --
// which meant a deferred close turned a failure on the very first file into
// an empty but perfectly valid archive, downloaded with a 200, while the
// caller's error status arrived too late to be sent.
func TestDownloadArtifactV3LeavesTheResponseToTheCallerWhenAFileCannotBeRead(t *testing.T) {
	recorder := httptest.NewRecorder()
	missing := &actions_model.ActionArtifact{
		ArtifactName: "by-v3",
		ArtifactPath: "a.txt",
		StoragePath:  "no/such/object",
	}

	err := DownloadArtifactV3(recorder, "by-v3", []*actions_model.ActionArtifact{missing})

	require.Error(t, err)
	assert.Empty(t, recorder.Body.Bytes(), "a closed zip is a response body, and a response body is a 200")
	assert.Empty(t, recorder.Header().Get("Content-Disposition"), "the download header outlived the download")
}
