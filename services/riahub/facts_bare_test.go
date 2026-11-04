// Copyright 2026 The RIA Hub Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package riahub

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitea.dev/modules/git"
	"gitea.dev/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A repository on the server is bare, and nothing that only parses git's
// output can tell you whether the command would have run against one. The
// first version of changedPaths used `git diff`, which passed every parser
// test in this package and then died on the first real push with "fatal: this
// operation must be run in a work tree" -- so these tests run git.

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "riahub-git-home")
	if err != nil {
		panic(err)
	}
	setting.Git.HomePath = home
	if err := git.InitSimple(); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

// bareRepo is the smallest thing gitcmd will run against: an absolute path is
// used as-is, so no repository root has to be configured.
type bareRepo string

func (r bareRepo) GitRepoManagedID() string { return string(r) }
func (r bareRepo) GitRepoLocation() string  { return string(r) }
func (r bareRepo) LogString() string        { return string(r) }

const samplePointer = "version https://git-lfs.github.com/spec/v1\n" +
	"oid sha256:4d7a214614ab2935c943f9e0ff69d22eadbb8f32b1258daaa5e2ca24d17e2393\n" +
	"size 12345\n"

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

// pushedRepo builds a bare repository with two commits and returns it with
// both commit ids, the way a server holds them after two pushes.
func pushedRepo(t *testing.T) (bareRepo, string, string) {
	t.Helper()
	work, bare := t.TempDir(), filepath.Join(t.TempDir(), "repo.git")

	run(t, work, "init", "-q", "-b", "main")
	write(t, work, "data/one.npy", samplePointer)
	write(t, work, "notes/README.md", "hello\n")
	write(t, work, "old/gone.txt", "bye\n")
	run(t, work, "add", "-A")
	run(t, work, "commit", "-q", "-m", "first")
	first := run(t, work, "rev-parse", "HEAD")

	write(t, work, "notes/README.md", "hello again\n")
	write(t, work, "models/net.py", "import torch\n")
	require.NoError(t, os.Remove(filepath.Join(work, "old/gone.txt")))
	run(t, work, "add", "-A")
	run(t, work, "commit", "-q", "-m", "second")
	second := run(t, work, "rev-parse", "HEAD")

	run(t, work, "clone", "--bare", "-q", work, bare)
	return bareRepo(bare), first, second
}

func TestChangedPathsReadsTheFirstCommitOfABareRepository(t *testing.T) {
	repo, first, _ := pushedRepo(t)

	facts, err := changedPaths(context.Background(), repo, "", first)

	require.NoError(t, err)
	assert.ElementsMatch(t, []PathFact{
		{Path: "data/one.npy", Change: Added},
		{Path: "notes/README.md", Change: Added},
		{Path: "old/gone.txt", Change: Added},
	}, facts)
}

func TestChangedPathsReadsACommitRangeOfABareRepository(t *testing.T) {
	repo, first, second := pushedRepo(t)

	facts, err := changedPaths(context.Background(), repo, first, second)

	require.NoError(t, err)
	assert.ElementsMatch(t, []PathFact{
		{Path: "notes/README.md", Change: Modified},
		{Path: "models/net.py", Change: Added},
		{Path: "old/gone.txt", Change: Removed},
	}, facts)
}

func TestChangedPathsNamesFilesNotTheDirectoriesHoldingThem(t *testing.T) {
	// -r. Without it a changed directory is reported as one entry, and every
	// asset inside it is invisible to the catalog.
	repo, first, _ := pushedRepo(t)

	facts, err := changedPaths(context.Background(), repo, "", first)

	require.NoError(t, err)
	for _, fact := range facts {
		assert.Contains(t, fact.Path, "/", "expected a file path, got %q", fact.Path)
		assert.NotEqual(t, "data", fact.Path)
	}
}

func TestResolveLFSTellsAPointerFromAnOrdinaryFile(t *testing.T) {
	repo, first, _ := pushedRepo(t)
	facts, err := changedPaths(context.Background(), repo, "", first)
	require.NoError(t, err)

	resolveLFS(context.Background(), repo, first, facts)

	byPath := map[string]PathFact{}
	for _, fact := range facts {
		byPath[fact.Path] = fact
	}
	assert.True(t, byPath["data/one.npy"].IsLFS)
	assert.Equal(t, "4d7a214614ab2935c943f9e0ff69d22eadbb8f32b1258daaa5e2ca24d17e2393", byPath["data/one.npy"].LfsOid)
	assert.Equal(t, int64(12345), byPath["data/one.npy"].LfsSize)
	assert.False(t, byPath["notes/README.md"].IsLFS)
	assert.Empty(t, byPath["notes/README.md"].LfsOid)
}

