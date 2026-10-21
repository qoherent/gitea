// Copyright 2016 The Gogs Authors. All rights reserved.
// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package structs

// TeamVisibility controls who can list a team within its organization.
//   - "public":  visible to any signed-in user (still bounded by org visibility)
//   - "limited": visible to any member of the parent organization
//   - "private": visible only to team members and org owners
//
// swagger:enum TeamVisibility
type TeamVisibility string

const (
	TeamVisibilityPublic  TeamVisibility = "public"
	TeamVisibilityLimited TeamVisibility = "limited"
	TeamVisibilityPrivate TeamVisibility = "private"
)

// Team represents a team in an organization
type Team struct {
	// The unique identifier of the team
	ID int64 `json:"id"`
	// The name of the team
	Name string `json:"name"`
	// The description of the team
	Description string `json:"description"`
	// The organization that the team belongs to
	Organization *Organization `json:"organization"`
	// Whether the team has access to all repositories in the organization
	IncludesAllRepositories bool            `json:"includes_all_repositories"`
	Permission              AccessLevelName `json:"permission"`
	// example: ["repo.code","repo.issues","repo.ext_issues","repo.wiki","repo.pulls","repo.releases","repo.projects","repo.ext_wiki"]
	// Deprecated: This variable should be replaced by UnitsMap and will be dropped in later versions.
	Units []string `json:"units"`
	// example: {"repo.code":"read","repo.issues":"write","repo.ext_issues":"none","repo.wiki":"admin","repo.pulls":"owner","repo.releases":"none","repo.projects":"none","repo.ext_wiki":"none"}
	UnitsMap map[string]string `json:"units_map"`
	// Whether the team can create repositories in the organization
	CanCreateOrgRepo bool `json:"can_create_org_repo"`
	// Whether this team's members may have Actions runs dispatched to runners
	// marked for privileged compute in this organization. Never granted to
	// the owner team automatically, unlike the other organization-wide flags:
	// privileged runners execute on hardware the instance owns.
	PrivilegedCompute bool `json:"privileged_compute"`
	// Team visibility within the organization. "private" teams are only
	// listable by members and org owners; "limited" teams are listable by
	// any organization member; "public" teams are listable by any signed-in
	// user.
	Visibility TeamVisibility `json:"visibility"`
}

// CreateTeamOption options for creating a team
type CreateTeamOption struct {
	// required: true
	Name string `json:"name" binding:"Required;AlphaDashDot;MaxSize(255)"`
	// The description of the team
	Description string `json:"description" binding:"MaxSize(255)"`
	// Whether the team has access to all repositories in the organization
	IncludesAllRepositories bool                `json:"includes_all_repositories"`
	Permission              RepoWritePermission `json:"permission"`
	// example: ["repo.actions","repo.code","repo.issues","repo.ext_issues","repo.wiki","repo.ext_wiki","repo.pulls","repo.releases","repo.projects","repo.ext_wiki"]
	// Deprecated: This variable should be replaced by UnitsMap and will be dropped in later versions.
	Units []string `json:"units"`
	// example: {"repo.actions","repo.packages","repo.code":"read","repo.issues":"write","repo.ext_issues":"none","repo.wiki":"admin","repo.pulls":"owner","repo.releases":"none","repo.projects":"none","repo.ext_wiki":"none"}
	UnitsMap map[string]string `json:"units_map"`
	// Whether the team can create repositories in the organization
	CanCreateOrgRepo bool `json:"can_create_org_repo"`
	// Whether this team's members may have Actions runs dispatched to runners
	// marked for privileged compute in this organization. Defaults to false,
	// so a team made without naming it carries no grant.
	PrivilegedCompute bool `json:"privileged_compute"`
	// Team visibility within the organization. Defaults to "private".
	Visibility TeamVisibility `json:"visibility" binding:"OmitEmpty;In(public,limited,private)"`
}

// EditTeamOption options for editing a team
type EditTeamOption struct {
	// required: true
	Name string `json:"name" binding:"AlphaDashDot;MaxSize(255)"`
	// The description of the team
	Description *string `json:"description" binding:"MaxSize(255)"`
	// Whether the team has access to all repositories in the organization
	IncludesAllRepositories *bool               `json:"includes_all_repositories"`
	Permission              RepoWritePermission `json:"permission"`
	// example: ["repo.code","repo.issues","repo.ext_issues","repo.wiki","repo.pulls","repo.releases","repo.projects","repo.ext_wiki"]
	// Deprecated: This variable should be replaced by UnitsMap and will be dropped in later versions.
	Units []string `json:"units"`
	// example: {"repo.code":"read","repo.issues":"write","repo.ext_issues":"none","repo.wiki":"admin","repo.pulls":"owner","repo.releases":"none","repo.projects":"none","repo.ext_wiki":"none"}
	UnitsMap map[string]string `json:"units_map"`
	// Whether the team can create repositories in the organization
	CanCreateOrgRepo *bool `json:"can_create_org_repo"`
	// Whether this team's members may have Actions runs dispatched to runners
	// marked for privileged compute in this organization. When omitted, the
	// grant is left unchanged -- a pointer rather than a bool, so that an
	// edit which does not mention it cannot revoke it.
	PrivilegedCompute *bool `json:"privileged_compute"`
	// Team visibility within the organization. When omitted, visibility is
	// left unchanged.
	Visibility *TeamVisibility `json:"visibility" binding:"OmitEmpty;In(public,limited,private)"`
}

// ComputeAllowance is how much runner time an organization has used this
// period, and how much it may use.
//
// Storage already had a quota and compute had none, so "this organization has
// used its share" was a sentence the instance could not say.
type ComputeAllowance struct {
	// The accounting window, "2006-01" in UTC. A calendar month, so it resets
	// without anything running on a schedule.
	Period string `json:"period"`
	// Runner seconds spent in this window.
	UsedSeconds int64 `json:"used_seconds"`
	// The ceiling in seconds. Zero means none is set.
	LimitSeconds int64 `json:"limit_seconds"`
	// Whether work will be dispatched regardless of usage -- either because
	// no limit is set, or because this organization holds privileged compute.
	Unlimited bool `json:"unlimited"`
	// Whether the exemption is the privileged-compute grant specifically,
	// which is a different thing from nobody having set a limit yet.
	Exempt bool `json:"exempt"`
}

// SetComputeAllowanceOption sets an organization's ceiling.
type SetComputeAllowanceOption struct {
	// Seconds per calendar month. Zero clears the limit.
	LimitSeconds int64 `json:"limit_seconds"`
}
