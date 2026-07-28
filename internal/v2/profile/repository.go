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
	_ "time/tzdata"

	"github.com/lib/pq"

	"reup-goals-backend/internal/v2/billing"
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

	type overviewPart struct {
		kind         string
		account      Account
		memberCount  int
		subscription SubscriptionSummary
		err          error
	}
	parts := make(chan overviewPart, 3)
	go func() {
		var account Account
		loadErr := s.dbx.QueryRowContext(ctx, `
			SELECT id, email, name, avatar_url, company_role FROM users WHERE id=$1
		`, userID).Scan(&account.ID, &account.Email, &account.Name, &account.AvatarURL, &account.CompanyRole)
		parts <- overviewPart{kind: "account", account: account, err: loadErr}
	}()
	go func() {
		var memberCount int
		loadErr := s.dbx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM workspace_memberships
			WHERE workspace_id=$1 AND status='active'
		`, workspace.ID).Scan(&memberCount)
		parts <- overviewPart{kind: "members", memberCount: memberCount, err: loadErr}
	}()
	go func() {
		subscription, loadErr := s.Subscription(ctx, workspace.ID, workspace.OwnerUserID, checkoutAvailable)
		parts <- overviewPart{kind: "subscription", subscription: subscription, err: loadErr}
	}()

	var account Account
	var memberCount int
	var subscription SubscriptionSummary
	for range 3 {
		part := <-parts
		if part.err != nil {
			return Overview{}, part.err
		}
		switch part.kind {
		case "account":
			account = part.account
		case "members":
			memberCount = part.memberCount
		case "subscription":
			subscription = part.subscription
		}
	}
	subscription.SeatsUsed = memberCount

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
		SELECT plan_name, plan_code, billing_period, status, amount, currency,
			payment_method, payment_provider, current_period_end, next_payment_at,
			grace_until, member_limit
		FROM subscriptions
		WHERE workspace_id=$1 OR (workspace_id IS NULL AND user_id=$2)
		ORDER BY CASE WHEN workspace_id=$1 THEN 0 ELSE 1 END, updated_at DESC
		LIMIT 1
	`, workspaceID, ownerUserID).Scan(
		&result.Plan, &result.PlanCode, &result.BillingPeriod, &result.Status,
		&result.Amount, &result.Currency, &result.PaymentMethod, &result.PaymentProvider,
		&periodEnd, &nextRenewal, &graceUntil, &result.MemberLimit,
	)
	if errors.Is(err, sql.ErrNoRows) {
		plan, _ := billing.PlanByCode(billing.PlanFounder)
		return SubscriptionSummary{
			Plan:              plan.Name,
			PlanCode:          plan.Code,
			BillingPeriod:     billing.PeriodMonthly,
			Status:            "inactive",
			Amount:            plan.MonthlyAmount,
			AnnualAmount:      plan.AnnualAmount,
			ResetAmount:       plan.ResetAmount,
			Currency:          plan.Currency,
			DisplayStatus:     "inactive",
			CheckoutAvailable: checkoutAvailable,
			MemberLimit:       plan.MemberLimit,
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
				SELECT
					(SELECT COUNT(*) FROM workspace_memberships
					 WHERE workspace_id=$1 AND status='active')
					+
					(SELECT COUNT(*) FROM workspace_invitations
					 WHERE workspace_id=$1
					   AND status='pending'
					   AND expires_at>NOW())
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

func (s *Store) SellerProfile(ctx context.Context) (SellerProfile, error) {
	var result SellerProfile
	err := s.dbx.QueryRowContext(ctx, `
		SELECT full_name, inn, kpp, registration_number, legal_address, bank_name,
			settlement_account, correspondent_account, bic, director_name,
			accounting_email, tax_label
		FROM billing_seller_profiles
		WHERE id=1
	`).Scan(
		&result.FullName, &result.INN, &result.KPP, &result.RegistrationNumber,
		&result.LegalAddress, &result.BankName, &result.SettlementAccount,
		&result.CorrespondentAccount, &result.BIC, &result.DirectorName,
		&result.AccountingEmail, &result.TaxLabel,
	)
	return result, err
}

func (s *Store) CreateInvoice(
	ctx context.Context,
	workspaceID, userID int,
	request InvoiceRequest,
) (Invoice, error) {
	organization, err := s.BillingOrganization(ctx, workspaceID)
	if err != nil {
		return Invoice{}, err
	}
	if organization == nil {
		return Invoice{}, errors.New("billing_organization_required")
	}
	seller, err := s.SellerProfile(ctx)
	if err != nil {
		return Invoice{}, err
	}
	buyerSnapshot, err := json.Marshal(organization)
	if err != nil {
		return Invoice{}, err
	}
	sellerSnapshot, err := json.Marshal(seller)
	if err != nil {
		return Invoice{}, err
	}
	plan, err := billing.PlanByCode(request.PlanCode)
	if err != nil {
		return Invoice{}, err
	}
	request.PlanCode = plan.Code
	request.BillingPeriod = strings.ToLower(strings.TrimSpace(request.BillingPeriod))
	request.OrderKind = strings.ToLower(strings.TrimSpace(request.OrderKind))
	amount, err := billing.Price(plan, request.BillingPeriod)
	if err != nil {
		return Invoice{}, err
	}
	description := fmt.Sprintf("Подписка REUP.goals, тариф %s, оплата за месяц", plan.Name)
	if request.BillingPeriod == billing.PeriodAnnual {
		description = fmt.Sprintf("Подписка REUP.goals, тариф %s, оплата за год со скидкой 20%%", plan.Name)
	}
	if request.OrderKind == billing.OrderQuotaReset {
		request.BillingPeriod = billing.PeriodMonthly
		amount = plan.ResetAmount
		description = fmt.Sprintf("Сброс недельного AI-лимита REUP.goals, тариф %s", plan.Name)
	} else if request.OrderKind != billing.OrderSubscription {
		return Invoice{}, errors.New("billing_order_kind_invalid")
	}
	request.IdempotencyKey = normalizeInvoiceIdempotencyKey(
		request.IdempotencyKey,
		workspaceID,
		userID,
		plan.Code,
		request.BillingPeriod,
		request.OrderKind,
	)
	timezone, location, err := s.workspaceBillingLocation(ctx, workspaceID)
	if err != nil {
		return Invoice{}, err
	}

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Invoice{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceID); err != nil {
		return Invoice{}, err
	}
	existing, err := invoiceByIdempotencyKey(ctx, tx, workspaceID, request.IdempotencyKey)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Invoice{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Invoice{}, err
	}
	var orderID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO workspace_billing_orders (
			workspace_id, created_by, order_kind, plan_code, billing_period,
			amount, currency, status, provider, idempotency_key
		) VALUES ($1,$2,$3,$4,$5,$6,$7,'waiting','manual',$8)
		RETURNING id
	`, workspaceID, userID, request.OrderKind, plan.Code, request.BillingPeriod, amount, plan.Currency, request.IdempotencyKey).Scan(&orderID); err != nil {
		return Invoice{}, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT nextval('workspace_billing_invoices_id_seq')`).Scan(&id); err != nil {
		return Invoice{}, err
	}
	now := time.Now().In(location)
	dueAt := now.AddDate(0, 0, 5)
	number := fmt.Sprintf("REUP-%d-%06d", now.Year(), id)
	invoice := Invoice{
		ID: id, OrderID: &orderID, Number: number, OrderKind: request.OrderKind,
		PlanCode: plan.Code, BillingPeriod: request.BillingPeriod, Description: description,
		TaxLabel: seller.TaxLabel, Amount: amount, Currency: plan.Currency, Status: "waiting",
		RecipientEmail: organization.AccountingEmail, IssuedAt: now, DueAt: dueAt,
		IssuedDate: now.Format("02.01.2006"), DueDate: dueAt.Format("02.01.2006"),
		Timezone: timezone,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_billing_invoices (
			id, workspace_id, number, amount, currency, status, organization_snapshot,
			recipient_email, issued_by, issued_at, due_at, order_id, order_kind,
			plan_code, billing_period, description, tax_label, seller_snapshot
		) VALUES (
			$1,$2,$3,$4,$5,'waiting',$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17
		)
	`, id, workspaceID, number, amount, plan.Currency, buyerSnapshot, organization.AccountingEmail,
		userID, now, dueAt, orderID, request.OrderKind, plan.Code, request.BillingPeriod,
		description, seller.TaxLabel, sellerSnapshot); err != nil {
		return Invoice{}, err
	}
	pdf, err := BuildInvoicePDF(invoice, seller, *organization)
	if err != nil {
		return Invoice{}, err
	}
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
		SELECT invoice.id, invoice.order_id, invoice.number, invoice.order_kind,
			invoice.plan_code, invoice.billing_period, invoice.description, invoice.tax_label,
			invoice.amount, invoice.currency,
			CASE WHEN invoice.status='waiting' AND invoice.due_at <= NOW() THEN 'expired' ELSE invoice.status END,
			invoice.recipient_email, invoice.issued_at, invoice.due_at, invoice.paid_at, invoice.emailed_at,
			COALESCE(NULLIF(workspace.timezone, ''), 'Europe/Moscow'),
			document.id
		FROM workspace_billing_invoices invoice
		JOIN workspaces workspace ON workspace.id=invoice.workspace_id
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
		var orderID, documentID sql.NullInt64
		if err := rows.Scan(&item.ID, &orderID, &item.Number, &item.OrderKind,
			&item.PlanCode, &item.BillingPeriod, &item.Description, &item.TaxLabel,
			&item.Amount, &item.Currency, &item.Status,
			&item.RecipientEmail, &item.IssuedAt, &item.DueAt, &paidAt, &emailedAt,
			&item.Timezone, &documentID); err != nil {
			return nil, err
		}
		setInvoiceCalendarDates(&item)
		if orderID.Valid {
			value := orderID.Int64
			item.OrderID = &value
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
	var orderID, documentID sql.NullInt64
	err := s.dbx.QueryRowContext(ctx, `
		WITH updated AS (
			UPDATE workspace_billing_invoices SET emailed_at=NOW(), updated_at=NOW()
			WHERE id=$1 AND workspace_id=$2
			RETURNING *
		)
		SELECT invoice.id, invoice.order_id, invoice.number, invoice.order_kind,
			invoice.plan_code, invoice.billing_period, invoice.description, invoice.tax_label,
			invoice.amount, invoice.currency, invoice.status, invoice.recipient_email,
			invoice.issued_at, invoice.due_at, invoice.paid_at, invoice.emailed_at,
			COALESCE(NULLIF(workspace.timezone, ''), 'Europe/Moscow'), document.id
		FROM updated invoice
		JOIN workspaces workspace ON workspace.id=invoice.workspace_id
		LEFT JOIN LATERAL (
			SELECT id FROM workspace_billing_documents
			WHERE invoice_id=invoice.id AND kind='invoice'
			ORDER BY created_at DESC LIMIT 1
		) document ON TRUE
	`, invoiceID, workspaceID).Scan(
		&item.ID, &orderID, &item.Number, &item.OrderKind, &item.PlanCode,
		&item.BillingPeriod, &item.Description, &item.TaxLabel, &item.Amount,
		&item.Currency, &item.Status, &item.RecipientEmail, &item.IssuedAt,
		&item.DueAt, &paidAt, &emailedAt, &item.Timezone, &documentID,
	)
	if orderID.Valid {
		value := orderID.Int64
		item.OrderID = &value
	}
	item.PaidAt = nullableTime(paidAt)
	item.EmailedAt = nullableTime(emailedAt)
	if documentID.Valid {
		value := documentID.Int64
		item.DocumentID = &value
	}
	setInvoiceCalendarDates(&item)
	return item, err
}

type invoiceRowScanner interface {
	Scan(dest ...any) error
}

func invoiceByIdempotencyKey(ctx context.Context, tx *sql.Tx, workspaceID int, key string) (Invoice, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT invoice.id, invoice.order_id, invoice.number, invoice.order_kind,
			invoice.plan_code, invoice.billing_period, invoice.description, invoice.tax_label,
			invoice.amount, invoice.currency,
			CASE WHEN invoice.status='waiting' AND invoice.due_at <= NOW() THEN 'expired' ELSE invoice.status END,
			invoice.recipient_email, invoice.issued_at, invoice.due_at, invoice.paid_at, invoice.emailed_at,
			COALESCE(NULLIF(workspace.timezone, ''), 'Europe/Moscow'), document.id
		FROM workspace_billing_orders billing_order
		JOIN workspace_billing_invoices invoice ON invoice.order_id=billing_order.id
		JOIN workspaces workspace ON workspace.id=billing_order.workspace_id
		LEFT JOIN LATERAL (
			SELECT id FROM workspace_billing_documents
			WHERE invoice_id=invoice.id AND kind='invoice'
			ORDER BY created_at DESC LIMIT 1
		) document ON TRUE
		WHERE billing_order.workspace_id=$1 AND billing_order.idempotency_key=$2
		LIMIT 1
	`, workspaceID, key)
	return scanInvoice(row)
}

func scanInvoice(row invoiceRowScanner) (Invoice, error) {
	var item Invoice
	var orderID, documentID sql.NullInt64
	var paidAt, emailedAt sql.NullTime
	err := row.Scan(
		&item.ID, &orderID, &item.Number, &item.OrderKind,
		&item.PlanCode, &item.BillingPeriod, &item.Description, &item.TaxLabel,
		&item.Amount, &item.Currency, &item.Status, &item.RecipientEmail,
		&item.IssuedAt, &item.DueAt, &paidAt, &emailedAt, &item.Timezone, &documentID,
	)
	if err != nil {
		return Invoice{}, err
	}
	if orderID.Valid {
		value := orderID.Int64
		item.OrderID = &value
	}
	if documentID.Valid {
		value := documentID.Int64
		item.DocumentID = &value
	}
	item.PaidAt = nullableTime(paidAt)
	item.EmailedAt = nullableTime(emailedAt)
	setInvoiceCalendarDates(&item)
	return item, nil
}

func (s *Store) workspaceBillingLocation(ctx context.Context, workspaceID int) (string, *time.Location, error) {
	var timezone string
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(timezone, ''), 'Europe/Moscow')
		FROM workspaces WHERE id=$1
	`, workspaceID).Scan(&timezone); err != nil {
		return "", nil, err
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return "", nil, fmt.Errorf("workspace_timezone_invalid: %w", err)
	}
	return timezone, location, nil
}

func setInvoiceCalendarDates(invoice *Invoice) {
	location, err := time.LoadLocation(invoice.Timezone)
	if err != nil {
		location = time.UTC
	}
	invoice.IssuedDate = invoice.IssuedAt.In(location).Format("02.01.2006")
	invoice.DueDate = invoice.DueAt.In(location).Format("02.01.2006")
}

func normalizeInvoiceIdempotencyKey(value string, workspaceID, userID int, planCode, period, kind string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		if len(value) > 128 {
			value = value[:128]
		}
		return value
	}
	return fmt.Sprintf(
		"legacy:%d:%d:%s:%s:%s:%d",
		workspaceID,
		userID,
		planCode,
		period,
		kind,
		time.Now().UTC().Unix()/120,
	)
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
