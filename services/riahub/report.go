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

// job is what the queue carries: where to look, not what was found.
//
// The facts are gathered in the worker rather than before the push returns.
// Gathering them means one `ls-tree` or `diff-tree` and a `cat-file` for
// every changed blob small enough to be an LFS pointer -- which on a large
// import is thousands of git invocations, and the post-receive hook waits
// for all of them. "Queued, so the push returns at once" was only true of
// the HTTP call.
// The tags matter: the queue persists its items, so an upgrade meets
// whatever the previous build wrote. These are the names the Event this
// replaced marshalled under, and `commit_sha` lands on NewCommitID, so a
// report saved by the old build decodes into a usable job rather than an
// empty one -- it loses only `old_commit_id`, which is absent, and an
// absent old commit is already the "this ref is new" case: the whole tree
// is reported as additions, which the catalogue upserts. Over-reporting a
// pending item once beats discarding it.
type job struct {
	EventID      string `json:"event_id"`
	Kind         string `json:"kind"`
	RepositoryID int64  `json:"repository_id"`
	Owner        string `json:"owner"`
	Name         string `json:"name"`
	Ref          string `json:"ref,omitempty"`
	OldCommitID  string `json:"old_commit_id,omitempty"`
	NewCommitID  string `json:"commit_sha,omitempty"`
}

var eventQueue *queue.WorkerPoolQueue[*job]

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

func handler(items ...*job) []*job {
	var unhandled []*job
	for _, item := range items {
		event, err := describe(item)
		if err != nil {
			// Only an absent repository is permanent. Everything else --
			// a database that is briefly unreachable, a read cancelled by
			// shutdown -- is mended by asking again, and dropping it here
			// loses a report that was already waiting to be retried.
			if repo_model.IsErrRepoNotExist(err) {
				log.Error("riahub: repository %d is gone; dropping its %s", item.RepositoryID, item.Kind)
				continue
			}
			log.Error("riahub: could not describe %s for repository %d, will retry: %v", item.Kind, item.RepositoryID, err)
			unhandled = append(unhandled, item)
			continue
		}
		if event == nil {
			continue
		}
		if err := send(event); err != nil {
			log.Error("riahub: reporting %s for repository %d failed, will retry: %v", item.Kind, item.RepositoryID, err)
			unhandled = append(unhandled, item)
		}
	}
	return unhandled
}

// describe turns a queued range into the event the API is sent, reading the
// repository as it is now. A nil event means there is nothing to report.
func describe(item *job) (*Event, error) {
	event := &Event{
		EventID:      item.EventID,
		Kind:         item.Kind,
		RepositoryID: item.RepositoryID,
		Owner:        item.Owner,
		Name:         item.Name,
		Ref:          item.Ref,
		CommitSHA:    item.NewCommitID,
	}
	if item.Kind != KindPushed {
		return event, nil
	}
	ctx := graceful.GetManager().ShutdownContext()
	repo, err := repo_model.GetRepositoryByID(ctx, item.RepositoryID)
	if err != nil {
		return nil, err
	}
	facts, err := changedPaths(ctx, repo, item.OldCommitID, item.NewCommitID)
	if err != nil {
		return nil, err
	}
	if len(facts) == 0 {
		return nil, nil
	}
	resolveLFS(ctx, repo, item.NewCommitID, facts)
	event.Paths = facts
	return event, nil
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
	switch outcomeOf(response.StatusCode) {
	case delivered:
		return nil
	case refused:
		log.Error("riahub: the API refused %s for repository %d with %d; not retrying",
			event.Kind, event.RepositoryID, response.StatusCode)
		return nil
	default:
		return fmt.Errorf("the API answered %d", response.StatusCode)
	}
}

type outcome int

const (
	delivered outcome = iota
	// refused: the API understood and will not take it. Retrying an
	// unauthorised or malformed report just repeats it.
	refused
	// retryable: not now. Returned to the queue.
	retryable
)

// outcomeOf reads a response status as one of three answers.
//
// Separated from the call so the rule can be tested without a server. The
// two that look like refusals and are not cost a catalogue update each: 408
// is a proxy giving up on a slow request and 429 is a rate limiter, and
// both are exactly the outage the queue exists to ride out.
func outcomeOf(status int) outcome {
	switch {
	case status == http.StatusOK, status == http.StatusAccepted, status == http.StatusNoContent:
		return delivered
	case status == http.StatusRequestTimeout, status == http.StatusTooManyRequests:
		return retryable
	case status >= 400 && status < 500:
		return refused
	default:
		return retryable
	}
}

func enqueue(item *job) {
	if eventQueue == nil {
		return
	}
	// Assigned here rather than at send time, so every retry of this item
	// carries the id the API already recorded.
	item.EventID = uuid.NewString()
	if err := eventQueue.Push(item); err != nil {
		log.Error("riahub: could not queue %s for repository %d: %v", item.Kind, item.RepositoryID, err)
	}
}

// NotifyPush reports what a push changed.
//
// It queues the range and returns. Nothing here reads the repository: the
// worker does that, because reading it is one git invocation per changed
// blob and the post-receive hook is waiting.
func NotifyPush(_ context.Context, repo *repo_model.Repository, updates []*repo_module.PushUpdateOptions) {
	if eventQueue == nil || repo == nil {
		return
	}
	for _, update := range updates {
		if update.IsDelRef() || !update.RefFullName.IsBranch() {
			continue
		}
		enqueue(&job{
			Kind:         KindPushed,
			RepositoryID: repo.ID,
			Owner:        repo.OwnerName,
			Name:         repo.Name,
			Ref:          update.RefFullName.String(),
			OldCommitID:  update.OldCommitID,
			NewCommitID:  update.NewCommitID,
		})
	}
}

// NotifyRepositoryDeleted reports a repository that no longer exists, so the
// catalog can forget what was in it.
func NotifyRepositoryDeleted(repo *repo_model.Repository) {
	if eventQueue == nil || repo == nil {
		return
	}
	enqueue(&job{
		Kind:         KindDeleted,
		RepositoryID: repo.ID,
		Owner:        repo.OwnerName,
		Name:         repo.Name,
	})
}