func TestResolveLFSLeavesARemovedPathAlone(t *testing.T) {
	// There is nothing to read at the new commit, and asking would cost a
	// failed git invocation per removed file.
	repo, first, second := pushedRepo(t)
	facts, err := changedPaths(context.Background(), repo, first, second)
	require.NoError(t, err)

	resolveLFS(context.Background(), repo, second, facts)

	for _, fact := range facts {
		if fact.Change == Removed {
			assert.False(t, fact.IsLFS)
			assert.Empty(t, fact.LfsOid)
		}
	}
}

func TestPointerSizedLeavesOutWhatIsTooBigToBeAPointer(t *testing.T) {
	// The size has to come from git, not from reading the blob: this is the
	// bound that keeps a recording committed without LFS out of Gitea's
	// memory, and the only way to see it work is to ask for a big one.
	work, bare := t.TempDir(), filepath.Join(t.TempDir(), "repo.git")
	run(t, work, "init", "-q", "-b", "main")
	write(t, work, "data/pointer.npy", samplePointer)
	write(t, work, "data/raw.npy", strings.Repeat("x", pointerCeiling+1))
	run(t, work, "add", "-A")
	run(t, work, "commit", "-q", "-m", "first")
	head := run(t, work, "rev-parse", "HEAD")
	run(t, work, "clone", "--bare", "-q", work, bare)
	repo := bareRepo(bare)

	facts, err := changedPaths(context.Background(), repo, "", head)
	require.NoError(t, err)
	sized := pointerSized(context.Background(), repo, head, facts)

	kept := make([]string, 0, len(sized))
	for _, i := range sized {
		kept = append(kept, facts[i].Path)
	}
	assert.Equal(t, []string{"data/pointer.npy"}, kept)
}

func TestPointerSizedAsksForNothingWhenEveryPathIsRemoved(t *testing.T) {
	repo, _, head := pushedRepo(t)
	facts := []PathFact{{Path: "old/gone.txt", Change: Removed}}

	assert.Empty(t, pointerSized(context.Background(), repo, head, facts))
}

func TestChangedPathsReportsEveryCommitOfAFirstPushNotJustTheTip(t *testing.T) {
	// A branch is pushed whole, not commit by commit, and the hook is given
	// one range for the lot. `diff-tree --root <tip>` diffs the tip against
	// its parent whenever it has one, so a first push carrying three commits
	// reported only what the third changed and the rest were never
	// catalogued. The earlier test passed because its first push was one
	// commit, where tip and root are the same thing.
	work, bare := t.TempDir(), filepath.Join(t.TempDir(), "repo.git")
	run(t, work, "init", "-q", "-b", "main")
	write(t, work, "first/early.npy", samplePointer)
	run(t, work, "add", "-A")
	run(t, work, "commit", "-q", "-m", "one")
	write(t, work, "second/middle.npy", "not a pointer\n")
	run(t, work, "add", "-A")
	run(t, work, "commit", "-q", "-m", "two")
	write(t, work, "third/late.py", "import torch\n")
	run(t, work, "add", "-A")
	run(t, work, "commit", "-q", "-m", "three")
	tip := run(t, work, "rev-parse", "HEAD")
	run(t, work, "clone", "--bare", "-q", work, bare)

	facts, err := changedPaths(context.Background(), bareRepo(bare), "", tip)

	require.NoError(t, err)
	named := make([]string, 0, len(facts))
	for _, fact := range facts {
		assert.Equal(t, Added, fact.Change, "everything a new branch carries is an addition")
		named = append(named, fact.Path)
	}
	assert.ElementsMatch(t, []string{"first/early.npy", "second/middle.npy", "third/late.py"}, named)
}

func TestOutcomeOfTellsARefusalFromAPause(t *testing.T) {
	// A blanket "4xx means dropped" loses a report to a rate limiter or a
	// proxy's timeout, and the catalogue is then permanently missing what
	// that push changed with nothing anywhere saying so.
	for status, want := range map[int]outcome{
		200: delivered,
		202: delivered,
		204: delivered,
		400: refused,
		401: refused,
		403: refused,
		404: refused,
		413: refused,
		422: refused,
		408: retryable,
		429: retryable,
		500: retryable,
		502: retryable,
		503: retryable,
	} {
		assert.Equal(t, want, outcomeOf(status), "status %d", status)
	}
}
