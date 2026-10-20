// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	"gitea.dev/modules/container"

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
