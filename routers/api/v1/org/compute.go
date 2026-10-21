// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"net/http"
	"time"

	actions_model "gitea.dev/models/actions"
	organization_model "gitea.dev/models/organization"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"
)

// GetComputeAllowance reports how much runner time an organization has used
// this month, what its ceiling is, and whether it is metered at all.
//
// Readable by an organization owner, because an owner whose jobs have stopped
// being dispatched should be able to find out why without asking an
// administrator. Settable only by an administrator, below.
func GetComputeAllowance(ctx *context.APIContext) {
	// swagger:operation GET /orgs/{org}/actions/compute organization orgGetComputeAllowance
	// ---
	// summary: Get an organization's compute allowance and usage
	// produces:
	// - application/json
	// parameters:
	// - name: org
	//   in: path
	//   description: name of the organization
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/ComputeAllowance"
	//   "404":
	//     "$ref": "#/responses/notFound"
	orgID := ctx.Org.Organization.ID

	used, err := actions_model.ComputeUsedThisPeriod(ctx, orgID)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	limit, err := actions_model.ComputeLimitFor(ctx, orgID)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	exempt, err := organization_model.HasPrivilegedCompute(ctx, orgID)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}

	ctx.JSON(http.StatusOK, &api.ComputeAllowance{
		Period:       actions_model.CurrentPeriod(time.Now()),
		UsedSeconds:  used,
		LimitSeconds: limit,
		Unlimited:    limit <= 0 || exempt,
		Exempt:       exempt,
	})
}

// SetComputeAllowance sets or clears an organization's ceiling.
//
// A site admin's decision, like every other decision in this area. An
// organization owner may read their allowance and may not raise it, for the
// same reason they may not mark a runner privileged: the resource being
// apportioned is the instance's, not the organization's.
func SetComputeAllowance(ctx *context.APIContext) {
	// swagger:operation PUT /orgs/{org}/actions/compute organization orgSetComputeAllowance
	// ---
	// summary: Set an organization's compute allowance
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: org
	//   in: path
	//   description: name of the organization
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/SetComputeAllowanceOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/ComputeAllowance"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "422":
	//     "$ref": "#/responses/validationError"
	if !ctx.Doer.IsAdmin {
		ctx.APIError(http.StatusForbidden, "a compute allowance is a site-admin decision")
		return
	}

	form := web.GetForm(ctx).(*api.SetComputeAllowanceOption)
	if form.LimitSeconds < 0 {
		ctx.APIError(http.StatusUnprocessableEntity, "a limit cannot be negative")
		return
	}
	if err := actions_model.SetComputeLimit(ctx, ctx.Org.Organization.ID, form.LimitSeconds); err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	GetComputeAllowance(ctx)
}
