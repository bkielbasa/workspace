package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

type signatureRepository struct {
	db *sql.DB
}

func NewSignatureRepository(db *sql.DB) mail.SignatureRepository {
	return &signatureRepository{db: db}
}

func (r *signatureRepository) Create(ctx context.Context, sig *mail.Signature) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if sig.IsDefault {
		_, err = tx.ExecContext(ctx, `
			UPDATE mail_signatures SET is_default = FALSE WHERE user_id = $1
		`, sig.UserID)
		if err != nil {
			return fmt.Errorf("clear other defaults: %w", err)
		}
	}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO mail_signatures (user_id, name, content, is_default)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at
	`, sig.UserID, sig.Name, sig.Content, sig.IsDefault).Scan(&sig.ID, &sig.CreatedAt, &sig.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert signature: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (r *signatureRepository) GetByID(ctx context.Context, id uuid.UUID) (*mail.Signature, error) {
	var sig mail.Signature
	err := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, content, is_default, created_at, updated_at
		FROM mail_signatures WHERE id = $1
	`, id).Scan(&sig.ID, &sig.UserID, &sig.Name, &sig.Content, &sig.IsDefault, &sig.CreatedAt, &sig.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, mail.ErrSignatureNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get signature: %w", err)
	}
	return &sig, nil
}

func (r *signatureRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]mail.Signature, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, name, content, is_default, created_at, updated_at
		FROM mail_signatures WHERE user_id = $1 ORDER BY created_at ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list signatures: %w", err)
	}
	defer rows.Close()

	var sigs []mail.Signature
	for rows.Next() {
		var sig mail.Signature
		err := rows.Scan(&sig.ID, &sig.UserID, &sig.Name, &sig.Content, &sig.IsDefault, &sig.CreatedAt, &sig.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan signature: %w", err)
		}
		sigs = append(sigs, sig)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate signatures: %w", err)
	}
	return sigs, nil
}

func (r *signatureRepository) GetDefault(ctx context.Context, userID uuid.UUID) (*mail.Signature, error) {
	var sig mail.Signature
	err := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, content, is_default, created_at, updated_at
		FROM mail_signatures WHERE user_id = $1 AND is_default = TRUE
	`, userID).Scan(&sig.ID, &sig.UserID, &sig.Name, &sig.Content, &sig.IsDefault, &sig.CreatedAt, &sig.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, mail.ErrSignatureNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get default signature: %w", err)
	}
	return &sig, nil
}

func (r *signatureRepository) Update(ctx context.Context, sig *mail.Signature) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if sig.IsDefault {
		_, err = tx.ExecContext(ctx, `
			UPDATE mail_signatures SET is_default = FALSE WHERE user_id = $1
		`, sig.UserID)
		if err != nil {
			return fmt.Errorf("clear other defaults: %w", err)
		}
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE mail_signatures
		SET name = $1, content = $2, is_default = $3, updated_at = NOW()
		WHERE id = $4 AND user_id = $5
	`, sig.Name, sig.Content, sig.IsDefault, sig.ID, sig.UserID)
	if err != nil {
		return fmt.Errorf("update signature: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return mail.ErrSignatureNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (r *signatureRepository) SetDefault(ctx context.Context, userID uuid.UUID, id uuid.UUID) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		UPDATE mail_signatures SET is_default = FALSE WHERE user_id = $1
	`, userID)
	if err != nil {
		return fmt.Errorf("clear other defaults: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE mail_signatures SET is_default = TRUE, updated_at = NOW()
		WHERE user_id = $1 AND id = $2
	`, userID, id)
	if err != nil {
		return fmt.Errorf("set default signature: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return mail.ErrSignatureNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (r *signatureRepository) Delete(ctx context.Context, userID uuid.UUID, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM mail_signatures WHERE user_id = $1 AND id = $2
	`, userID, id)
	if err != nil {
		return fmt.Errorf("delete signature: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return mail.ErrSignatureNotFound
	}
	return nil
}

type ruleRepository struct {
	db *sql.DB
}

func NewRuleRepository(db *sql.DB) mail.RuleRepository {
	return &ruleRepository{db: db}
}

func (r *ruleRepository) Create(ctx context.Context, rule *mail.Rule) error {
	condsJSON, err := json.Marshal(rule.Conditions)
	if err != nil {
		return fmt.Errorf("marshal conditions: %w", err)
	}

	actsJSON, err := json.Marshal(rule.Actions)
	if err != nil {
		return fmt.Errorf("marshal actions: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `
		INSERT INTO mail_rules (user_id, name, priority, enabled, match_mode, conditions, actions, stop_processing)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at
	`, rule.UserID, rule.Name, rule.Priority, rule.Enabled, rule.MatchMode, condsJSON, actsJSON, rule.StopProcessing).Scan(&rule.ID, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert rule: %w", err)
	}
	return nil
}

func (r *ruleRepository) GetByID(ctx context.Context, id uuid.UUID) (*mail.Rule, error) {
	var rule mail.Rule
	var condsBytes, actsBytes []byte

	err := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, priority, enabled, match_mode, conditions, actions, stop_processing, created_at, updated_at
		FROM mail_rules WHERE id = $1
	`, id).Scan(&rule.ID, &rule.UserID, &rule.Name, &rule.Priority, &rule.Enabled, &rule.MatchMode, &condsBytes, &actsBytes, &rule.StopProcessing, &rule.CreatedAt, &rule.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, mail.ErrRuleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get rule: %w", err)
	}

	if err := json.Unmarshal(condsBytes, &rule.Conditions); err != nil {
		return nil, fmt.Errorf("unmarshal conditions: %w", err)
	}

	if err := json.Unmarshal(actsBytes, &rule.Actions); err != nil {
		return nil, fmt.Errorf("unmarshal actions: %w", err)
	}

	return &rule, nil
}

func (r *ruleRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]mail.Rule, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, name, priority, enabled, match_mode, conditions, actions, stop_processing, created_at, updated_at
		FROM mail_rules WHERE user_id = $1 ORDER BY priority ASC, created_at ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer rows.Close()

	var rules []mail.Rule
	for rows.Next() {
		var rule mail.Rule
		var condsBytes, actsBytes []byte

		err := rows.Scan(&rule.ID, &rule.UserID, &rule.Name, &rule.Priority, &rule.Enabled, &rule.MatchMode, &condsBytes, &actsBytes, &rule.StopProcessing, &rule.CreatedAt, &rule.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}

		if err := json.Unmarshal(condsBytes, &rule.Conditions); err != nil {
			return nil, fmt.Errorf("unmarshal conditions: %w", err)
		}

		if err := json.Unmarshal(actsBytes, &rule.Actions); err != nil {
			return nil, fmt.Errorf("unmarshal actions: %w", err)
		}

		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rules: %w", err)
	}
	return rules, nil
}

