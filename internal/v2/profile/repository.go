package profile

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"reup-goals-backend/internal/v2/workspaces"
)

var (
	ErrMemberLimitReached = errors.New("workspace_member_limit_reached")
	ErrAlreadyMember      = errors.New("workspace_member_already_exists")
	ErrInvalidMemberRole  = errors.New("invalid_member_role")
	ErrInvalidDepartments = errors.New("invalid_invitation_departments")
)

const invitationResendCooldown = 2 * time.Minute

type InvitationResendTooSoonError struct {
	RetryAfter time.Duration
}

func (e *InvitationResendTooSoonError) Error() string {
	return "invitation_resend_too_soon"
}

func (e *InvitationResendTooSoonError) RetryAfterSeconds() int {
	seconds := int((e.RetryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

type Store struct {
	dbx        *sql.DB
	workspaces *workspaces.Store
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx, workspaces: workspaces.NewStore(dbx)}
}

func (s *Store) Overview(ctx context.Context, userID int, checkoutAvailable bool) (Overview, error) {
	workspace, membership, err := s.workspaces.GetOrCreateDefault(ctx, userID)
	if err != nil {
		return Overview{}, err
	}

	var account Account
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT id, email, name, avatar_url, company_role FROM users WHERE id=$1
	`, userID).Scan(&account.ID, &account.Email, &account.Name, &account.AvatarURL, &account.CompanyRole); err != nil {
		return Overview{}, err
	}

	var memberCount int
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workspace_memberships
		WHERE workspace_id=$1 AND status='active'
	`, workspace.ID).Scan(&memberCount); err != nil {
		return Overview{}, err
	}

	subscription, err := s.Subscription(ctx, workspace.ID, workspace.OwnerUserID, checkoutAvailable)
	if err != nil {
		return Overview{}, err
	}
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workspace_memberships
		WHERE workspace_id=$1 AND status='active'
	`, workspace.ID).Scan(&subscription.SeatsUsed); err != nil {
		return Overview{}, err
	}

	displayName := workspace.Name
	if workspace.DisplayName != nil && strings.TrimSpace(*workspace.DisplayName) != "" {
		displayName = strings.TrimSpace(*workspace.DisplayName)
	}
	isOwner := membership.Role == roleOwner
	canManageMembers := isOwner || membership.Role == roleAdmin
	return Overview{
		Account: account,
		Workspace: WorkspaceSummary{
			ID:          workspace.ID,
			Name:        workspace.Name,
			DisplayName: displayName,
			Status:      workspace.Status,
			MemberCount: memberCount,
		},
		Membership:   MembershipSummary{Role: membership.Role, Status: membership.Status},
		Subscription: subscription,
		Capabilities: Capabilities{
			ManageWorkspace:    isOwner,
			ManageMembers:      canManageMembers,
			ManageSubscription: isOwner,
			DeleteWorkspace:    isOwner,
		},
	}, nil
}

func (s *Store) Subscription(ctx context.Context, workspaceID, ownerUserID int, checkoutAvailable bool) (SubscriptionSummary, error) {
	var result SubscriptionSummary
	var periodEnd, nextRenewal, graceUntil sql.NullTime
	err := s.dbx.QueryRowContext(ctx, `
		SELECT plan_name, status, amount, currency, payment_method, payment_provider,
			current_period_end, next_payment_at, grace_until, member_limit
		FROM subscriptions
		WHERE workspace_id=$1 OR (workspace_id IS NULL AND user_id=$2)
		ORDER BY CASE WHEN workspace_id=$1 THEN 0 ELSE 1 END, updated_at DESC
		LIMIT 1
	`, workspaceID, ownerUserID).Scan(
		&result.Plan, &result.Status, &result.Amount, &result.Currency,
		&result.PaymentMethod, &result.PaymentProvider, &periodEnd, &nextRenewal, &graceUntil,
		&result.MemberLimit,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionSummary{
			Plan:              "REUP.goals Pro",
			Status:            "inactive",
			Currency:          "RUB",
			DisplayStatus:     "inactive",
			CheckoutAvailable: checkoutAvailable,
			MemberLimit:       5,
		}, nil
	}
	if err != nil {
		return SubscriptionSummary{}, err
	}
	result.PeriodEnd = nullableTime(periodEnd)
	result.NextRenewal = nullableTime(nextRenewal)
	result.GraceUntil = nullableTime(graceUntil)
	result.CheckoutAvailable = checkoutAvailable
	result.DisplayStatus = subscriptionDisplayStatus(result.Status, result.PeriodEnd, result.GraceUntil)
	now := time.Now().UTC()
	result.Access = result.Status == "active" || result.Status == "trial_active" ||
		(result.Status == "cancelled" && result.PeriodEnd != nil && now.Before(*result.PeriodEnd)) ||
		(result.Status == "past_due" && result.GraceUntil != nil && now.Before(*result.GraceUntil))
	return result, nil
}

func (s *Store) UpdateAccount(ctx context.Context, userID int, name, avatarURL, companyRole string) (Account, error) {
	var account Account
	err := s.dbx.QueryRowContext(ctx, `
		UPDATE users SET name=$1, avatar_url=$2, company_role=$3 WHERE id=$4
		RETURNING id, email, name, avatar_url, company_role
	`, name, avatarURL, companyRole, userID).Scan(
		&account.ID, &account.Email, &account.Name, &account.AvatarURL, &account.CompanyRole,
	)
	return account, err
}

func (s *Store) UpdateWorkspace(ctx context.Context, workspaceID int, displayName string) (WorkspaceSummary, error) {
	var result WorkspaceSummary
	err := s.dbx.QueryRowContext(ctx, `
		UPDATE workspaces SET name=$1, display_name=$1, updated_at=NOW() WHERE id=$2
		RETURNING id, name, COALESCE(display_name, name), status
	`, displayName, workspaceID).Scan(&result.ID, &result.Name, &result.DisplayName, &result.Status)
	if err != nil {
		return WorkspaceSummary{}, err
	}
	err = s.dbx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=$1 AND status='active'
	`, workspaceID).Scan(&result.MemberCount)
	return result, err
}

