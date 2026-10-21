// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package privilegedcompute records who was given reach over the instance's
// own hardware, and when.
//
// Both halves of the gate hand out code execution on machines the instance
// owns: marking a runner privileged, and granting a team the right to reach
// one. Neither is recoverable from the row afterwards -- a boolean says what
// is true now and nothing about who made it true or when -- so "who could
// reach this machine on that date" had no answer at all.
//
// Written as a system notice rather than a table of its own. The admin notice
// list already exists, is already the place an administrator looks for things
// the instance did on its own account, and needs no schema. The cost is that
// notices are prunable, which is the right trade for a trail that supports a
// question rather than enforcing anything.
package privilegedcompute

import (
	"context"
	"fmt"
	"strings"

	actions_model "gitea.dev/models/actions"
	organization_model "gitea.dev/models/organization"
	"gitea.dev/models/system"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/log"
)

// actor names whoever made the change, for a line somebody reads months later.
// A nil doer is a change made by no signed-in person -- a command-line call or
// a fixture -- and is named as such rather than left blank.
func actor(doer *user_model.User) string {
	if doer == nil {
		return "(no signed-in user)"
	}
	return doer.Name
}

func verb(granted bool) string {
	if granted {
		return "granted"
	}
	return "revoked"
}

// RunnerMarked records a change to a runner's privileged mark.
//
// Failing to write the trail must not fail the change that was asked for: an
// administrator who ticked a box and got an error would reasonably conclude
// the box is not ticked, when it is. Logged instead, where the same
// administrator can still find it.
func RunnerMarked(ctx context.Context, doer *user_model.User, runner *actions_model.ActionRunner, privileged bool) {
	owner := "no organization"
	if runner.OwnerID != 0 {
		if org, err := user_model.GetUserByID(ctx, runner.OwnerID); err == nil {
			owner = "organization " + org.Name
		}
	}
	if err := system.CreateNotice(ctx, system.NoticeTask,
		"privileged compute %s on runner %q (id %d, %s, labels %v) by %s",
		verb(privileged), runner.Name, runner.ID, owner, runner.AgentLabels, actor(doer),
	); err != nil {
		log.Error("privilegedcompute: could not record the runner mark: %v", err)
	}
}

// TeamGranted records a change to a team's privileged-compute grant.
//
// The team's members are named rather than counted. The grant is held through
// membership, so who it reached on the day it changed is not recoverable from
// the team afterwards -- somebody added next week looks identical to somebody
// who was there.
func TeamGranted(ctx context.Context, doer *user_model.User, team *organization_model.Team, granted bool) {
	org := fmt.Sprintf("organization id %d", team.OrgID)
	if o, err := user_model.GetUserByID(ctx, team.OrgID); err == nil {
		org = "organization " + o.Name
	}

	members := "(membership could not be read)"
	if users, err := organization_model.GetTeamMembers(ctx, &organization_model.SearchMembersOptions{TeamID: team.ID}); err == nil {
		names := make([]string, 0, len(users))
		for _, u := range users {
			names = append(names, u.Name)
		}
		if len(names) == 0 {
			members = "(no members)"
		} else {
			members = strings.Join(names, ", ")
		}
	}

	if err := system.CreateNotice(ctx, system.NoticeTask,
		"privileged compute %s to team %q of %s by %s; members at the time: %s",
		verb(granted), team.Name, org, actor(doer), members,
	); err != nil {
		log.Error("privilegedcompute: could not record the team grant: %v", err)
	}
}
