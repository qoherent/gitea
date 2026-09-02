// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"

	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/lfs"

	"xorm.io/builder"
)

// LinkLFSObject associates an LFS object already present in the shared content
// store with targetRepo, without transferring any content bytes. It is a
// per-object counterpart to CopyLFS. Callers are responsible for verifying the
// caller's authorization before calling this function; it performs no
// permission checks itself. Linking an object that is already associated with
// targetRepo is a no-op.
func LinkLFSObject(ctx context.Context, targetRepo *repo_model.Repository, oid string, size int64) error {
	if _, exist, err := db.Get[LFSMetaObject](ctx, builder.Eq{"repository_id": targetRepo.ID, "oid": oid}); err != nil {
		return err
	} else if exist {
		return nil
	}
	return db.Insert(ctx, &LFSMetaObject{
		Pointer:      lfs.Pointer{Oid: oid, Size: size},
		RepositoryID: targetRepo.ID,
	})
}