func (s *Store) Settings(ctx context.Context, userID int) (Settings, error) {
	var result Settings
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO user_profile_settings (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE SET user_id=EXCLUDED.user_id
		RETURNING interface_language, theme, date_format, ai_language,
			email_notifications, in_product_notifications
	`, userID).Scan(
		&result.InterfaceLanguage, &result.Theme, &result.DateFormat, &result.AILanguage,
		&result.EmailNotifications, &result.InProductNotifications,
	)
	return result, err
}

func (s *Store) UpdateSettings(ctx context.Context, userID int, value Settings) (Settings, error) {
	var result Settings
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO user_profile_settings (
			user_id, interface_language, theme, date_format, ai_language,
			email_notifications, in_product_notifications
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (user_id) DO UPDATE SET
			interface_language=EXCLUDED.interface_language,
			theme=EXCLUDED.theme,
			date_format=EXCLUDED.date_format,
			ai_language=EXCLUDED.ai_language,
			email_notifications=EXCLUDED.email_notifications,
			in_product_notifications=EXCLUDED.in_product_notifications,
			updated_at=NOW()
		RETURNING interface_language, theme, date_format, ai_language,
			email_notifications, in_product_notifications
	`, userID, value.InterfaceLanguage, value.Theme, value.DateFormat, value.AILanguage,
		value.EmailNotifications, value.InProductNotifications).Scan(
		&result.InterfaceLanguage, &result.Theme, &result.DateFormat, &result.AILanguage,
		&result.EmailNotifications, &result.InProductNotifications,
	)
	return result, err
}

