// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Storage already had a quota and compute had none: nothing counted runner
// time at all, so "this organization has used its share" was a sentence the
// instance could not say.

func finishedTask(ownerID int64, started, stopped timeutil.TimeStamp) *ActionTask {
	return &ActionTask{OwnerID: ownerID, Started: started, Stopped: stopped}
}

func TestComputeUsageAccumulatesWithinAPeriod(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	now := timeutil.TimeStampNow()

	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(3, now-60, now)))
	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(3, now-30, now)))

	used, err := ComputeUsedThisPeriod(t.Context(), 3)
	require.NoError(t, err)
	assert.EqualValues(t, 90, used, "two tasks on one owner add up")
}

func TestComputeUsageIsKeptPerOwner(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	now := timeutil.TimeStampNow()

	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(3, now-60, now)))

	used, err := ComputeUsedThisPeriod(t.Context(), 6)
	require.NoError(t, err)
	assert.EqualValues(t, 0, used, "one owner's usage is not another's")
}

func TestATaskThatRanNoTimeIsNotWritten(t *testing.T) {
	// Absence of a row keeps meaning "nothing ran". A zero row would make an
	// owner who never started anything indistinguishable from one whose job
	// failed instantly.
	assert.NoError(t, unittest.PrepareTestDatabase())
	now := timeutil.TimeStampNow()

	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(3, 0, now)))
	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(3, now, now)))
	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(0, now-60, now)))

	count, err := db.GetEngine(t.Context()).Count(new(ActionComputeUsage))
	require.NoError(t, err)
	assert.EqualValues(t, 0, count)
}

func TestAPeriodIsACalendarMonthInUTC(t *testing.T) {
	assert.Equal(t, "2026-10", CurrentPeriod(time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)))
	assert.Equal(t, "2026-11", CurrentPeriod(time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)))
	// A period is UTC wherever the instance or the runner happens to be, so a
	// task finishing at 05:00 on the first in UTC+10 belongs to the month that
	// was still running in UTC. Otherwise two runners in different zones would
	// bill the same minute to different months.
	east := time.FixedZone("east", 10*60*60)
	assert.Equal(t, "2026-10", CurrentPeriod(time.Date(2026, 11, 1, 5, 0, 0, 0, east)))
}

func TestNobodyIsLimitedUntilSomebodyIsGivenALimit(t *testing.T) {
	// The feature changes nothing on an instance that never asked for a quota,
	// exactly as the two privileged-compute flags change nothing until one is
	// set. An instance-wide default would start refusing work on upgrade.
	assert.NoError(t, unittest.PrepareTestDatabase())
	now := timeutil.TimeStampNow()
	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(3, now-100000, now)))

	assert.False(t, ComputeExhausted(t.Context(), 3))
}

func TestAnOwnerOverItsLimitIsExhausted(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	now := timeutil.TimeStampNow()
	require.NoError(t, SetComputeLimit(t.Context(), 3, 100))

	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(3, now-99, now)))
	assert.False(t, ComputeExhausted(t.Context(), 3), "under the limit")

	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(3, now-2, now)))
	assert.True(t, ComputeExhausted(t.Context(), 3), "at or past the limit")
}

func TestAGrantedOrganizationIsNotMetered(t *testing.T) {
	// The grant is the instance saying this organization's work runs on the
	// instance's own hardware. Metering what has already been declared
	// unmetered would be the instance disagreeing with itself.
	assert.NoError(t, unittest.PrepareTestDatabase())
	now := timeutil.TimeStampNow()
	require.NoError(t, SetComputeLimit(t.Context(), 3, 10))
	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(3, now-1000, now)))
	require.True(t, ComputeExhausted(t.Context(), 3))

	grantTeam(t, privTeamID)
	assert.False(t, ComputeExhausted(t.Context(), 3), "a privileged organization is exempt")
}

func TestClearingALimitRestoresTheOwner(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	now := timeutil.TimeStampNow()
	require.NoError(t, SetComputeLimit(t.Context(), 3, 10))
	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(3, now-1000, now)))
	require.True(t, ComputeExhausted(t.Context(), 3))

	require.NoError(t, SetComputeLimit(t.Context(), 3, 0))
	limit, err := ComputeLimitFor(t.Context(), 3)
	require.NoError(t, err)
	assert.EqualValues(t, 0, limit)
	assert.False(t, ComputeExhausted(t.Context(), 3))
}

// An owner out of compute has jobs that wait, which is what a quota means --
// and the next owner's job is still handed out, so one exhausted owner does
// not stall the instance.
func TestAnExhaustedOwnersJobIsNotDispatched(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	now := timeutil.TimeStampNow()
	require.NoError(t, SetComputeLimit(t.Context(), privOrgID, 10))
	require.NoError(t, RecordComputeUsage(t.Context(), finishedTask(privOrgID, now-1000, now)))

	runner := insertRunner(t, "99999999-8888-7777-6666-555555555555", privOrgID, false, "ordinary")
	waitingJobFor(t, grantedUID, []string{"ordinary"}, 950)

	task, ok, err := CreateTaskForRunner(t.Context(), runner)
	require.NoError(t, err)
	assert.False(t, ok, "an owner past its compute limit was handed a job")
	assert.Nil(t, task)

	// And it resumes the moment the limit is lifted.
	require.NoError(t, SetComputeLimit(t.Context(), privOrgID, 0))
	task, ok, err = CreateTaskForRunner(t.Context(), runner)
	require.NoError(t, err)
	assert.True(t, ok, "lifting the limit should release the job")
	assert.NotNil(t, task)
}
