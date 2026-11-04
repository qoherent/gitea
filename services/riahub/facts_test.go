// Copyright 2026 The RIA Hub Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package riahub

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseNameStatusReadsEachKind(t *testing.T) {
	facts := parseNameStatus("A\x00data/one.npy\x00M\x00README.md\x00D\x00old.sigmf-data\x00")

	assert.Equal(t, []PathFact{
		{Path: "data/one.npy", Change: Added},
		{Path: "README.md", Change: Modified},
		{Path: "old.sigmf-data", Change: Removed},
	}, facts)
}

func TestParseNameStatusKeepsPathsWithSpacesAndQuotes(t *testing.T) {
	// `-z` is why: without it git quotes and escapes these, and a path that
	// came back mangled would catalog a file nobody can open.
	facts := parseNameStatus("A\x00my data/a b \"c\".npy\x00")

	assert.Equal(t, []PathFact{{Path: `my data/a b "c".npy`, Change: Added}}, facts)
}

func TestParseNameStatusIgnoresWhatItCannotName(t *testing.T) {
	// U is a conflict and cannot reach a pushed commit; anything unknown is
	// not ours to guess at. Neither may become an addition by default.
	facts := parseNameStatus("U\x00conflicted.npy\x00X\x00strange.npy\x00A\x00real.npy\x00")

	assert.Equal(t, []PathFact{{Path: "real.npy", Change: Added}}, facts)
}

func TestParseNameStatusToleratesATrailingNul(t *testing.T) {
	assert.Empty(t, parseNameStatus(""))
	assert.Empty(t, parseNameStatus("\x00"))
}

func TestAnAllZeroCommitIsEmpty(t *testing.T) {
	// What git passes for "there was no branch here before". Read wrongly,
	// a push that creates a branch diffs against a commit that does not
	// exist and reports nothing -- so the first push of a repository, which
	// is the one that matters most, catalogs nothing.
	assert.True(t, isEmptyCommit("0000000000000000000000000000000000000000"))
	assert.True(t, isEmptyCommit(""))
	assert.False(t, isEmptyCommit("1b86a7f9fa0000000000000000000000000000000"))
}