func (s *Store) Members(ctx context.Context, workspaceID, currentUserID int) ([]Member, error) {
	if _, err := s.dbx.ExecContext(ctx, `
		UPDATE workspace_invitations SET status='expired', updated_at=NOW()
		WHERE workspace_id=$1 AND status='pending' AND expires_at <= NOW()
	`, workspaceID); err != nil {
		return nil, err
	}

	rows, err := s.dbx.QueryContext(ctx, `
			SELECT membership.id, users.id, users.name, users.email, users.avatar_url,
				users.company_role, membership.role, membership.created_at
		FROM workspace_memberships membership
		JOIN users ON users.id=membership.user_id
		WHERE membership.workspace_id=$1 AND membership.status='active'
		ORDER BY CASE WHEN membership.role='owner' THEN 0 ELSE 1 END, membership.created_at, membership.id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Member, 0)
	for rows.Next() {
		var item Member
		var userID int
		if err := rows.Scan(
			&item.ID, &userID, &item.Name, &item.Email, &item.AvatarURL,
			&item.CompanyRole, &item.Role, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		item.Kind = "membership"
		item.UserID = &userID
		item.Status = invitationAccepted
		item.CanBeRemoved = item.Role != roleOwner && userID != currentUserID
		item.CanChangeRole = item.Role != roleOwner && userID != currentUserID
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	invitationRows, err := s.dbx.QueryContext(ctx, `
		SELECT id, email, role, status, department_ids, expires_at, created_at
		FROM workspace_invitations
		WHERE workspace_id=$1 AND status='pending' AND expires_at>NOW()
		ORDER BY created_at DESC, id DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer invitationRows.Close()
	for invitationRows.Next() {
		var item Member
		var expiresAt time.Time
		if err := invitationRows.Scan(
			&item.ID, &item.Email, &item.Role, &item.Status,
			pq.Array(&item.DepartmentIDs), &expiresAt, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		item.Kind = "invitation"
		item.ExpiresAt = &expiresAt
		item.CanBeRemoved = item.Status == invitationPending
		item.CanChangeRole = item.Status == invitationPending
		result = append(result, item)
	}
	return result, invitationRows.Err()
}

func (s *Store) Invite(
	ctx context.Context,
	workspaceID, invitedBy int,
	email, role string,
	departmentIDs []int,
	memberLimit int,
) (Member, string, error) {
	if role != roleAdmin && role != roleMember {
		return Member{}, "", ErrInvalidMemberRole
	}
	departmentIDs = dedupePositiveIDs(departmentIDs)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return Member{}, "", err
	}
	token := hex.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256([]byte(token))
	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Member{}, "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceID); err != nil {
		return Member{}, "", err
	}
	if len(departmentIDs) > 0 {
		var validDepartmentCount int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM v2_departments
			WHERE workspace_id=$1 AND status='active' AND id=ANY($2)
		`, workspaceID, pq.Array(departmentIDs)).Scan(&validDepartmentCount); err != nil {
			return Member{}, "", err
		}
		if validDepartmentCount != len(departmentIDs) {
			return Member{}, "", ErrInvalidDepartments
		}
	}
	var alreadyMember bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM workspace_memberships membership
			JOIN users ON users.id=membership.user_id
			WHERE membership.workspace_id=$1 AND membership.status='active' AND lower(users.email)=lower($2)
		)
	`, workspaceID, email).Scan(&alreadyMember); err != nil {
		return Member{}, "", err
	}
	if alreadyMember {
		return Member{}, "", ErrAlreadyMember
	}
	var pendingUpdatedAt time.Time
	pendingErr := tx.QueryRowContext(ctx, `
		SELECT updated_at
		FROM workspace_invitations
		WHERE workspace_id=$1 AND lower(email)=lower($2) AND status='pending' AND expires_at>NOW()
		LIMIT 1
	`, workspaceID, email).Scan(&pendingUpdatedAt)
	pendingExists := pendingErr == nil
	if pendingErr != nil && !errors.Is(pendingErr, sql.ErrNoRows) {
		return Member{}, "", pendingErr
	}
	if pendingExists {
		if retryAfter := invitationCooldownRemaining(pendingUpdatedAt, time.Now().UTC()); retryAfter > 0 {
			return Member{}, "", &InvitationResendTooSoonError{RetryAfter: retryAfter}
		}
	}
	if !pendingExists && memberLimit > 0 {
		var seatsUsed int
		if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM workspace_memberships
				WHERE workspace_id=$1 AND status='active'
			`, workspaceID).Scan(&seatsUsed); err != nil {
			return Member{}, "", err
		}
		if seatsUsed >= memberLimit {
			return Member{}, "", ErrMemberLimitReached
		}
	}

	var invitationID int64
	err = tx.QueryRowContext(ctx, `
			INSERT INTO workspace_invitations (
				workspace_id, email, role, status, invited_by, token_hash, department_ids, expires_at
			) VALUES ($1,$2,$3,'pending',$4,$5,$6,$7)
			ON CONFLICT (workspace_id, (lower(email))) WHERE status='pending' DO UPDATE SET
				invited_by=EXCLUDED.invited_by,
				role=EXCLUDED.role,
				token_hash=EXCLUDED.token_hash,
				department_ids=EXCLUDED.department_ids,
				expires_at=EXCLUDED.expires_at,
				updated_at=NOW()
			RETURNING id
		`, workspaceID, email, role, invitedBy, hex.EncodeToString(tokenHash[:]), pq.Array(departmentIDs), expiresAt).Scan(&invitationID)
	if err != nil {
		return Member{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Member{}, "", err
	}
	return Member{
		ID: invitationID, Kind: "invitation", Email: email, Role: role,
		DepartmentIDs: departmentIDs,
		Status:        invitationPending, ExpiresAt: &expiresAt, CreatedAt: time.Now().UTC(),
		CanBeRemoved: true, CanChangeRole: true,
	}, token, nil
}

func (s *Store) AcceptInvitation(ctx context.Context, userID int, token string) error {
	tokenHash := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(tokenHash[:])
	var workspaceID int
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT invitation.workspace_id
		FROM workspace_invitations invitation
		JOIN users ON users.id=$1
		WHERE invitation.token_hash=$2
			AND lower(invitation.email)=lower(users.email)
			AND invitation.status='pending'
			AND invitation.expires_at > NOW()
	`, userID, hash).Scan(&workspaceID); err != nil {
		return err
	}
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceID); err != nil {
		return err
	}
	var role string
	var departmentIDs []int
	err = tx.QueryRowContext(ctx, `
		SELECT invitation.role, invitation.department_ids
		FROM workspace_invitations invitation
		JOIN users ON users.id=$1
		WHERE invitation.token_hash=$2
			AND invitation.workspace_id=$3
			AND lower(invitation.email)=lower(users.email)
			AND invitation.status='pending'
			AND invitation.expires_at > NOW()
		FOR UPDATE
	`, userID, hash, workspaceID).Scan(&role, pq.Array(&departmentIDs))
	if err != nil {
		return err
	}
	if role != roleAdmin && role != roleMember {
		return ErrInvalidMemberRole
	}
	var alreadyMember bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM workspace_memberships
			WHERE workspace_id=$1 AND user_id=$2 AND status='active'
		)
	`, workspaceID, userID).Scan(&alreadyMember); err != nil {
		return err
	}
	var memberLimit int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE((
			SELECT subscription.member_limit
			FROM subscriptions subscription
			JOIN workspaces workspace ON workspace.id=$1
			WHERE subscription.workspace_id=$1
				OR (subscription.workspace_id IS NULL AND subscription.user_id=workspace.owner_user_id)
			ORDER BY CASE WHEN subscription.workspace_id=$1 THEN 0 ELSE 1 END, subscription.updated_at DESC
			LIMIT 1
		), 5)
	`, workspaceID).Scan(&memberLimit); err != nil {
		return err
	}
	if !alreadyMember && memberLimit > 0 {
		var activeMembers int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM workspace_memberships
			WHERE workspace_id=$1 AND status='active'
		`, workspaceID).Scan(&activeMembers); err != nil {
			return err
		}
		if activeMembers >= memberLimit {
			return ErrMemberLimitReached
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_memberships SET is_default=FALSE, updated_at=NOW()
		WHERE user_id=$1 AND status='active'
	`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_memberships (workspace_id, user_id, role, status, is_default)
		VALUES ($1,$2,$3,'active',TRUE)
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET
			role=EXCLUDED.role, status='active', is_default=TRUE, updated_at=NOW()
	`, workspaceID, userID, role); err != nil {
		return err
	}
	for _, departmentID := range dedupePositiveIDs(departmentIDs) {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_department_members (department_id, workspace_id, user_id, role)
			SELECT id, workspace_id, $3, 'member'
			FROM v2_departments
			WHERE id=$1 AND workspace_id=$2 AND status='active'
			ON CONFLICT (department_id, user_id) DO NOTHING
		`, departmentID, workspaceID, userID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET workspace_onboarding_mode='complete' WHERE id=$1
	`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_invitations
		SET status='accepted', accepted_by=$1, accepted_at=NOW(), updated_at=NOW()
		WHERE token_hash=$2 AND status='pending'
	`, userID, hex.EncodeToString(tokenHash[:])); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PreviewInvitation(ctx context.Context, token string) (InvitationPreview, error) {
	token = strings.TrimSpace(token)
	if len(token) != 64 {
		return InvitationPreview{}, sql.ErrNoRows
	}
	tokenHash := sha256.Sum256([]byte(token))
	result := InvitationPreview{
		DepartmentIDs:   []int{},
		DepartmentNames: []string{},
	}
	err := s.dbx.QueryRowContext(ctx, `
		SELECT
			COALESCE(NULLIF(workspace.display_name, ''), workspace.name),
			invitation.email,
			COALESCE(NULLIF(inviter.name, ''), inviter.email, 'Команда REUP.goals'),
			COALESCE(inviter.email, ''),
			invitation.role,
			invitation.department_ids,
			invitation.expires_at
		FROM workspace_invitations invitation
		JOIN workspaces workspace ON workspace.id=invitation.workspace_id
		LEFT JOIN users inviter ON inviter.id=invitation.invited_by
		WHERE invitation.token_hash=$1
			AND invitation.status='pending'
			AND invitation.expires_at>NOW()
			AND workspace.status='active'
	`, hex.EncodeToString(tokenHash[:])).Scan(
		&result.WorkspaceName,
		&result.InvitedEmail,
		&result.InviterName,
		&result.InviterEmail,
		&result.Role,
		pq.Array(&result.DepartmentIDs),
		&result.ExpiresAt,
	)
	if err != nil {
		return InvitationPreview{}, err
	}
	if len(result.DepartmentIDs) > 0 {
		rows, err := s.dbx.QueryContext(ctx, `
			SELECT name
			FROM v2_departments
			WHERE id=ANY($1) AND status='active'
			ORDER BY sort_order, lower(name)
		`, pq.Array(result.DepartmentIDs))
		if err != nil {
			return InvitationPreview{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return InvitationPreview{}, err
			}
			result.DepartmentNames = append(result.DepartmentNames, name)
		}
		if err := rows.Err(); err != nil {
			return InvitationPreview{}, err
		}
	}
	return result, nil
}

