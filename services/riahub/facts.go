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
	"strconv"
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
	// diff-tree, not diff. A repository on the server is bare, and `git diff`
	// given a single commit compares it against a work tree, so the form this
	// needs for a new branch died with "fatal: this operation must be run in a
	// work tree" and the push that created the branch reported nothing at all.
	// diff-tree is the plumbing for comparing trees and never looks for one.
	//
	// -r to recurse, or only the top-level entry of a changed directory is
	// named. --no-commit-id because the single-commit form otherwise writes the
	// commit's own id ahead of the first record, where the parser would read it
	// as a status field.
	if isEmptyCommit(oldCommitID) {
		return wholeTree(ctx, repo, newCommitID)
	}
	cmd := gitcmd.NewCommand("diff-tree", "-r", "--no-commit-id", "--name-status", "--no-renames", "-z").
		AddDynamicArguments(oldCommitID, newCommitID)
	stdout, _, runErr := cmd.WithRepo(repo).RunStdString(ctx)
	if runErr != nil {
		return nil, runErr
	}
	return parseNameStatus(stdout), nil
}

// wholeTree lists everything a ref carries, as additions.
//
// What a push with no old commit means: this ref is new here, and nothing
// has been said about any path on it. `diff-tree --root <tip>` is not that
// -- it only diffs against the empty tree when the tip is a root commit, and
// against the tip's parent whenever it has one, so a first push carrying
// three commits reported what the third changed and the first two were never
// catalogued at all.
//
// ls-tree rather than a diff against the empty tree object, whose id depends
// on whether the repository hashes with SHA-1 or SHA-256.
func wholeTree(ctx context.Context, repo gitrepo.RepositoryFacade, commitID string) ([]PathFact, error) {
	stdout, _, runErr := gitcmd.NewCommand("ls-tree", "-r", "-z", "--name-only").
		AddDynamicArguments(commitID).
		WithRepo(repo).
		RunStdString(ctx)
	if runErr != nil {
		return nil, runErr
	}
	paths := strings.Split(stdout, "\x00")
	facts := make([]PathFact, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		facts = append(facts, PathFact{Path: path, Change: Added})
	}
	return facts, nil
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
	for _, i := range pointerSized(ctx, repo, commitID, facts) {
		pointer, ok := readPointer(ctx, repo, commitID, facts[i].Path)
		if !ok {
			continue
		}
		facts[i].IsLFS = true
		facts[i].LfsOid = pointer.Oid
		facts[i].LfsSize = pointer.Size
	}
}

// pointerSized returns the indexes of the present paths whose blob is small
// enough to be a pointer, asking git for every size in one invocation.
//
// The size has to be known before the blob is read. Reading first and
// measuring after means every binary a push adds is pulled through this
// process to discover it is not a pointer, so a single recording committed
// without LFS -- exactly the case the LFS advisory exists to catch -- is read
// into Gitea's memory whole.
func pointerSized(ctx context.Context, repo gitrepo.RepositoryFacade, commitID string, facts []PathFact) []int {
	present := make([]int, 0, len(facts))
	query := make([]byte, 0, len(facts)*64)
	for i := range facts {
		// A removed path has nothing to read at this commit.
		if facts[i].Change == Removed {
			continue
		}
		// The query is newline-delimited, so a path holding one would be read
		// as two and every record after it would be matched to the wrong
		// path. `--batch-check -z` is the answer and arrived in git 2.47,
		// which is newer than this fork requires. Such a path is catalogued
		// by name like any other; only its LFS facts are lost.
		if strings.ContainsRune(facts[i].Path, '\n') {
			continue
		}
		present = append(present, i)
		query = append(query, commitID...)
		query = append(query, ':')
		query = append(query, facts[i].Path...)
		query = append(query, '\n')
	}
	if len(present) == 0 {
		return nil
	}

	stdout, _, runErr := gitcmd.NewCommand("cat-file", "--batch-check").
		WithStdinBytes(query).
		WithRepo(repo).
		RunStdString(ctx)
	if runErr != nil {
		// No LFS facts for this push rather than an unbounded read: the API
		// still catalogs every path by name, which is the common case anyway.
		log.Debug("riahub: could not size the paths changed at %s: %v", commitID, runErr)
		return nil
	}

	// One record per query, in order, so they are matched by position: a
	// missing object answers with the query echoed back, which holds the
	// path and cannot be parsed as a field.
	records := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	small := make([]int, 0, len(present))
	for at, record := range records {
		if at >= len(present) {
			break
		}
		fields := strings.Fields(record)
		if len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size > pointerCeiling {
			continue
		}
		small = append(small, present[at])
	}
	return small
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
