package workspacedocs

import (
	"errors"
	"time"
)

var (
	ErrInvalidDocument = errors.New("invalid_workspace_document")
	ErrInvalidLink     = errors.New("invalid_workspace_document_link")
	ErrInvalidParent   = errors.New("invalid_workspace_document_parent")
)

type Document struct {
	ID                  int64      `json:"id"`
	WorkspaceID         int        `json:"workspace_id"`
	ParentID            *int64     `json:"parent_id,omitempty"`
	Title               string     `json:"title"`
	Content             string     `json:"content"`
	Status              string     `json:"status"`
	Favorite            bool       `json:"favorite"`
	LinkedDepartmentIDs []int      `json:"linked_department_ids"`
	LinkedWorkstreamIDs []int      `json:"linked_workstream_ids"`
	LinkedProjectIDs    []int      `json:"linked_project_ids"`
	Version             int        `json:"version"`
	CreatedBy           *int       `json:"created_by,omitempty"`
	UpdatedBy           *int       `json:"updated_by,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	ArchivedAt          *time.Time `json:"archived_at,omitempty"`
}

type Version struct {
	ID         int64     `json:"id"`
	DocumentID int64     `json:"document_id"`
	Version    int       `json:"version"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	Status     string    `json:"status"`
	Favorite   bool      `json:"favorite"`
	SavedBy    *int      `json:"saved_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type Input struct {
	Title         *string `json:"title"`
	Content       *string `json:"content"`
	Status        *string `json:"status"`
	Favorite      *bool   `json:"favorite"`
	ParentID      *int64  `json:"parent_id"`
	ClearParent   bool    `json:"clear_parent"`
	DepartmentIDs *[]int  `json:"linked_department_ids"`
	WorkstreamIDs *[]int  `json:"linked_workstream_ids"`
	ProjectIDs    *[]int  `json:"linked_project_ids"`
}
