// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package private

import (
	"io"
	"net/http"

	git_model "gitea.dev/models/git"
	access_model "gitea.dev/models/perm/access"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	lfs_module "gitea.dev/modules/lfs"
	"gitea.dev/modules/private"
	myCtx "gitea.dev/services/context"
)

// LinkLFSObject links an LFS object already present in the shared content
// store into the target repository without transferring its bytes. It is
// internal-token-gated and intended to be called only by RiaHub's trusted
// backend on behalf of an authenticated end user.
func LinkLFSObject(ctx *myCtx.PrivateContext) {
	bs, err := io.ReadAll(ctx.Req.Body)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{Err: err.Error()})
		return
	}
	params := struct {
		SourceOwner string
		SourceRepo  string
		TargetOwner string
		TargetRepo  string
		OID         string
		Size        int64
		UserID      int64
	}{}
	if err = json.Unmarshal(bs, &params); err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{Err: err.Error()})
		return
	}

	// The end user the backend acts on behalf of. Their real Gitea access, not
	// the internal token alone, is what authorizes the link.
	doer, err := user_model.GetUserByID(ctx, params.UserID)
	if err != nil {
		ctx.JSON(http.StatusNotFound, private.Response{Err: "unknown user"})
		return
	}

	sourceRepo := loadRepository(ctx, params.SourceOwner, params.SourceRepo)
	if ctx.Written() {
		return
	}
	targetRepo := loadRepository(ctx, params.TargetOwner, params.TargetRepo)
	if ctx.Written() {
		return
	}

	// Proof of authorization: the source repo must already legitimately own
	// this OID (a previously-established DB association), so a bare hash claim
	// cannot pull content from a repo the user was never granted access to.
	if _, err := git_model.GetLFSMetaObjectByOid(ctx, sourceRepo.ID, params.OID); err != nil {
		ctx.JSON(http.StatusNotFound, private.Response{Err: "source repository does not own this LFS object"})
		return
	}

	// The user must be able to read the source and write the target.
	sourcePerm, err := access_model.GetDoerRepoPermission(ctx, sourceRepo, doer)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{Err: err.Error()})
		return
	}
	if !sourcePerm.CanRead(unit.TypeCode) {
		ctx.JSON(http.StatusForbidden, private.Response{Err: "no read access to source repository"})
		return
	}
	targetPerm, err := access_model.GetDoerRepoPermission(ctx, targetRepo, doer)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{Err: err.Error()})
		return
	}
	if !targetPerm.CanWrite(unit.TypeCode) {
		ctx.JSON(http.StatusForbidden, private.Response{Err: "no write access to target repository"})
		return
	}

	// The claimed OID/size must independently exist in the content store with
	// the correct size, so a fabricated size cannot poison quota accounting or
	// later resolution.
	contentStore := lfs_module.NewContentStore()
	ok, err := contentStore.Verify(lfs_module.Pointer{Oid: params.OID, Size: params.Size})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{Err: err.Error()})
		return
	}
	if !ok {
		ctx.JSON(http.StatusNotFound, private.Response{Err: "LFS object not present in content store"})
		return
	}

	if err := git_model.LinkLFSObject(ctx, targetRepo, params.OID, params.Size); err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{Err: err.Error()})
		return
	}
	ctx.PlainText(http.StatusOK, "success")
}
