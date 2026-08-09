package profile

import (
	"time"

	"reup-goals-backend/internal/v2/billing"
)

const (
	roleOwner  = "owner"
	roleAdmin  = "admin"
	roleMember = "member"

	invitationPending   = "pending"
	invitationAccepted  = "accepted"
	invitationExpired   = "expired"
	invitationCancelled = "cancelled"
)

type Overview struct {
	Account      Account             `json:"account"`
	Workspace    WorkspaceSummary    `json:"workspace"`
	Membership   MembershipSummary   `json:"membership"`
	Subscription SubscriptionSummary `json:"subscription"`
	Capabilities Capabilities        `json:"capabilities"`
}

type Account struct {
	ID          int    `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	AvatarURL   string `json:"avatar_url"`
	CompanyRole string `json:"company_role"`
}

type WorkspaceSummary struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	MemberCount int    `json:"member_count"`
}

type MembershipSummary struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

type Capabilities struct {
	ManageWorkspace    bool `json:"manage_workspace"`
	ManageMembers      bool `json:"manage_members"`
	ManageSubscription bool `json:"manage_subscription"`
	DeleteWorkspace    bool `json:"delete_workspace"`
}

type SubscriptionSummary struct {
	Plan              string               `json:"plan"`
	PlanCode          string               `json:"plan_code"`
	BillingPeriod     string               `json:"billing_period"`
	Status            string               `json:"status"`
	Amount            float64              `json:"amount"`
	AnnualAmount      float64              `json:"annual_amount"`
	ResetAmount       float64              `json:"reset_amount"`
	Currency          string               `json:"currency"`
	PaymentMethod     string               `json:"payment_method"`
	PaymentProvider   string               `json:"payment_provider"`
	PeriodEnd         *time.Time           `json:"period_end"`
	NextRenewal       *time.Time           `json:"next_renewal"`
	GraceUntil        *time.Time           `json:"grace_until"`
	PendingPlanCode   string               `json:"pending_plan_code,omitempty"`
	PendingPlanName   string               `json:"pending_plan_name,omitempty"`
	PendingPeriod     string               `json:"pending_billing_period,omitempty"`
	PendingStartsAt   *time.Time           `json:"pending_starts_at,omitempty"`
	Access            bool                 `json:"access"`
	DisplayStatus     string               `json:"display_status"`
	CheckoutAvailable bool                 `json:"checkout_available"`
	MemberLimit       int                  `json:"member_limit"`
	SeatsUsed         int                  `json:"seats_used"`
	AIUsage           billing.QuotaSummary `json:"ai_usage"`
	AvailablePlans    []billing.Plan       `json:"available_plans"`
}

type AIUsageResponse struct {
	PlanCode              string               `json:"plan_code"`
	PlanName              string               `json:"plan_name"`
	ResetAmount           float64              `json:"reset_amount"`
	Currency              string               `json:"currency"`
	CanManageSubscription bool                 `json:"can_manage_subscription"`
	AIUsage               billing.QuotaSummary `json:"ai_usage"`
}

type Member struct {
	ID            int64      `json:"id"`
	Kind          string     `json:"kind"`
	UserID        *int       `json:"user_id,omitempty"`
	Name          string     `json:"name"`
	Email         string     `json:"email"`
	AvatarURL     string     `json:"avatar_url"`
	CompanyRole   string     `json:"company_role"`
	Role          string     `json:"role"`
	Status        string     `json:"status"`
	DepartmentIDs []int      `json:"department_ids"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	CanBeRemoved  bool       `json:"can_be_removed"`
	CanChangeRole bool       `json:"can_change_role"`
}

