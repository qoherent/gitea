// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	"gitea.dev/models/db"
	organization_model "gitea.dev/models/organization"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixture chain these tests stand on: organization 3 owns repository 3, which
// has the Actions unit; team 2 belongs to organization 3 and contains user 2;
// team 7 belongs to the same organization and does not.
const (
	privOrgID  = 3
	privRepoID = 3
	privTeamID = 2
	grantedUID = 2
)

// waitingJobFor inserts a run triggered by triggerUID and one waiting job on
// it, and returns the job.
func waitingJobFor(t *testing.T, triggerUID int64, runsOn []string, index int64) *ActionRunJob {
	t.Helper()
	run := &ActionRun{
		Title:         "privileged-gate-run",
		RepoID:        privRepoID,
		OwnerID:       privOrgID,
		WorkflowID:    "test.yaml",
		Index:         index,
		TriggerUserID: triggerUID,
		Ref:           "refs/heads/main",
		CommitSHA:     "c2d72f548424103f01ee1dc02889c1e2bff816b0",
		Event:         "push",
		TriggerEvent:  "push",
		Status:        StatusWaiting,
	}
	require.NoError(t, db.Insert(t.Context(), run))

	job := &ActionRunJob{
		RunID:     run.ID,
		RepoID:    run.RepoID,
		OwnerID:   run.OwnerID,
		CommitSHA: run.CommitSHA,
		Name:      "gate-job",
		Attempt:   1,
		JobID:     "gate-job",
		Status:    StatusWaiting,
		RunsOn:    runsOn,
		// Claiming parses this; a job without it fails at claim time rather
		// than at the gate, which would hide whichever outcome we are testing.
		WorkflowPayload: []byte("on: push\njobs:\n  gate-job:\n    runs-on: " + runsOn[0] + "\n    steps:\n      - run: echo hi\n"),
	}
	require.NoError(t, db.Insert(t.Context(), job))
	return job
}

func insertRunner(t *testing.T, uuid string, ownerID int64, privileged bool, labels ...string) *ActionRunner {
	t.Helper()
	runner := &ActionRunner{
		UUID:              uuid,
		Name:              uuid,
		OwnerID:           ownerID,
		AgentLabels:       labels,
		PrivilegedCompute: privileged,
	}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(t.Context(), runner))
	return runner
}

func grantTeam(t *testing.T, teamID int64) {
	t.Helper()
	_, err := db.GetEngine(t.Context()).
		Table("team").Where("id = ?", teamID).
		Update(map[string]any{"org_privileged_compute": true})
	require.NoError(t, err)
}

// Without the grant the job is not merely rejected -- it is never selected,
// because the check is a condition on the query. Write access to one
// repository of the organization would otherwise carry code execution on the
// organization's own hardware.
func TestPrivilegedRunnerIsGivenNothingWithoutTheGrant(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	waitingJobFor(t, grantedUID, []string{"gpu-box"}, 9910)
	runner := insertRunner(t, "priv-nogrant", privOrgID, true, "gpu-box")

	granted, err := organization_model.OrgPrivilegedCompute(t.Context(), privOrgID, grantedUID)
	require.NoError(t, err)
	require.False(t, granted, "the fixture must start without the grant or this proves nothing")

	task, ok, err := CreateTaskForRunner(t.Context(), runner)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, task)
}

// The same job, the same runner, once a team the user belongs to carries the
// flag. This is the pair that shows the gate is the grant and not something
// else about the fixture.
func TestPrivilegedRunnerIsGivenTheJobOnceTheTeamIsGranted(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	job := waitingJobFor(t, grantedUID, []string{"gpu-box"}, 9911)
	runner := insertRunner(t, "priv-granted", privOrgID, true, "gpu-box")
	grantTeam(t, privTeamID)

	granted, err := organization_model.OrgPrivilegedCompute(t.Context(), privOrgID, grantedUID)
	require.NoError(t, err)
	require.True(t, granted)

	task, ok, err := CreateTaskForRunner(t.Context(), runner)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, task)

	claimed := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: job.ID})
	assert.Equal(t, StatusRunning, claimed.Status)
	assert.Equal(t, task.ID, claimed.TaskID)
}

// The grant is per-organization, so a privileged runner that belongs to no
// organization has nothing to check against. The edit form refuses to create
// this state; dispatch fails closed in case a row arrives another way.
func TestPrivilegedRunnerWithNoOrganizationIsGivenNothing(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	waitingJobFor(t, grantedUID, []string{"gpu-box"}, 9912)
	grantTeam(t, privTeamID)
	runner := insertRunner(t, "priv-noorg", 0, true, "gpu-box")

	task, ok, err := CreateTaskForRunner(t.Context(), runner)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, task)
}

