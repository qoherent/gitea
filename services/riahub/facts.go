// Copyright 2026 The RIA Hub Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package riahub reports what a push changed to RIA Hub's API.
//
// It reports; it does not classify. What a `.npy` file means, whether it
// belongs in the library, and what badge it earns are all decisions for the
// API, and none of that logic is here -- which is the difference between
// this package and the classifier the pre-refactor fork carried inline, and
// the reason this one does not make an upstream merge harder.
//
// Two facts are gathered here rather than there, because they are cheap on
// this side of the wire and expensive on the other: whether a path is a Git
// LFS pointer, and what object it points at. Reading them here turns one
// API round trip per changed file into none.
package riahub

import (
	"context"
	"strings"

	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/git/gitrepo"
	"gitea.dev/modules/lfs"
	"gitea.dev/modules/log"
)

// Change is what happened to one path in a push.
type Change string

const (
	Added    Change = "added"
	Modified Change = "modified"
	Removed  Change = "removed"
)

// PathFact is one changed path, and what this side knows about it that the
// other side would have to ask for.
type PathFact struct {
	Path   string `json:"path"`
	Change Change `json:"change"`
	// IsLFS is false for a removed path, where there is nothing to read.
	IsLFS bool `json:"is_lfs"`
	// LfsOid and LfsSize are set only when IsLFS. The oid is what the object
	// is stored under; the size is the real file's, not the pointer's.
	LfsOid  string `json:"lfs_oid,omitempty"`
	LfsSize int64  `json:"lfs_size,omitempty"`
}

// changedPaths lists what a push changed, as git reports it.
//
// `--no-renames` on purpose: a rename reported as such would reach the API as
// neither an addition nor a removal, and a catalog row would keep a path that
// no longer exists. Split into a removal and an addition it stays correct
// with no rename handling on either side.
func changedPaths(ctx context.Context, repo gitrepo.RepositoryFacade, oldCommitID, newCommitID string) ([]PathFact, error) {
	cmd := gitcmd.NewCommand("diff", "--name-status", "--no-renames", "-z")
	if isEmptyCommit(oldCommitID) {
		// A new branch: everything it carries is an addition. `--root` makes
		// git diff the first commit against the empty tree rather than
		// refusing, which is what made a push that created a branch report
		// nothing at all.
		cmd.AddArguments("--root")
		cmd.AddDynamicArguments(newCommitID)
	} else {
		cmd.AddDynamicArguments(oldCommitID, newCommitID)
	}
	stdout, _, runErr := cmd.WithRepo(repo).RunStdString(ctx)
	if runErr != nil {
		return nil, runErr
	}
	return parseNameStatus(stdout), nil
}

func isEmptyCommit(id string) bool {
	return id == "" || strings.Trim(id, "0") == ""
}

// parseNameStatus reads `--name-status -z`: a status field and a path field,
// each NUL-terminated, alternating.
func parseNameStatus(out string) []PathFact {
	fields := strings.Split(out, "\x00")
	facts := make([]PathFact, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		status, path := fields[i], fields[i+1]
		if status == "" || path == "" {
			continue
		}
		var change Change
		switch status[0] {
		case 'A':
			change = Added
		case 'M', 'T':
			change = Modified
		case 'D':
			change = Removed
		default:
			// C and R cannot appear (`--no-renames`); U is a conflict, which
			// a pushed commit does not carry. Anything else is not ours to
			// guess at.
			continue
		}
		facts = append(facts, PathFact{Path: path, Change: change})
	}
	return facts
}

// pointerCeiling bounds what is read to decide whether a blob is an LFS
// pointer. A pointer is about 130 bytes and the format caps it well under
// this; anything larger is the file itself, and reading it would mean
// pulling a recording through this process to learn that it is not a
// pointer.
const pointerCeiling = 1024

// resolveLFS fills in the LFS facts for every path still present.
func resolveLFS(ctx context.Context, repo gitrepo.RepositoryFacade, commitID string, facts []PathFact) {
	for i := range facts {
		if facts[i].Change == Removed {
			continue
		}
		pointer, ok := readPointer(ctx, repo, commitID, facts[i].Path)
		if !ok {
			continue
		}
		facts[i].IsLFS = true
		facts[i].LfsOid = pointer.Oid
		facts[i].LfsSize = pointer.Size
	}
}

func readPointer(ctx context.Context, repo gitrepo.RepositoryFacade, commitID, path string) (lfs.Pointer, bool) {
	stdout, _, runErr := gitcmd.NewCommand("cat-file", "blob").
		AddDynamicArguments(commitID + ":" + path).
		WithRepo(repo).
		RunStdBytes(ctx)
	if runErr != nil {
		// A path that cannot be read is reported without LFS facts rather
		// than dropped: the API still catalogs it by name, which is the
		// common case anyway.
		log.Debug("riahub: could not read %s at %s: %v", path, commitID, runErr)
		return lfs.Pointer{}, false
	}
	if len(stdout) > pointerCeiling {
		return lfs.Pointer{}, false
	}
	pointer, err := lfs.ReadPointerFromBuffer(stdout)
	if err != nil {
		return lfs.Pointer{}, false
	}
	return pointer, true
}
