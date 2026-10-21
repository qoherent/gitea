// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"time"

	"gitea.dev/models/db"
	organization_model "gitea.dev/models/organization"
	"gitea.dev/modules/log"
	"gitea.dev/modules/timeutil"

	"xorm.io/builder"
)

// ActionComputeUsage is how much runner time an owner has used in a period.
//
// Storage already has a quota and compute had none: nothing counted runner
// time at all, so "this organization has used its share" was a sentence the
// instance could not say. ActionTask carries Started and Stopped, so the
// measurement existed and had nowhere to go.
//
// Keyed by calendar month, so the window resets without anything having to run
// on a schedule. A period nobody used has no row.
type ActionComputeUsage struct {
	ID      int64 `xorm:"pk autoincr"`
	OwnerID int64 `xorm:"UNIQUE(owner_period) NOT NULL"`
	// "2006-01", UTC. A string rather than a pair of timestamps because every
	// question asked of it is "this month" or "that month", never a range.
	Period      string             `xorm:"UNIQUE(owner_period) NOT NULL"`
	Seconds     int64              `xorm:"NOT NULL DEFAULT 0"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

// ActionComputeLimit is the ceiling an owner's runner time is measured
// against, in seconds per month.
//
// Absent means unlimited, and that is the default for everyone: this changes
// nothing until a site admin sets a limit on somebody, exactly as the two
// privileged-compute flags change nothing until one is set. An instance-wide
// default was considered and rejected -- it would make upgrading to this
// version start refusing work on deployments that never asked for a quota.
type ActionComputeLimit struct {
	ID          int64              `xorm:"pk autoincr"`
	OwnerID     int64              `xorm:"UNIQUE NOT NULL"`
	Seconds     int64              `xorm:"NOT NULL DEFAULT 0"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

func init() {
	db.RegisterModel(new(ActionComputeUsage))
	db.RegisterModel(new(ActionComputeLimit))
}

// CurrentPeriod names the accounting window a moment falls in.
func CurrentPeriod(at time.Time) string {
	return at.UTC().Format("2006-01")
}

// RecordComputeUsage adds a finished task's runner time to its owner's total.
//
// Called where a task stops. Best-effort by design: the accounting must not be
// able to fail the task that was just completed, because a job that ran and
// then reports a failure is worse than a job whose cost was not counted. The
// caller logs; it does not propagate.
//
// A task with no owner (both scopes zero cannot happen for a job, but a
// malformed row can) or no measurable duration is skipped rather than written
// as zero, so the row's absence keeps meaning "nothing ran".
func RecordComputeUsage(ctx context.Context, task *ActionTask) error {
	if task == nil || task.OwnerID == 0 {
		return nil
	}
	if task.Started <= 0 || task.Stopped <= task.Started {
		return nil
	}
	seconds := int64(task.Stopped - task.Started)
	period := CurrentPeriod(task.Stopped.AsTime())

	return db.WithTx(ctx, func(ctx context.Context) error {
		e := db.GetEngine(ctx)
		updated, err := e.Exec(
			"UPDATE action_compute_usage SET seconds = seconds + ?, updated_unix = ? WHERE owner_id = ? AND period = ?",
			seconds, timeutil.TimeStampNow(), task.OwnerID, period,
		)
		if err != nil {
			return err
		}
		if affected, err := updated.RowsAffected(); err == nil && affected > 0 {
			return nil
		}
		// First task of the period for this owner. A concurrent insert loses
		// the unique constraint race, so the retry folds into the update above.
		if err := db.Insert(ctx, &ActionComputeUsage{
			OwnerID: task.OwnerID,
			Period:  period,
			Seconds: seconds,
		}); err != nil {
			_, retry := e.Exec(
				"UPDATE action_compute_usage SET seconds = seconds + ?, updated_unix = ? WHERE owner_id = ? AND period = ?",
				seconds, timeutil.TimeStampNow(), task.OwnerID, period,
			)
			return retry
		}
		return nil
	})
}

// ComputeUsedThisPeriod reports an owner's runner seconds in the current month.
func ComputeUsedThisPeriod(ctx context.Context, ownerID int64) (int64, error) {
	usage := new(ActionComputeUsage)
	has, err := db.GetEngine(ctx).
		Where(builder.Eq{"owner_id": ownerID, "period": CurrentPeriod(time.Now())}).
		Get(usage)
	if err != nil || !has {
		return 0, err
	}
	return usage.Seconds, nil
}

// ComputeLimitFor reports an owner's ceiling in seconds, zero meaning none.
func ComputeLimitFor(ctx context.Context, ownerID int64) (int64, error) {
	limit := new(ActionComputeLimit)
	has, err := db.GetEngine(ctx).Where(builder.Eq{"owner_id": ownerID}).Get(limit)
	if err != nil || !has {
		return 0, err
	}
	return limit.Seconds, nil
}

// SetComputeLimit sets or clears an owner's ceiling. Zero clears it.
func SetComputeLimit(ctx context.Context, ownerID, seconds int64) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		e := db.GetEngine(ctx)
		if seconds <= 0 {
			_, err := e.Where(builder.Eq{"owner_id": ownerID}).Delete(new(ActionComputeLimit))
			return err
		}
		affected, err := e.Where(builder.Eq{"owner_id": ownerID}).
			Cols("seconds").Update(&ActionComputeLimit{Seconds: seconds})
		if err != nil {
			return err
		}
		if affected > 0 {
			return nil
		}
		return db.Insert(ctx, &ActionComputeLimit{OwnerID: ownerID, Seconds: seconds})
	})
}

// ComputeExhausted reports whether an owner has spent its allowance.
//
// An organization holding privileged compute is exempt: the grant is the
// instance saying this organization's work runs on the instance's own
// hardware, and metering what you have already said is unmetered would be the
// instance disagreeing with itself.
//
// Failing to read the quota answers "not exhausted". A job that cannot run
// because the accounting is unavailable is an outage caused by bookkeeping,
// and the quota exists to apportion a shared resource rather than to keep
// anybody out.
func ComputeExhausted(ctx context.Context, ownerID int64) bool {
	if ownerID == 0 {
		return false
	}
	limit, err := ComputeLimitFor(ctx, ownerID)
	if err != nil {
		log.Error("actions: could not read the compute limit for owner %d: %v", ownerID, err)
		return false
	}
	if limit <= 0 {
		return false
	}
	// Asked after the limit, so an unmetered owner costs no second query.
	if exempt, err := organization_model.HasPrivilegedCompute(ctx, ownerID); err != nil {
		log.Error("actions: could not read the privileged grant for owner %d: %v", ownerID, err)
		return false
	} else if exempt {
		return false
	}
	used, err := ComputeUsedThisPeriod(ctx, ownerID)
	if err != nil {
		log.Error("actions: could not read the compute usage for owner %d: %v", ownerID, err)
		return false
	}
	return used >= limit
}
