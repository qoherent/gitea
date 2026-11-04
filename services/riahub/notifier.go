// Copyright 2026 The RIA Hub Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package riahub

import (
	"context"

	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	notify_service "gitea.dev/services/notify"
)

type notifier struct {
	notify_service.NullNotifier
}

// DeleteRepository reports a repository that is gone, so the catalog stops
// listing assets nobody can reach.
//
// A notifier rather than a second webhook: Gitea deletes a repository's own
// hooks and their queued deliveries as part of deleting the repository,
// racing its own queue for this very event. A repository-scoped hook is
// therefore the one kind that cannot reliably report this.
func (n *notifier) DeleteRepository(ctx context.Context, doer *user_model.User, repo *repo_model.Repository) {
	NotifyRepositoryDeleted(repo)
}

func registerNotifier() {
	notify_service.RegisterNotifier(&notifier{})
}
