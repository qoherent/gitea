// Copyright 2026 The RIA Hub Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package riahub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The receiver of these events lives in another repository, and nothing else
// compares the two. This fixture is the contract: the same bytes are checked
// in to ria-hub at shared/fixtures/riahub-push-event.json, where its own
// ingestion test feeds them through the route that reads them.
//
// Change the Event struct and this fails. Change the reader over there and
// that fails. Either way somebody is told, which is the whole point -- the
// alternative is a renamed field that nobody notices until no push is
// catalogued, and that absence is exactly the fault this channel exists to
// stop having.
const fixturePath = "testdata/riahub-push-event.json"

func TestTheReportedEventMatchesTheContract(t *testing.T) {
	event := &Event{
		EventID:      "8f1d4c2e-6b3a-4e5f-9a7c-2d8b1e0f3a64",
		Kind:         KindPushed,
		RepositoryID: 5,
		Owner:        "ben",
		Name:         "lfs-test",
		Ref:          "refs/heads/main",
		CommitSHA:    "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
		Paths: []PathFact{
			{Path: "data/one.npy", Change: Added, IsLFS: true, LfsOid: "b" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde", LfsSize: 4096},
			{Path: "notes/README.md", Change: Modified},
			{Path: "old/gone.sigmf-data", Change: Removed},
		},
	}

	encoded, err := json.MarshalIndent(event, "", "  ")
	require.NoError(t, err)

	expected, err := os.ReadFile(filepath.Clean(fixturePath))
	require.NoError(t, err, "the contract fixture is missing")

	assert.JSONEq(t, string(expected), string(encoded),
		"the reported shape changed; update ria-hub's copy of this fixture in the same change")
}

func TestARemovedPathCarriesNoLFSFields(t *testing.T) {
	// `omitempty` on the oid and size, so a removal does not claim an object
	// of all zeroes. The reader treats a present oid as a real one.
	encoded, err := json.Marshal(PathFact{Path: "gone.npy", Change: Removed})
	require.NoError(t, err)

	var back map[string]any
	require.NoError(t, json.Unmarshal(encoded, &back))
	assert.NotContains(t, back, "lfs_oid")
	assert.NotContains(t, back, "lfs_size")
	assert.Equal(t, false, back["is_lfs"])
}
