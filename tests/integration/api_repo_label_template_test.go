// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"

	auth_model "gitea.dev/models/auth"
	api "gitea.dev/modules/structs"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
)

// A repository asked for with a label template this instance does not have is
// the request's mistake: 422 and the reason, not a 500. CreateUserRepo moved
// to APIErrorAuto in #40095 and dropped the explicit check it used to make.
func TestAPICreateRepoWithUnknownLabelTemplate(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
	resp := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/user/repos", &api.CreateRepoOption{
		Name:        "unknown-label-template",
		IssueLabels: "no-such-template",
	}).AddTokenAuth(token), http.StatusUnprocessableEntity)
	assert.Contains(t, resp.Body.String(), "no-such-template")
}
