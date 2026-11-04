// Copyright 2026 The RIA Hub Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package riahub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/graceful"
	"gitea.dev/modules/log"
	"gitea.dev/modules/queue"
	repo_module "gitea.dev/modules/repository"
	"gitea.dev/modules/setting"

	"github.com/google/uuid"
)

// Event is one thing that happened, as reported to RIA Hub's API.
type Event struct {
	// EventID makes a redelivery recognisable as the same event. The API
	// stores it, so a retry after a timeout that in fact arrived is recorded
	// as a duplicate rather than processed twice.
	EventID      string     `json:"event_id"`
	Kind         string     `json:"kind"`
	RepositoryID int64      `json:"repository_id"`
	Owner        string     `json:"owner"`
	Name         string     `json:"name"`
	Ref          string     `json:"ref,omitempty"`
	CommitSHA    string     `json:"commit_sha,omitempty"`
	Paths        []PathFact `json:"paths,omitempty"`
}

const (
	KindPushed  = "repository_pushed"
	KindDeleted = "repository_deleted"
)

var eventQueue *queue.WorkerPoolQueue[*Event]

// Init starts the queue that carries events to the API.
//
// Queued rather than sent inline, which is the one thing the pre-refactor
// fork got badly wrong: it classified inside the post-receive hook and polled
// for the answer for up to fifteen minutes, holding the client's `git push`
// open the whole time. Nothing here is on the push path. A push returns when
// git says it is done, and the report follows.
//
// The queue is also what makes an API that is down a delay rather than a
// loss: items that fail are returned to it, and it survives a restart.
func Init() error {
	if !setting.RiaHub.Enabled {
		log.Info("riahub: no API_URL configured; pushes will not be reported")
		return nil
	}
	eventQueue = queue.CreateSimpleQueue(graceful.GetManager().ShutdownContext(), "riahub_event", handler)
	if eventQueue == nil {
		return fmt.Errorf("unable to create riahub_event queue")
	}
	go graceful.GetManager().RunWithCancel(eventQueue)
	registerNotifier()
	return nil
}

func handler(items ...*Event) []*Event {
	var unhandled []*Event
	for _, event := range items {
		if err := send(event); err != nil {
			log.Error("riahub: reporting %s for repository %d failed, will retry: %v", event.Kind, event.RepositoryID, err)
			unhandled = append(unhandled, event)
		}
	}
	return unhandled
}

func send(event *Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		// Unrecoverable: retrying cannot make this encode. Dropped with a
		// loud log rather than returned, which would retry it forever.
		log.Error("riahub: could not encode %s for repository %d: %v", event.Kind, event.RepositoryID, err)
		return nil
	}
	ctx, cancel := context.WithTimeout(graceful.GetManager().ShutdownContext(), setting.RiaHub.Timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, setting.RiaHub.APIURL+"/webhooks/internal/event", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	// The credential both processes already share. The API holds this same
	// value to call this instance's own private routes, so there is no second
	// token to mint and nothing that can drift out of step.
	request.Header.Set("X-Gitea-Internal-Auth", "Bearer "+setting.InternalToken)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusAccepted, response.StatusCode == http.StatusOK:
		return nil
	case response.StatusCode == http.StatusNoContent:
		return nil
	case response.StatusCode >= 400 && response.StatusCode < 500:
		// The API understood and refused. Retrying an unauthorised or
		// malformed report just repeats it; said once, loudly, and dropped.
		log.Error("riahub: the API refused %s for repository %d with %d; not retrying",
			event.Kind, event.RepositoryID, response.StatusCode)
		return nil
	default:
		return fmt.Errorf("the API answered %d", response.StatusCode)
	}
}

func enqueue(event *Event) {
	if eventQueue == nil {
		return
	}
	event.EventID = uuid.NewString()
	if err := eventQueue.Push(event); err != nil {
		log.Error("riahub: could not queue %s for repository %d: %v", event.Kind, event.RepositoryID, err)
	}
}

// NotifyPush reports what a push changed. Called after the push is complete
// and never blocks it.
func NotifyPush(ctx context.Context, repo *repo_model.Repository, updates []*repo_module.PushUpdateOptions) {
	if eventQueue == nil || repo == nil {
		return
	}
	for _, update := range updates {
		if update.IsDelRef() || !update.RefFullName.IsBranch() {
			continue
		}
		facts, err := changedPaths(ctx, repo, update.OldCommitID, update.NewCommitID)
		if err != nil {
			log.Error("riahub: could not list what %s changed in repository %d: %v",
				update.NewCommitID, repo.ID, err)
			continue
		}
		if len(facts) == 0 {
			continue
		}
		resolveLFS(ctx, repo, update.NewCommitID, facts)
		enqueue(&Event{
			Kind:         KindPushed,
			RepositoryID: repo.ID,
			Owner:        repo.OwnerName,
			Name:         repo.Name,
			Ref:          update.RefFullName.String(),
			CommitSHA:    update.NewCommitID,
			Paths:        facts,
		})
	}
}

// NotifyRepositoryDeleted reports a repository that no longer exists, so the
// catalog can forget what was in it.
func NotifyRepositoryDeleted(repo *repo_model.Repository) {
	if eventQueue == nil || repo == nil {
		return
	}
	enqueue(&Event{
		Kind:         KindDeleted,
		RepositoryID: repo.ID,
		Owner:        repo.OwnerName,
		Name:         repo.Name,
	})
}
