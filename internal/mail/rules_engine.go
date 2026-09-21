package mail

import (
	"slices"
	"strconv"
	"strings"
)

type RuleResult struct {
	TargetFolder string // Mailbox name e.g. "Archive", "Trash" (empty means default)
	MarkRead     bool
	Star         bool
	Discard      bool
}

type RuleEngine struct{}

func NewRuleEngine() *RuleEngine {
	return &RuleEngine{}
}

func (e *RuleEngine) Evaluate(rules []Rule, msg *Message, parsedBody string, hasAttachments bool) RuleResult {
	if len(rules) == 0 {
		return RuleResult{}
	}

	// 1. Filter enabled rules
	enabledRules := make([]Rule, 0, len(rules))
	for _, r := range rules {
		if r.Enabled {
			enabledRules = append(enabledRules, r)
		}
	}

	// 2. Sort by Priority ascending, preserving stable order
	slices.SortStableFunc(enabledRules, func(a, b Rule) int {
		if a.Priority < b.Priority {
			return -1
		}
		if a.Priority > b.Priority {
			return 1
		}
		return 0
	})

	var res RuleResult

	// 3. Evaluate each rule
	for _, rule := range enabledRules {
		if e.evaluateRule(rule, msg, parsedBody, hasAttachments) {
			// Apply actions
			for _, action := range rule.Actions {
				switch action.Type {
				case RuleActionMoveToFolder:
					res.TargetFolder = action.Target
				case RuleActionMarkRead:
					res.MarkRead = true
				case RuleActionStar:
					res.Star = true
				case RuleActionMoveToTrash:
					res.TargetFolder = "Trash"
				case RuleActionDelete:
					res.Discard = true
				}
			}

			// If StopProcessing is true, halt evaluation immediately
			if rule.StopProcessing {
				break
			}
		}
	}

	return res
}

func (e *RuleEngine) evaluateRule(rule Rule, msg *Message, parsedBody string, hasAttachments bool) bool {
	if len(rule.Conditions) == 0 {
		return false
	}

	if rule.MatchMode == "all" {
		for _, cond := range rule.Conditions {
			if !e.evaluateCondition(cond, msg, parsedBody, hasAttachments) {
				return false
			}
		}
		return true
	} else {
		// Default or "any": at least one must be true
		for _, cond := range rule.Conditions {
			if e.evaluateCondition(cond, msg, parsedBody, hasAttachments) {
				return true
			}
		}
		return false
	}
}

func (e *RuleEngine) evaluateCondition(cond RuleCondition, msg *Message, parsedBody string, hasAttachments bool) bool {
	var values []string

	switch cond.Field {
	case RuleFieldFrom:
		if msg != nil {
			values = []string{msg.Sender}
		} else {
			values = []string{""}
		}
	case RuleFieldTo:
		if msg != nil {
			values = msg.Recipients
		}
	case RuleFieldSubject:
		if msg != nil {
			values = []string{msg.Subject}
		} else {
			values = []string{""}
		}
	case RuleFieldBody:
		values = []string{parsedBody}
	case RuleFieldHasAttachment:
		values = []string{strconv.FormatBool(hasAttachments)}
	default:
		return false
	}

	match := func(val string) bool {
		vLower := strings.ToLower(val)
		cLower := strings.ToLower(cond.Value)

		switch cond.Operator {
		case RuleOperatorContains:
			return strings.Contains(vLower, cLower)
		case RuleOperatorNotContains:
			return !strings.Contains(vLower, cLower)
		case RuleOperatorEquals:
			return vLower == cLower
		case RuleOperatorNotEquals:
			return vLower != cLower
		case RuleOperatorStartsWith:
			return strings.HasPrefix(vLower, cLower)
		case RuleOperatorEndsWith:
			return strings.HasSuffix(vLower, cLower)
		case RuleOperatorIs:
			return vLower == cLower
		default:
			return false
		}
	}

	isNegated := cond.Operator == RuleOperatorNotContains || cond.Operator == RuleOperatorNotEquals
	if isNegated {
		if len(values) == 0 {
			return false
		}
		for _, v := range values {
			if !match(v) {
				return false
			}
		}
		return true
	}

	for _, v := range values {
		if match(v) {
			return true
		}
	}

	return false
}
