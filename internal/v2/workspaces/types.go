package workspaces

import "time"

const (
	DefaultWorkspaceName = "Моя компания"

	WorkspaceStatusActive = "active"

	MembershipRoleOwner    = "owner"
	MembershipRoleAdmin    = "admin"
	MembershipRoleMember   = "member"
	MembershipStatusActive = "active"
)

type Workspace struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	DisplayName *string   `json:"display_name"`
	OwnerUserID int       `json:"owner_user_id"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Membership struct {
	ID          int       `json:"-"`
	WorkspaceID int       `json:"-"`
	UserID      int       `json:"-"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"-"`
	UpdatedAt   time.Time `json:"-"`
}
