// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package shared

import (
	"errors"
	"net/http"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	organization_model "gitea.dev/models/organization"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/util"
	"gitea.dev/modules/web"
	"gitea.dev/routers/api/v1/utils"
	"gitea.dev/services/context"
	"gitea.dev/services/convert"
)

// RegistrationToken is response related to registration token
// swagger:response RegistrationToken
type RegistrationToken struct {
	Token string `json:"token"`
}

// MayRegisterRunner reports whether this Doer may bring a new machine onto the
// instance at the given scope, and writes the refusal if not.
//
// Registering a runner is offering to execute other people's jobs. On an
// instance with open sign-up that was available to anyone: a user-scoped
// runner to any account, an organization-scoped one to any organization they
// could create and own. Neither reached privileged hardware, but both put a
// machine of the registrant's choosing in the path of somebody else's code and
// secrets.
//
// So: site admins anywhere, and organizations the instance has granted
// privileged compute -- which is what "a privileged organization" means, since
// only a site admin can grant it. Everyone else uses the runners the instance
// provides.
func MayRegisterRunner(ctx *context.APIContext, ownerID, repoID int64) bool {
	if ctx.Doer != nil && ctx.Doer.IsAdmin {
		return true
	}
	// A repository-scoped runner belongs to its repository's owner, so the
	// question is the same one asked a segment higher up.
	if repoID != 0 && ctx.Repo != nil && ctx.Repo.Repository != nil {
		ownerID = ctx.Repo.Repository.OwnerID
	}
	if ownerID != 0 {
		privileged, err := organization_model.HasPrivilegedCompute(ctx, ownerID)
		if err != nil {
			ctx.APIErrorInternal(err)
			return false
		}
		if privileged {
			return true
		}
	}
	ctx.APIError(http.StatusForbidden,
		"registering a runner is reserved to site administrators and to organizations granted privileged compute")
	return false
}

func GetRegistrationToken(ctx *context.APIContext, ownerID, repoID int64) {
	if !MayRegisterRunner(ctx, ownerID, repoID) {
		return
	}
	token, err := actions_model.GetLatestRunnerToken(ctx, ownerID, repoID)
	if errors.Is(err, util.ErrNotExist) || (token != nil && !token.IsActive) {
		token, err = actions_model.NewRunnerToken(ctx, ownerID, repoID)
	}
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}

	ctx.JSON(http.StatusOK, RegistrationToken{Token: token.Token})
}

// ListRunners lists runners for api route validated ownerID and repoID
// ownerID == 0 and repoID == 0 means all runners including global runners, does not appear in sql where clause
// ownerID == 0 and repoID != 0 means all runners for the given repo
// ownerID != 0 and repoID == 0 means all runners for the given user/org
// ownerID != 0 and repoID != 0 undefined behavior
// Access rights are checked at the API route level
func ListRunners(ctx *context.APIContext, ownerID, repoID int64) {
	if ownerID != 0 && repoID != 0 {
		setting.PanicInDevOrTesting("ownerID and repoID should not be both set")
	}
	opts := &actions_model.FindRunnerOptions{
		OwnerID:     ownerID,
		RepoID:      repoID,
		ListOptions: utils.GetListOptions(ctx),
	}
	opts.IsDisabled = ctx.FormOptionalBool("disabled")
	// `available=true` widens the listing to every runner that could take this
	// repository's or organization's work, rather than only those registered
	// at that exact scope.
	//
	// Without it a repository listing shows repository-scoped runners alone,
	// while dispatch also considers the owner's and the instance's. A surface
	// asking somebody to choose a `runs-on` label from that listing therefore
	// offers a strict subset of the labels that actually work, and reports the
	// rest as carried by nothing -- which is how a privileged runner, always
	// organization-scoped, became invisible to the form meant to target it.
	opts.WithAvailable = ctx.FormBool("available")
	runners, total, err := db.FindAndCount[actions_model.ActionRunner](ctx, opts)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}

	res := new(api.ActionRunnersResponse)
	res.TotalCount = total

	res.Entries = make([]*api.ActionRunner, len(runners))
	for i, runner := range runners {
		res.Entries[i] = convert.ToActionRunner(ctx, runner)
	}

	ctx.JSON(http.StatusOK, &res)
}

