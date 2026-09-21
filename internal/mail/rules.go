package mail

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type RuleField string

const (
	RuleFieldFrom          RuleField = "from"
	RuleFieldTo            RuleField = "to"
	RuleFieldSubject       RuleField = "subject"
	RuleFieldBody          RuleField = "body"
	RuleFieldHasAttachment RuleField = "has_attachment"
)

type RuleOperator string

const (
	RuleOperatorContains    RuleOperator = "contains"
	RuleOperatorNotContains RuleOperator = "not_contains"
	RuleOperatorEquals      RuleOperator = "equals"
	RuleOperatorNotEquals   RuleOperator = "not_equals"
	RuleOperatorStartsWith  RuleOperator = "starts_with"
	RuleOperatorEndsWith    RuleOperator = "ends_with"
	RuleOperatorIs          RuleOperator = "is"
)

type RuleActionType string

const (
	RuleActionMoveToFolder RuleActionType = "move_to_folder"
	RuleActionMarkRead     RuleActionType = "mark_read"
	RuleActionStar         RuleActionType = "star"
	RuleActionMoveToTrash  RuleActionType = "move_to_trash"
	RuleActionDelete       RuleActionType = "delete"
)

type RuleCondition struct {
	Field    RuleField    `json:"field"`
	Operator RuleOperator `json:"operator"`
	Value    string       `json:"value"`
}

type RuleAction struct {
	Type   RuleActionType `json:"type"`
	Target string         `json:"target"`
}

type Rule struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Name           string
	Priority       int
	Enabled        bool
	MatchMode      string
	Conditions     []RuleCondition
	Actions        []RuleAction
	StopProcessing bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type RuleRepository interface {
	Create(ctx context.Context, rule *Rule) error
	GetByID(ctx context.Context, userID, id uuid.UUID) (*Rule, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]Rule, error)
	ListEnabled(ctx context.Context, userID uuid.UUID) ([]Rule, error)
	Update(ctx context.Context, rule *Rule) error
	SetEnabled(ctx context.Context, userID uuid.UUID, id uuid.UUID, enabled bool) error
	Reorder(ctx context.Context, userID uuid.UUID, orderedIDs []uuid.UUID) error
	Delete(ctx context.Context, userID uuid.UUID, id uuid.UUID) error
}