// The reservation, from the other side. An unprivileged runner carrying a
// privileged runner's label must not be handed that work, however the labels
// match -- otherwise anyone able to register an organization runner could
// intercept it.
func TestUnprivilegedRunnerCannotTakeAReservedLabel(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	job := waitingJobFor(t, grantedUID, []string{"gpu-box"}, 9913)
	grantTeam(t, privTeamID)
	insertRunner(t, "the-real-gpu-box", privOrgID, true, "gpu-box")
	impostor := insertRunner(t, "impostor", privOrgID, false, "gpu-box")

	task, ok, err := CreateTaskForRunner(t.Context(), impostor)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, task)

	still := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: job.ID})
	assert.Equal(t, StatusWaiting, still.Status)
}

// The owner team is given every other organization-wide permission
// automatically. This one it is not: privileged compute runs on hardware the
// instance owns, so it is always a separate decision. A regression here would
// silently grant every organization owner on the instance.
func TestTheOwnerTeamIsNotGrantedByDefault(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	granted, err := organization_model.OrgPrivilegedCompute(t.Context(), privOrgID, grantedUID)
	require.NoError(t, err)
	assert.False(t, granted, "user 2 owns organization 3 and must not hold the grant unasked")
}

// Membership is what the grant follows, so leaving the team takes it away.
// The flag itself is touched rarely; team membership is how access is
// ordinarily handed out, which is the case worth pinning.
func TestTheGrantFollowsTeamMembership(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	grantTeam(t, privTeamID)

	granted, err := organization_model.OrgPrivilegedCompute(t.Context(), privOrgID, grantedUID)
	require.NoError(t, err)
	require.True(t, granted)

	_, err = db.GetEngine(t.Context()).
		Where("team_id = ? AND uid = ?", privTeamID, grantedUID).
		Delete(&organization_model.TeamUser{})
	require.NoError(t, err)

	granted, err = organization_model.OrgPrivilegedCompute(t.Context(), privOrgID, grantedUID)
	require.NoError(t, err)
	assert.False(t, granted, "the grant must not survive leaving the team that carried it")
}

// A grant in one organization says nothing about another. The runner's owning
// organization is what the dispatch check joins on, so a cross-organization
// leak here would hand one organization's hardware to another's members.
func TestTheGrantDoesNotCrossOrganizations(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	grantTeam(t, privTeamID)

	// Organization 6 is a different organization in the fixtures.
	granted, err := organization_model.OrgPrivilegedCompute(t.Context(), 6, grantedUID)
	require.NoError(t, err)
	assert.False(t, granted)
}

// A re-run is attributed to whoever asked for it, and the gate must read that
// rather than the run's original trigger.
//
// Upstream 1.27 records the rerunner on a new ActionRunAttempt and leaves
// `action_run.trigger_user_id` as the person who first caused the run. The
// gate subqueries `action_run`, so an ungranted person re-running a granted
// colleague's job is admitted on the colleague's grant -- which is a way onto
// the hardware for somebody who was never given it, needing only the ability
// to press a button on a run they can see.
func TestARerunIsJudgedOnWhoAskedForIt(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	grantTeam(t, privTeamID)

	runner := insertRunner(t, "11111111-2222-3333-4444-555555555555", privOrgID, true, "gpu")

	// The run was triggered by the granted user, so the run row says granted.
	job := waitingJobFor(t, grantedUID, []string{"gpu"}, 900)

	// The latest attempt was asked for by a member of the organization who
	// holds no grant: user 15 is in team 7, which carries nothing. User 4 is
	// in the granted team 2 and would prove nothing here.
	ungranted := int64(15)
	held, err := organization_model.OrgPrivilegedCompute(t.Context(), privOrgID, ungranted)
	require.NoError(t, err)
	require.False(t, held, "user 4 is expected to hold no grant in this organization")

	attempt := &ActionRunAttempt{
		RepoID:        privRepoID,
		RunID:         job.RunID,
		Attempt:       2,
		TriggerUserID: ungranted,
		Status:        StatusWaiting,
	}
	require.NoError(t, db.Insert(t.Context(), attempt))

	// Where rather than ID: xorm's ID() resolves a primary key against a bean
	// type, and Table() alone gives it none.
	_, err = db.GetEngine(t.Context()).Table("action_run_job").Where("id = ?", job.ID).
		Update(map[string]any{"run_attempt_id": attempt.ID})
	require.NoError(t, err)

	got, ok, err := CreateTaskForRunner(t.Context(), runner)
	require.NoError(t, err)
	assert.False(t, ok, "a re-run asked for by somebody without the grant reached privileged hardware")
	assert.Nil(t, got)
}