func invitationCooldownRemaining(lastSentAt, now time.Time) time.Duration {
	remaining := invitationResendCooldown - now.Sub(lastSentAt)
	if remaining <= 0 {
		return 0
	}
	if remaining > invitationResendCooldown {
		return invitationResendCooldown
	}
	return remaining
}

func dedupePositiveIDs(values []int) []int {
	result := make([]int, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (s *Store) RemoveMember(ctx context.Context, workspaceID, actorUserID int, kind string, id int64) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var result sql.Result
	switch kind {
	case "membership":
		var userID int
		if err := tx.QueryRowContext(ctx, `
			SELECT user_id FROM workspace_memberships
			WHERE id=$1 AND workspace_id=$2 AND role <> 'owner' AND user_id <> $3
			FOR UPDATE
		`, id, workspaceID, actorUserID).Scan(&userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM v2_department_members WHERE workspace_id=$1 AND user_id=$2
		`, workspaceID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE v2_departments SET manager_user_id=NULL, updated_at=NOW()
			WHERE workspace_id=$1 AND manager_user_id=$2
		`, workspaceID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE v2_tasks SET owner_user_id=NULL, updated_at=NOW()
			WHERE workspace_id=$1 AND owner_user_id=$2
		`, workspaceID, userID); err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `
			DELETE FROM workspace_memberships
			WHERE id=$1 AND workspace_id=$2 AND role <> 'owner'
		`, id, workspaceID)
	case "invitation":
		result, err = tx.ExecContext(ctx, `
			UPDATE workspace_invitations
			SET status='cancelled', cancelled_at=NOW(), updated_at=NOW()
			WHERE id=$1 AND workspace_id=$2 AND status='pending'
		`, id, workspaceID)
	default:
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) UpdateMemberRole(
	ctx context.Context,
	workspaceID, actorUserID int,
	membershipID int64,
	role string,
) (Member, error) {
	if role != roleAdmin && role != roleMember {
		return Member{}, ErrInvalidMemberRole
	}
	var item Member
	var userID int
	err := s.dbx.QueryRowContext(ctx, `
		UPDATE workspace_memberships membership
		SET role=$1, updated_at=NOW()
		FROM users
		WHERE membership.id=$2 AND membership.workspace_id=$3
			AND membership.role <> 'owner' AND membership.user_id <> $4
			AND users.id=membership.user_id
			RETURNING membership.id, users.id, users.name, users.email, users.avatar_url,
				users.company_role, membership.role, membership.created_at
	`, role, membershipID, workspaceID, actorUserID).Scan(
		&item.ID, &userID, &item.Name, &item.Email, &item.AvatarURL,
		&item.CompanyRole, &item.Role, &item.CreatedAt,
	)
	if err != nil {
		return Member{}, err
	}
	item.Kind = "membership"
	item.UserID = &userID
	item.Status = invitationAccepted
	item.CanBeRemoved = true
	item.CanChangeRole = true
	return item, nil
}

func (s *Store) BillingOrganization(ctx context.Context, workspaceID int) (*BillingOrganization, error) {
	var result BillingOrganization
	err := s.dbx.QueryRowContext(ctx, `
		SELECT full_name, inn, kpp, registration_number, legal_address, accounting_email, contact_person
		FROM workspace_billing_organizations WHERE workspace_id=$1
	`, workspaceID).Scan(&result.FullName, &result.INN, &result.KPP, &result.RegistrationNumber,
		&result.LegalAddress, &result.AccountingEmail, &result.ContactPerson)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &result, err
}

func (s *Store) SaveBillingOrganization(ctx context.Context, workspaceID, userID int, value BillingOrganization) (BillingOrganization, error) {
	var result BillingOrganization
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO workspace_billing_organizations (
			workspace_id, full_name, inn, kpp, registration_number, legal_address,
			accounting_email, contact_person, created_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (workspace_id) DO UPDATE SET
			full_name=EXCLUDED.full_name, inn=EXCLUDED.inn, kpp=EXCLUDED.kpp,
			registration_number=EXCLUDED.registration_number,
			legal_address=EXCLUDED.legal_address,
			accounting_email=EXCLUDED.accounting_email,
			contact_person=EXCLUDED.contact_person,
			updated_at=NOW()
		RETURNING full_name, inn, kpp, registration_number, legal_address, accounting_email, contact_person
	`, workspaceID, value.FullName, value.INN, value.KPP, value.RegistrationNumber,
		value.LegalAddress, value.AccountingEmail, value.ContactPerson, userID).Scan(
		&result.FullName, &result.INN, &result.KPP, &result.RegistrationNumber,
		&result.LegalAddress, &result.AccountingEmail, &result.ContactPerson,
	)
	return result, err
}

func (s *Store) CreateInvoice(ctx context.Context, workspaceID, userID int, amount float64, currency string) (Invoice, error) {
	organization, err := s.BillingOrganization(ctx, workspaceID)
	if err != nil {
		return Invoice{}, err
	}
	if organization == nil {
		return Invoice{}, errors.New("billing_organization_required")
	}
	snapshot, err := json.Marshal(organization)
	if err != nil {
		return Invoice{}, err
	}

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Invoice{}, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT nextval('workspace_billing_invoices_id_seq')`).Scan(&id); err != nil {
		return Invoice{}, err
	}
	now := time.Now().UTC()
	dueAt := now.Add(5 * 24 * time.Hour)
	number := fmt.Sprintf("REUP-%d-%06d", now.Year(), id)
	invoice := Invoice{
		ID: id, Number: number, Amount: amount, Currency: currency, Status: "waiting",
		RecipientEmail: organization.AccountingEmail, IssuedAt: now, DueAt: dueAt,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_billing_invoices (
			id, workspace_id, number, amount, currency, status, organization_snapshot,
			recipient_email, issued_by, issued_at, due_at
		) VALUES ($1,$2,$3,$4,$5,'waiting',$6,$7,$8,$9,$10)
	`, id, workspaceID, number, amount, currency, snapshot, organization.AccountingEmail, userID, now, dueAt); err != nil {
		return Invoice{}, err
	}
	pdf := BuildInvoicePDF(invoice, *organization)
	var documentID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO workspace_billing_documents (
			workspace_id, invoice_id, kind, title, file_name, mime_type, content
		) VALUES ($1,$2,'invoice',$3,$4,'application/pdf',$5)
		RETURNING id
	`, workspaceID, id, "Счёт "+number, "invoice-"+number+".pdf", pdf).Scan(&documentID); err != nil {
		return Invoice{}, err
	}
	if err := tx.Commit(); err != nil {
		return Invoice{}, err
	}
	invoice.DocumentID = &documentID
	return invoice, nil
}

func (s *Store) Invoices(ctx context.Context, workspaceID int) ([]Invoice, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT invoice.id, invoice.number, invoice.amount, invoice.currency,
			CASE WHEN invoice.status='waiting' AND invoice.due_at <= NOW() THEN 'expired' ELSE invoice.status END,
			invoice.recipient_email, invoice.issued_at, invoice.due_at, invoice.paid_at, invoice.emailed_at,
			document.id
		FROM workspace_billing_invoices invoice
		LEFT JOIN LATERAL (
			SELECT id FROM workspace_billing_documents
			WHERE invoice_id=invoice.id AND kind='invoice'
			ORDER BY created_at DESC LIMIT 1
		) document ON TRUE
		WHERE invoice.workspace_id=$1
		ORDER BY invoice.issued_at DESC, invoice.id DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Invoice, 0)
	for rows.Next() {
		var item Invoice
		var paidAt, emailedAt sql.NullTime
		var documentID sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Number, &item.Amount, &item.Currency, &item.Status,
			&item.RecipientEmail, &item.IssuedAt, &item.DueAt, &paidAt, &emailedAt, &documentID); err != nil {
			return nil, err
		}
		item.PaidAt = nullableTime(paidAt)
		item.EmailedAt = nullableTime(emailedAt)
		if documentID.Valid {
			value := documentID.Int64
			item.DocumentID = &value
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Documents(ctx context.Context, workspaceID int) ([]BillingDocument, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, invoice_id, kind, title, file_name, mime_type, period_start, period_end, created_at
		FROM workspace_billing_documents
		WHERE workspace_id=$1
		ORDER BY created_at DESC, id DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]BillingDocument, 0)
	for rows.Next() {
		var item BillingDocument
		var invoiceID sql.NullInt64
		var periodStart, periodEnd sql.NullTime
		if err := rows.Scan(&item.ID, &invoiceID, &item.Kind, &item.Title, &item.FileName,
			&item.MimeType, &periodStart, &periodEnd, &item.CreatedAt); err != nil {
			return nil, err
		}
		if invoiceID.Valid {
			value := invoiceID.Int64
			item.InvoiceID = &value
		}
		item.PeriodStart = nullableTime(periodStart)
		item.PeriodEnd = nullableTime(periodEnd)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) DocumentContent(ctx context.Context, workspaceID int, documentID int64) (string, string, []byte, error) {
	var fileName, mimeType string
	var content []byte
	err := s.dbx.QueryRowContext(ctx, `
		SELECT file_name, mime_type, content
		FROM workspace_billing_documents
		WHERE id=$1 AND workspace_id=$2
	`, documentID, workspaceID).Scan(&fileName, &mimeType, &content)
	return fileName, mimeType, content, err
}

func (s *Store) MarkInvoiceEmailed(ctx context.Context, workspaceID int, invoiceID int64) (Invoice, error) {
	var item Invoice
	var paidAt, emailedAt sql.NullTime
	err := s.dbx.QueryRowContext(ctx, `
		UPDATE workspace_billing_invoices SET emailed_at=NOW(), updated_at=NOW()
		WHERE id=$1 AND workspace_id=$2
		RETURNING id, number, amount, currency, status, recipient_email, issued_at, due_at, paid_at, emailed_at
	`, invoiceID, workspaceID).Scan(&item.ID, &item.Number, &item.Amount, &item.Currency,
		&item.Status, &item.RecipientEmail, &item.IssuedAt, &item.DueAt, &paidAt, &emailedAt)
	item.PaidAt = nullableTime(paidAt)
	item.EmailedAt = nullableTime(emailedAt)
	return item, err
}

func (s *Store) Payments(ctx context.Context, workspaceID int) ([]Payment, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, invoice_id, provider, method, amount, currency, status, paid_at, created_at
		FROM (
			SELECT payment.id, payment.invoice_id, payment.provider, payment.method,
				payment.amount, payment.currency, payment.status, payment.paid_at, payment.created_at
			FROM workspace_billing_payments payment
			WHERE payment.workspace_id=$1

			UNION ALL

			SELECT -(event.id::BIGINT), NULL::BIGINT, 'cloudpayments', 'card',
				COALESCE(event.amount, 0), COALESCE(NULLIF(event.currency, ''), 'RUB'),
				CASE event.event_type
					WHEN 'pay' THEN 'paid'
					WHEN 'recurrent' THEN 'paid'
					WHEN 'fail' THEN 'failed'
					WHEN 'cancel' THEN 'cancelled'
					ELSE event.event_type
				END,
				CASE WHEN event.event_type IN ('pay', 'recurrent') THEN event.created_at ELSE NULL END,
				event.created_at
			FROM payment_events event
			JOIN subscriptions subscription ON subscription.id=event.subscription_id
			JOIN workspaces workspace ON workspace.id=$1
			WHERE subscription.workspace_id=$1
				OR (subscription.workspace_id IS NULL AND subscription.user_id=workspace.owner_user_id)
		) history
		ORDER BY created_at DESC, id DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Payment, 0)
	for rows.Next() {
		var item Payment
		var invoiceID sql.NullInt64
		var paidAt sql.NullTime
		if err := rows.Scan(&item.ID, &invoiceID, &item.Provider, &item.Method, &item.Amount,
			&item.Currency, &item.Status, &paidAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		if invoiceID.Valid {
			value := invoiceID.Int64
			item.InvoiceID = &value
		}
		item.PaidAt = nullableTime(paidAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) DeleteWorkspace(ctx context.Context, workspaceID, userID int) error {
	result, err := s.dbx.ExecContext(ctx, `
		DELETE FROM workspaces WHERE id=$1 AND owner_user_id=$2
	`, workspaceID, userID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func nullableTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func subscriptionDisplayStatus(status string, periodEnd, graceUntil *time.Time) string {
	now := time.Now().UTC()
	if status == "past_due" && graceUntil != nil && now.Before(*graceUntil) {
		return "grace_period"
	}
	if (status == "active" || status == "trial_active" || status == "cancelled") && periodEnd != nil {
		if periodEnd.After(now) && periodEnd.Sub(now) <= 7*24*time.Hour {
			return "expires_soon"
		}
	}
	if status == "past_due" || status == "expired" {
		return "suspended"
	}
	return status
}