type InvitationPreview struct {
	WorkspaceName   string    `json:"workspace_name"`
	InvitedEmail    string    `json:"invited_email"`
	InviterName     string    `json:"inviter_name"`
	InviterEmail    string    `json:"inviter_email"`
	Role            string    `json:"role"`
	DepartmentIDs   []int     `json:"department_ids"`
	DepartmentNames []string  `json:"department_names"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type Settings struct {
	InterfaceLanguage      string `json:"interface_language"`
	Theme                  string `json:"theme"`
	DateFormat             string `json:"date_format"`
	AILanguage             string `json:"ai_language"`
	EmailNotifications     bool   `json:"email_notifications"`
	InProductNotifications bool   `json:"in_product_notifications"`
}

type BillingOrganization struct {
	FullName           string `json:"full_name"`
	INN                string `json:"inn"`
	KPP                string `json:"kpp"`
	RegistrationNumber string `json:"registration_number"`
	LegalAddress       string `json:"legal_address"`
	AccountingEmail    string `json:"accounting_email"`
	ContactPerson      string `json:"contact_person"`
}

type Invoice struct {
	ID             int64      `json:"id"`
	OrderID        *int64     `json:"order_id,omitempty"`
	Number         string     `json:"number"`
	OrderKind      string     `json:"order_kind"`
	PlanCode       string     `json:"plan_code"`
	BillingPeriod  string     `json:"billing_period"`
	Description    string     `json:"description"`
	TaxLabel       string     `json:"tax_label"`
	Amount         float64    `json:"amount"`
	Currency       string     `json:"currency"`
	Status         string     `json:"status"`
	RecipientEmail string     `json:"recipient_email"`
	IssuedAt       time.Time  `json:"issued_at"`
	DueAt          time.Time  `json:"due_at"`
	IssuedDate     string     `json:"issued_date"`
	DueDate        string     `json:"due_date"`
	Timezone       string     `json:"timezone"`
	PaidAt         *time.Time `json:"paid_at,omitempty"`
	EmailedAt      *time.Time `json:"emailed_at,omitempty"`
	DocumentID     *int64     `json:"document_id,omitempty"`
}

type SellerProfile struct {
	FullName             string `json:"full_name"`
	INN                  string `json:"inn"`
	KPP                  string `json:"kpp"`
	RegistrationNumber   string `json:"registration_number"`
	LegalAddress         string `json:"legal_address"`
	BankName             string `json:"bank_name"`
	SettlementAccount    string `json:"settlement_account"`
	CorrespondentAccount string `json:"correspondent_account"`
	BIC                  string `json:"bic"`
	DirectorName         string `json:"director_name"`
	AccountingEmail      string `json:"accounting_email"`
	TaxLabel             string `json:"tax_label"`
}

type InvoiceRequest struct {
	PlanCode       string `json:"plan_code"`
	BillingPeriod  string `json:"billing_period"`
	OrderKind      string `json:"order_kind"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type CheckoutRequest struct {
	PlanCode       string `json:"plan_code"`
	BillingPeriod  string `json:"billing_period"`
	OrderKind      string `json:"order_kind"`
	Quantity       int    `json:"quantity"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type CheckoutOrder struct {
	ID            int64
	OrderKind     string
	PlanCode      string
	BillingPeriod string
	Quantity      int
	Amount        float64
	Currency      string
}

type BillingDocument struct {
	ID          int64      `json:"id"`
	InvoiceID   *int64     `json:"invoice_id,omitempty"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	FileName    string     `json:"file_name"`
	MimeType    string     `json:"mime_type"`
	PeriodStart *time.Time `json:"period_start,omitempty"`
	PeriodEnd   *time.Time `json:"period_end,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Payment struct {
	ID        int64      `json:"id"`
	InvoiceID *int64     `json:"invoice_id,omitempty"`
	Provider  string     `json:"provider"`
	Method    string     `json:"method"`
	Amount    float64    `json:"amount"`
	Currency  string     `json:"currency"`
	Status    string     `json:"status"`
	PaidAt    *time.Time `json:"paid_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type About struct {
	Version          string `json:"version"`
	ChangelogURL     string `json:"changelog_url"`
	DocumentationURL string `json:"documentation_url"`
	SupportEmail     string `json:"support_email"`
	BugReportURL     string `json:"bug_report_url"`
	IdeaURL          string `json:"idea_url"`
}