func (r *ruleRepository) ListEnabled(ctx context.Context, userID uuid.UUID) ([]mail.Rule, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, name, priority, enabled, match_mode, conditions, actions, stop_processing, created_at, updated_at
		FROM mail_rules WHERE user_id = $1 AND enabled = TRUE ORDER BY priority ASC, created_at ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list enabled rules: %w", err)
	}
	defer rows.Close()

	var rules []mail.Rule
	for rows.Next() {
		var rule mail.Rule
		var condsBytes, actsBytes []byte

		err := rows.Scan(&rule.ID, &rule.UserID, &rule.Name, &rule.Priority, &rule.Enabled, &rule.MatchMode, &condsBytes, &actsBytes, &rule.StopProcessing, &rule.CreatedAt, &rule.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan enabled rule: %w", err)
		}

		if err := json.Unmarshal(condsBytes, &rule.Conditions); err != nil {
			return nil, fmt.Errorf("unmarshal conditions: %w", err)
		}

		if err := json.Unmarshal(actsBytes, &rule.Actions); err != nil {
			return nil, fmt.Errorf("unmarshal actions: %w", err)
		}

		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled rules: %w", err)
	}
	return rules, nil
}

func (r *ruleRepository) Update(ctx context.Context, rule *mail.Rule) error {
	condsJSON, err := json.Marshal(rule.Conditions)
	if err != nil {
		return fmt.Errorf("marshal conditions: %w", err)
	}

	actsJSON, err := json.Marshal(rule.Actions)
	if err != nil {
		return fmt.Errorf("marshal actions: %w", err)
	}

	result, err := r.db.ExecContext(ctx, `
		UPDATE mail_rules
		SET name = $1, priority = $2, enabled = $3, match_mode = $4, conditions = $5, actions = $6, stop_processing = $7, updated_at = NOW()
		WHERE id = $8 AND user_id = $9
	`, rule.Name, rule.Priority, rule.Enabled, rule.MatchMode, condsJSON, actsJSON, rule.StopProcessing, rule.ID, rule.UserID)
	if err != nil {
		return fmt.Errorf("update rule: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return mail.ErrRuleNotFound
	}
	return nil
}

func (r *ruleRepository) SetEnabled(ctx context.Context, userID uuid.UUID, id uuid.UUID, enabled bool) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE mail_rules SET enabled = $1, updated_at = NOW()
		WHERE user_id = $2 AND id = $3
	`, enabled, userID, id)
	if err != nil {
		return fmt.Errorf("set enabled rule: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return mail.ErrRuleNotFound
	}
	return nil
}

func (r *ruleRepository) Reorder(ctx context.Context, userID uuid.UUID, orderedIDs []uuid.UUID) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	for index, id := range orderedIDs {
		result, err := tx.ExecContext(ctx, `
			UPDATE mail_rules SET priority = $1, updated_at = NOW()
			WHERE user_id = $2 AND id = $3
		`, index, userID, id)
		if err != nil {
			return fmt.Errorf("reorder update rule: %w", err)
		}

		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}
		if rows == 0 {
			return mail.ErrRuleNotFound
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (r *ruleRepository) Delete(ctx context.Context, userID uuid.UUID, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM mail_rules WHERE user_id = $1 AND id = $2
	`, userID, id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return mail.ErrRuleNotFound
	}
	return nil
}
