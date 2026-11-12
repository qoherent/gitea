// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	api "gitea.dev/modules/structs"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A bot has no password, so it cannot authenticate to make its own tokens, and
// a token-authenticated administrator could not make them either: the user
// token endpoint wants basic auth. The admin UI could; this is the same for a
// client that holds no password.
func TestAPIAdminManagesABotsTokens(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	admin := getUserToken(t, "user1", auth_model.AccessTokenScopeWriteAdmin, auth_model.AccessTokenScopeWriteRepository)
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user4", map[string]string{"type": "Bot"}).AddTokenAuth(admin), http.StatusOK)
	bot := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user4"})
	require.True(t, bot.IsTypeBot())

	t.Run("CreateUseListDelete", func(t *testing.T) {
		resp := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/admin/users/user4/tokens", api.CreateAccessTokenOption{
			Name:   "training-service",
			Scopes: []string{"write:repository"},
		}).AddTokenAuth(admin), http.StatusCreated)
		created := DecodeJSON(t, resp, &api.AccessToken{})
		assert.NotEmpty(t, created.Token)
		assert.Equal(t, []string{"write:repository"}, created.Scopes)

		// The token works, and as the bot.
		MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/search").AddTokenAuth(created.Token), http.StatusOK)
		unittest.AssertExistsAndLoadBean(t, &auth_model.AccessToken{ID: created.ID, UID: bot.ID})

		resp = MakeRequest(t, NewRequest(t, "GET", "/api/v1/admin/users/user4/tokens").AddTokenAuth(admin), http.StatusOK)
		listed := DecodeJSON(t, resp, []*api.AccessToken{})
		require.Len(t, listed, 1)
		assert.Empty(t, listed[0].Token, "a listing never carries the secret")

		MakeRequest(t, NewRequest(t, "DELETE", fmt.Sprintf("/api/v1/admin/users/user4/tokens/%d", created.ID)).AddTokenAuth(admin), http.StatusNoContent)
		unittest.AssertNotExistsBean(t, &auth_model.AccessToken{ID: created.ID})
	})

	t.Run("ListedAsABot", func(t *testing.T) {
		resp := MakeRequest(t, NewRequest(t, "GET", "/api/v1/admin/users?type=Bot&limit=50").AddTokenAuth(admin), http.StatusOK)
		bots := DecodeJSON(t, resp, []*api.User{})
		require.Len(t, bots, 1)
		assert.Equal(t, "user4", bots[0].UserName)
		// People are what the listing has always meant.
		resp = MakeRequest(t, NewRequest(t, "GET", "/api/v1/admin/users?limit=50").AddTokenAuth(admin), http.StatusOK)
		for _, person := range DecodeJSON(t, resp, []*api.User{}) {
			assert.NotEqual(t, "user4", person.UserName)
		}
		MakeRequest(t, NewRequest(t, "GET", "/api/v1/admin/users?type=Robot").AddTokenAuth(admin), http.StatusUnprocessableEntity)
	})

	t.Run("NotBroaderThanTheCaller", func(t *testing.T) {
		MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/admin/users/user4/tokens", api.CreateAccessTokenOption{
			Name:   "too-broad",
			Scopes: []string{"write:organization"},
		}).AddTokenAuth(admin), http.StatusForbidden)
	})

	t.Run("OnlyABot", func(t *testing.T) {
		MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/admin/users/user2/tokens", api.CreateAccessTokenOption{
			Name:   "not-yours",
			Scopes: []string{"read:repository"},
		}).AddTokenAuth(admin), http.StatusUnprocessableEntity)
		MakeRequest(t, NewRequest(t, "GET", "/api/v1/admin/users/user2/tokens").AddTokenAuth(admin), http.StatusUnprocessableEntity)
	})

	t.Run("OnlyAnAdministrator", func(t *testing.T) {
		someone := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/admin/users/user4/tokens", api.CreateAccessTokenOption{
			Name:   "taken",
			Scopes: []string{"read:repository"},
		}).AddTokenAuth(someone), http.StatusForbidden)
	})
}
