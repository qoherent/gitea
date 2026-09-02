// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git_test

import (
	"bytes"
	"testing"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/lfs"

	"github.com/stretchr/testify/assert"
)

func TestLinkLFSObject(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	ctx := t.Context()
	repo, err := repo_model.GetRepositoryByOwnerAndName(ctx, "user2", "repo1")
	assert.NoError(t, err)

	pointer, err := lfs.GeneratePointer(bytes.NewReader([]byte("gitea-lfs-link-test")))
	assert.NoError(t, err)

	// First call links the object into the repo.
	assert.NoError(t, git_model.LinkLFSObject(ctx, repo, pointer.Oid, pointer.Size))
	meta, err := git_model.GetLFSMetaObjectByOid(ctx, repo.ID, pointer.Oid)
	assert.NoError(t, err)
	assert.EqualValues(t, pointer.Size, meta.Size)

	// Linking the same object again is a no-op, not a duplicate-key error.
	assert.NoError(t, git_model.LinkLFSObject(ctx, repo, pointer.Oid, pointer.Size))
	count, err := db.GetEngine(ctx).Count(&git_model.LFSMetaObject{Pointer: lfs.Pointer{Oid: pointer.Oid}, RepositoryID: repo.ID})
	assert.NoError(t, err)
	assert.EqualValues(t, 1, count)
}
