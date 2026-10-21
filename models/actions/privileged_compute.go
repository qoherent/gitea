// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Everything the privileged-compute gate adds to this package, kept out of the
// upstream files it would otherwise be wedged into.
//
// The two columns have to sit on their structs and the dispatch condition has
// to sit inside CreateTaskForRunner. Nothing else does, and a fork pays for
// every line in a file upstream also edits -- at every merge, forever.

package actions

import (
	"context"

	"gitea.dev/models/db"
	"gitea.dev/modules/container"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// SetRunnerPrivilegedCompute marks or unmarks a runner as privileged.
//
// Refuses a runner with no owning organization, for the reason
// CreateTaskForRunner fails closed on one: the grant that gates a privileged
// runner is per-organization, so such a runner has nothing to check against
// and could never be dispatched to. The web form refuses it too; this is the
// same refusal for the API.
//
// The task version is bumped as SetRunnerDisabled bumps it, because which jobs
// this runner may be handed has changed.
//
// @sigil implements models/actions/privileged-compute.sigil::PrivilegedCompute::RunnerMarking interface
func SetRunnerPrivilegedCompute(ctx context.Context, runner *ActionRunner, privileged bool) error {
	if privileged && runner.OwnerID == 0 {
		return util.NewInvalidArgumentErrorf("a privileged runner must belong to an organization")
	}
	if runner.PrivilegedCompute == privileged {
		return nil
	}

	return db.WithTx(ctx, func(ctx context.Context) error {
		runner.PrivilegedCompute = privileged
		if err := UpdateRunner(ctx, runner, "privileged_compute"); err != nil {
			return err
		}
		return IncreaseTaskVersion(ctx, runner.OwnerID, runner.RepoID)
	})
}

// FilterReservedLabels drops labels that belong to a privileged runner which
// this runner is not entitled to answer for, returning the labels it may keep
// and those removed.
//
// Runners declare their own labels, and dispatch matches on label text alone.
// Without this, any runner -- a laptop registered by anyone who can add an
// organization runner -- could declare "churchill-3090" and receive work meant
// for the GPU box, including whatever datasets and secrets that job pulls. The
// grant checked in CreateTaskForRunner governs who may *submit* such work; it
// says nothing about which machine ends up running it.
//
// A label held by a privileged runner is therefore reserved to privileged
// runners of the same organization. The reservation is instance-wide because a
// global runner takes jobs from every repository, so a narrower scope would
// leave the interception open. Only a site admin can mark a runner privileged,
// so the reserved set stays administrator-controlled.
//
// @sigil implements models/actions/privileged-compute.sigil::PrivilegedCompute::LabelReservation interface
func FilterReservedLabels(ctx context.Context, runner *ActionRunner, labels []string) (keep, dropped []string, err error) {
	if len(labels) == 0 {
		return labels, nil, nil
	}

	var privileged []*ActionRunner
	if err := db.GetEngine(ctx).
		Where(builder.Eq{"privileged_compute": true}).
		And(builder.Neq{"id": runner.ID}).
		Find(&privileged); err != nil {
		return nil, nil, err
	}
	if len(privileged) == 0 {
		return labels, nil, nil
	}

	// label -> the organizations whose privileged runners hold it.
	reserved := make(map[string]container.Set[int64])
	for _, p := range privileged {
		for _, l := range p.AgentLabels {
			if reserved[l] == nil {
				reserved[l] = make(container.Set[int64])
			}
			reserved[l].Add(p.OwnerID)
		}
	}

	keep = make([]string, 0, len(labels))
	for _, l := range labels {
		owners, isReserved := reserved[l]
		if isReserved && !(runner.PrivilegedCompute && owners.Contains(runner.OwnerID)) {
			dropped = append(dropped, l)
			continue
		}
		keep = append(keep, l)
	}
	return keep, dropped, nil
}

// reservedPrivilegedLabels lists the labels this runner may not answer for:
// those held by a privileged runner it is not entitled to stand in for.
//
// Empty for a privileged runner of the owning organization, which is the one
// that should be taking the work.
func reservedPrivilegedLabels(ctx context.Context, runner *ActionRunner) (container.Set[string], error) {
	var privileged []*ActionRunner
	if err := db.GetEngine(ctx).
		Where(builder.Eq{"privileged_compute": true}).
		And(builder.Neq{"id": runner.ID}).
		Find(&privileged); err != nil {
		return nil, err
	}
	out := make(container.Set[string])
	for _, p := range privileged {
		if runner.PrivilegedCompute && p.OwnerID == runner.OwnerID {
			continue
		}
		out.AddMultiple(p.AgentLabels...)
	}
	return out, nil
}

func claimsReservedLabel(reserved container.Set[string], runsOn []string) bool {
	for _, label := range runsOn {
		if reserved.Contains(label) {
			return true
		}
	}
	return false
}
