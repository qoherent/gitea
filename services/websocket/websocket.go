// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package websocket

import (
	"bytes"
	"context"

	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	notify_service "gitea.dev/services/notify"
	"gitea.dev/services/pubsub"
)

type wsNotifier struct {
	notify_service.NullNotifier
}

var _ notify_service.Notifier = &wsNotifier{}

func Init() error {
	// the pubsub broker must be ready before the notifier starts publishing to it
	if err := pubsub.Init(); err != nil {
		return err
	}
	notify_service.RegisterNotifier(&wsNotifier{})
	return nil
}

func UserData(ctx context.Context, user *user_model.User) (data [][]byte) {
	for _, event := range [][]byte{notificationCountEvent(ctx, user.ID), stopwatchesEvent(ctx, user)} {
		if event != nil {
			data = append(data, event)
		}
	}
	return data
}

// Current returns the event to send for a broker payload: for an event that
// carries a whole piece of state -- the unread count, the stopwatches -- that
// state read now, not the copy read when the event was published.
//
// A payload is read at publish time and can sit in a connection's queue while a
// newer snapshot is read and sent, as on a reconnect, where the snapshot read
// races the events published around it. Forwarded as it was, the older state
// arrived second and replaced the newer one, with nothing on either side to
// say which was which. Read at send time, whatever is sent is the latest there
// is, so the order of sends cannot make state go backwards. Other events are
// returned unchanged.
func Current(ctx context.Context, user *user_model.User, brokerPayload []byte) []byte {
	_, eventData, _ := bytes.Cut(brokerPayload, []byte("\n"))
	var event struct {
		EventType string `json:"eventType"`
	}
	if json.Unmarshal(eventData, &event) != nil {
		return brokerPayload
	}
	switch event.EventType {
	case eventNotificationCount:
		return notificationCountEvent(ctx, user.ID)
	case eventStopwatches:
		return stopwatchesEvent(ctx, user)
	}
	return brokerPayload
}

func EventForSession(brokerPayload []byte, sessionID string) []byte {
	target, eventData, _ := bytes.Cut(brokerPayload, []byte("\n"))
	if len(target) > 0 && string(target) != sessionID {
		return nil
	}
	return eventData
}

// makeEventMessage prefixes the client JSON with its target session, empty for all sessions.
func makeEventMessage(eventType, targetSession string, eventData any) []byte {
	buf := bytes.NewBufferString(targetSession + "\n")
	err := json.MarshalWriteRelaxed(buf, &struct {
		EventType string `json:"eventType"`
		EventData any    `json:"eventData,omitzero"`
	}{eventType, eventData})
	if err != nil {
		setting.PanicInDevOrTesting("websocket: marshal event: %v", err)
		return nil
	}
	return buf.Bytes()
}

func publishUserEvent(userID int64, build func() []byte) {
	topic := pubsub.UserTopic(userID)
	if !pubsub.DefaultBroker.HasTopicSubscribers(topic) {
		return
	}
	if event := build(); event != nil {
		pubsub.DefaultBroker.Publish(topic, event)
	}
}
