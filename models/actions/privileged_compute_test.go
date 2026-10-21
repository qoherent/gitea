// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/container"
	"gitea.dev/modules/util"

	"github.com/stretchr/testify/assert"
)

// A label held by a privileged runner is reserved. Runners declare their own
// labels and dispatch matches on label text alone, so without this an
// unprivileged machine can declare the GPU box's label and be handed its work.
func TestClaimsReservedLabel(t *testing.T) {
	reserved := make(container.Set[string])
	reserved.AddMultiple("churchill-3090", "horton-p4")

	cases := []struct {
		name   string
		runsOn []string
		expect bool
	}{
		{"a job wanting a reserved label", []string{"churchill-3090"}, true},
		{"a job wanting an ordinary label", []string{"ubuntu-latest"}, false},
		{"one reserved among several", []string{"ubuntu-latest", "horton-p4"}, true},
		{"no labels at all", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.expect, claimsReservedLabel(reserved, c.runsOn))
		})
	}
}

// The empty reserved set is the ordinary case -- an instance with no
// privileged runners -- and must not refuse anything.
func TestClaimsReservedLabelWithNothingReserved(t *testing.T) {
	assert.False(t, claimsReservedLabel(make(container.Set[string]), []string{"anything"}))
}

// TestSetRunnerPrivilegedComputeRefusesAnOwnerlessRunner is the API's half of
// the refusal the web form already makes. CreateTaskForRunner fails closed on
// a privileged runner with no owning organization -- the grant that gates it
// is per-organization, so there is nothing to check against -- which makes
// such a row a runner no job can ever reach. Refusing to create it is the
// other half, and the API could create it until this existed.
func TestSetRunnerPrivilegedComputeRefusesAnOwnerlessRunner(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	global := &ActionRunner{Name: "global", OwnerID: 0, RepoID: 0, UUID: "11111111-1111-1111-1111-111111111111"}
	assert.NoError(t, db.Insert(t.Context(), global))

	err := SetRunnerPrivilegedCompute(t.Context(), global, true)
	assert.ErrorIs(t, err, util.ErrInvalidArgument)
	assert.False(t, unittest.AssertExistsAndLoadBean(t, &ActionRunner{ID: global.ID}).PrivilegedCompute)

	// Unmarking one is always allowed: a runner that should not be privileged
	// must be demotable whatever scope it ended up at.
	assert.NoError(t, SetRunnerPrivilegedCompute(t.Context(), global, false))
}

func TestSetRunnerPrivilegedComputeMarksAnOrganizationsRunner(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	owned := &ActionRunner{Name: "gpu", OwnerID: 3, RepoID: 0, UUID: "22222222-2222-2222-2222-222222222222"}
	assert.NoError(t, db.Insert(t.Context(), owned))

	assert.NoError(t, SetRunnerPrivilegedCompute(t.Context(), owned, true))
	assert.True(t, unittest.AssertExistsAndLoadBean(t, &ActionRunner{ID: owned.ID}).PrivilegedCompute)

	assert.NoError(t, SetRunnerPrivilegedCompute(t.Context(), owned, false))
	assert.False(t, unittest.AssertExistsAndLoadBean(t, &ActionRunner{ID: owned.ID}).PrivilegedCompute)
}

