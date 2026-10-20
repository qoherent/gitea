// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A runner's scope decides which repositories may send it work. The three
// cases are documented on ActionRunner itself and encoded a second time in
// FindRunnerOptions.ToConds, so they are worth pinning: a global runner takes
// jobs from every repository on the instance, matched on label text alone.
func TestActionRunnerScope(t *testing.T) {
	cases := []struct {
		name   string
		runner ActionRunner
		expect RunnerScope
	}{
		{"both zero is global", ActionRunner{}, RunnerScopeGlobal},
		{"an owner makes it owner-scoped", ActionRunner{OwnerID: 7}, RunnerScopeOwner},
		{"a repository makes it repository-scoped", ActionRunner{RepoID: 9}, RunnerScopeRepository},
		{
			// Documented as unacceptable on the model, and ToConds resolves it
			// the same way: RepoID is tested first and OwnerID ignored.
			"a repository wins when both are set",
			ActionRunner{OwnerID: 7, RepoID: 9},
			RunnerScopeRepository,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.expect, c.runner.Scope())
		})
	}
}

// Scope is read on runners that came straight from a query, where Owner is
// nil because it is `xorm:"-"`. BelongsToOwnerType dereferences it and would
// panic; this must not.
func TestActionRunnerScopeNeedsNoLoadedAttributes(t *testing.T) {
	runner := ActionRunner{OwnerID: 7}
	assert.Nil(t, runner.Owner)
	assert.NotPanics(t, func() { _ = runner.Scope() })
	assert.Equal(t, RunnerScopeOwner, runner.Scope())
}
