// Copyright 2026 The RIA Hub Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"strings"
	"time"
)

// RiaHub is the one place this fork is told where RIA Hub's API is.
//
// It exists so that a repository push can be reported to that API without
// anything having to be registered first. The alternative -- a Gitea webhook
// pointed at the API -- has to be created through the admin API by somebody
// holding an admin token, is invisible when it is missing, and has been
// forgotten on two deployments in a row. A hook that nobody has to create is
// a hook nobody can forget.
//
// Nothing here is a secret: the request carries the INTERNAL_TOKEN this
// instance already has, which the API already holds for its own calls in the
// other direction. There is no second credential to mint and no second
// secret to keep in step.
var RiaHub = struct {
	Enabled bool          `ini:"ENABLED"`
	APIURL  string        `ini:"API_URL"`
	Timeout time.Duration `ini:"TIMEOUT"`
}{
	Enabled: true,
	// Both processes share a host. Sending a local push out through the edge
	// and back would make the catalog depend on the tunnel being up to hear
	// about it.
	APIURL:  "http://127.0.0.1:8000",
	Timeout: 10 * time.Second,
}

func loadRiaHubFrom(rootCfg ConfigProvider) {
	mustMapSetting(rootCfg, "riahub", &RiaHub)
	RiaHub.APIURL = strings.TrimSuffix(strings.TrimSpace(RiaHub.APIURL), "/")
	if RiaHub.APIURL == "" {
		RiaHub.Enabled = false
	}
}