func getRunnerByID(ctx *context.APIContext, ownerID, repoID, runnerID int64) (*actions_model.ActionRunner, bool) {
	if ownerID != 0 && repoID != 0 {
		setting.PanicInDevOrTesting("ownerID and repoID should not be both set")
	}

	runner, err := actions_model.GetRunnerByID(ctx, runnerID)
	if err != nil {
		ctx.APIErrorAuto(err)
		return nil, false
	}

	if !runner.EditableInContext(ownerID, repoID) {
		ctx.APIErrorNotFound("No permission to access this runner")
		return nil, false
	}
	return runner, true
}

// GetRunner get the runner for api route validated ownerID and repoID
// ownerID == 0 and repoID == 0 means any runner including global runners
// ownerID == 0 and repoID != 0 means any runner for the given repo
// ownerID != 0 and repoID == 0 means any runner for the given user/org
// ownerID != 0 and repoID != 0 undefined behavior
// Access rights are checked at the API route level
func GetRunner(ctx *context.APIContext, ownerID, repoID, runnerID int64) {
	if ownerID != 0 && repoID != 0 {
		setting.PanicInDevOrTesting("ownerID and repoID should not be both set")
	}
	runner, ok := getRunnerByID(ctx, ownerID, repoID, runnerID)
	if !ok {
		return
	}
	ctx.JSON(http.StatusOK, convert.ToActionRunner(ctx, runner))
}

// DeleteRunner deletes the runner for api route validated ownerID and repoID
// ownerID == 0 and repoID == 0 means any runner including global runners
// ownerID == 0 and repoID != 0 means any runner for the given repo
// ownerID != 0 and repoID == 0 means any runner for the given user/org
// ownerID != 0 and repoID != 0 undefined behavior
// Access rights are checked at the API route level
func DeleteRunner(ctx *context.APIContext, ownerID, repoID, runnerID int64) {
	runner, ok := getRunnerByID(ctx, ownerID, repoID, runnerID)
	if !ok {
		return
	}

	err := actions_model.DeleteRunner(ctx, runner.ID)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func UpdateRunner(ctx *context.APIContext, ownerID, repoID, runnerID int64) {
	runner, ok := getRunnerByID(ctx, ownerID, repoID, runnerID)
	if !ok {
		return
	}

	form := web.GetForm[*api.EditActionRunnerOption](ctx)
	if form.Disabled == nil && form.PrivilegedCompute == nil {
		ctx.APIError(http.StatusUnprocessableEntity, "[Disabled]: Required")
		return
	}

	if form.Disabled != nil {
		if err := actions_model.SetRunnerDisabled(ctx, runner, *form.Disabled); err != nil {
			ctx.APIErrorInternal(err)
			return
		}
	}

	// Privileged compute is a site-admin decision, as it is on the web form.
	// The caller here has already passed the owner check for this runner's
	// organization, which is not the same thing: an organization owner may say
	// which of their teams reaches privileged hardware, and only an
	// administrator may say that a machine is privileged at all.
	if form.PrivilegedCompute != nil {
		if !ctx.Doer.IsAdmin {
			ctx.APIError(http.StatusForbidden, "privileged compute is a site-admin decision")
			return
		}
		marked := runner.PrivilegedCompute != *form.PrivilegedCompute
		if err := actions_model.SetRunnerPrivilegedCompute(ctx, runner, *form.PrivilegedCompute); err != nil {
			if errors.Is(err, util.ErrInvalidArgument) {
				ctx.APIError(http.StatusUnprocessableEntity, err.Error())
				return
			}
			ctx.APIErrorInternal(err)
			return
		}
		if marked {
		}
	}

	GetRunner(ctx, ownerID, repoID, runnerID)
}
