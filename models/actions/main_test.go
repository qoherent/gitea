// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	"gitea.dev/models/unittest"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m, &unittest.TestOptions{
		FixtureFiles: []string{
			"action_runner_token.yml",
			"action_run.yml",
			"repository.yml",
			"user.yml",
			// The privileged-compute gate is a condition on the dispatch
			// query joining team and team_user, and an organization-scoped
			// runner only sees repositories carrying the Actions unit.
			// Without these three the gate's tests pass because nothing is
			// ever selected, which is not the same as being refused.
			"repo_unit.yml",
			"team.yml",
			"team_user.yml",
		},
	})
}
