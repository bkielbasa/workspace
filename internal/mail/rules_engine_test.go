package mail

import (
	"testing"

	"github.com/google/uuid"
)

func TestRuleEngine_DisabledRules(t *testing.T) {
	engine := NewRuleEngine()

	// A disabled rule that matches, but should be ignored.
	rules := []Rule{
		{
			ID:        uuid.New(),
			Name:      "Disabled rule",
			Enabled:   false,
			Priority:  1,
			MatchMode: "all",
			Conditions: []RuleCondition{
				{
					Field:    RuleFieldSubject,
					Operator: RuleOperatorContains,
					Value:    "test",
				},
			},
			Actions: []RuleAction{
				{
					Type: RuleActionStar,
				},
			},
		},
	}

	msg := &Message{
		Subject: "This is a test subject",
	}

	res := engine.Evaluate(rules, msg, "", false)
	if res.Star {
		t.Error("expected disabled rule to be skipped, but action was applied")
	}
}

func TestRuleEngine_CaseInsensitivity(t *testing.T) {
	engine := NewRuleEngine()

	rules := []Rule{
		{
			ID:        uuid.New(),
			Enabled:   true,
			Priority:  1,
			MatchMode: "all",
			Conditions: []RuleCondition{
				{
					Field:    RuleFieldFrom,
					Operator: RuleOperatorEquals,
					Value:    "ALICE@EXAMPLE.COM",
				},
			},
			Actions: []RuleAction{
				{
					Type: RuleActionStar,
				},
			},
		},
	}

	msg := &Message{
		Sender: "alice@example.com",
	}

	res := engine.Evaluate(rules, msg, "", false)
	if !res.Star {
		t.Error("expected case-insensitive match for equals operator")
	}
}

func TestRuleEngine_Operators(t *testing.T) {
	engine := NewRuleEngine()

	tests := []struct {
		name        string
		operator    RuleOperator
		value       string
		subject     string
		shouldMatch bool
	}{
		{"contains match", RuleOperatorContains, "apple", "an apple pie", true},
		{"contains no match", RuleOperatorContains, "banana", "an apple pie", false},
		{"not_contains match", RuleOperatorNotContains, "banana", "an apple pie", true},
		{"not_contains no match", RuleOperatorNotContains, "apple", "an apple pie", false},
		{"equals match", RuleOperatorEquals, "apple", "APPLE", true},
		{"equals no match", RuleOperatorEquals, "apple", "apple pie", false},
		{"not_equals match", RuleOperatorNotEquals, "apple", "banana", true},
		{"not_equals no match", RuleOperatorNotEquals, "apple", "APPLE", false},
		{"starts_with match", RuleOperatorStartsWith, "app", "apple", true},
		{"starts_with no match", RuleOperatorStartsWith, "pie", "apple", false},
		{"ends_with match", RuleOperatorEndsWith, "ple", "apple", true},
		{"ends_with no match", RuleOperatorEndsWith, "app", "apple", false},
		{"is match", RuleOperatorIs, "apple", "apple", true},
		{"is no match", RuleOperatorIs, "apple", "banana", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules := []Rule{
				{
					ID:        uuid.New(),
					Enabled:   true,
					Priority:  1,
					MatchMode: "all",
					Conditions: []RuleCondition{
						{
							Field:    RuleFieldSubject,
							Operator: tc.operator,
							Value:    tc.value,
						},
					},
					Actions: []RuleAction{
						{
							Type: RuleActionStar,
						},
					},
				},
			}

			msg := &Message{
				Subject: tc.subject,
			}

			res := engine.Evaluate(rules, msg, "", false)
			if res.Star != tc.shouldMatch {
				t.Errorf("operator %s (val: %s, subject: %s) match result: %t, expected %t",
					tc.operator, tc.value, tc.subject, res.Star, tc.shouldMatch)
			}
		})
	}
}

func TestRuleEngine_Fields(t *testing.T) {
	engine := NewRuleEngine()

	msg := &Message{
		Sender:     "alice@example.com",
		Recipients: []string{"bob@example.com", "charlie@example.com"},
		Subject:    "Project Update",
	}
	parsedBody := "This is the body content of the email."

	tests := []struct {
		name           string
		field          RuleField
		value          string
		hasAttachments bool
		shouldMatch    bool
	}{
		{"from field matches", RuleFieldFrom, "alice@example.com", false, true},
		{"from field no match", RuleFieldFrom, "bob@example.com", false, false},
		{"to field matches first recipient", RuleFieldTo, "bob@example.com", false, true},
		{"to field matches second recipient", RuleFieldTo, "charlie@example.com", false, true},
		{"to field no match", RuleFieldTo, "delta@example.com", false, false},
		{"subject field matches", RuleFieldSubject, "project", false, true},
		{"subject field no match", RuleFieldSubject, "urgent", false, false},
		{"body field matches", RuleFieldBody, "content", false, true},
		{"body field no match", RuleFieldBody, "missing", false, false},
		{"has_attachment true matches", RuleFieldHasAttachment, "true", true, true},
		{"has_attachment true no match", RuleFieldHasAttachment, "true", false, false},
		{"has_attachment false matches", RuleFieldHasAttachment, "false", false, true},
		{"has_attachment false no match", RuleFieldHasAttachment, "false", true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules := []Rule{
				{
					ID:        uuid.New(),
					Enabled:   true,
					Priority:  1,
					MatchMode: "all",
					Conditions: []RuleCondition{
						{
							Field:    tc.field,
							Operator: RuleOperatorContains, // We can use contains (or Is for attachment)
							Value:    tc.value,
						},
					},
					Actions: []RuleAction{
						{
							Type: RuleActionStar,
						},
					},
				},
			}

			// Adjust operator to Is for has_attachment as contains is not standard for booleans
			if tc.field == RuleFieldHasAttachment {
				rules[0].Conditions[0].Operator = RuleOperatorIs
			}

			res := engine.Evaluate(rules, msg, parsedBody, tc.hasAttachments)
			if res.Star != tc.shouldMatch {
				t.Errorf("field %s match result: %t, expected %t", tc.field, res.Star, tc.shouldMatch)
			}
		})
	}
}

func TestRuleEngine_MatchModes(t *testing.T) {
	engine := NewRuleEngine()

	msg := &Message{
		Sender:  "alice@example.com",
		Subject: "Meeting Tomorrow",
	}

	conditions := []RuleCondition{
		{
			Field:    RuleFieldFrom,
			Operator: RuleOperatorEquals,
			Value:    "alice@example.com",
		},
		{
			Field:    RuleFieldSubject,
			Operator: RuleOperatorContains,
			Value:    "Urgent",
		},
	}

	// For MatchMode "all": sender matches, but subject "Urgent" does not. So it shouldn't match.
	allRule := Rule{
		ID:         uuid.New(),
		Enabled:    true,
		MatchMode:  "all",
		Conditions: conditions,
		Actions:    []RuleAction{{Type: RuleActionStar}},
	}

	resAll := engine.Evaluate([]Rule{allRule}, msg, "", false)
	if resAll.Star {
		t.Error("expected 'all' match mode to fail when one condition is false")
	}

	// For MatchMode "any": sender matches, so it should match.
	anyRule := Rule{
		ID:         uuid.New(),
		Enabled:    true,
		MatchMode:  "any",
		Conditions: conditions,
		Actions:    []RuleAction{{Type: RuleActionStar}},
	}

	resAny := engine.Evaluate([]Rule{anyRule}, msg, "", false)
	if !resAny.Star {
		t.Error("expected 'any' match mode to pass when one condition is true")
	}

	// Empty conditions list: should not match.
	emptyRule := Rule{
		ID:         uuid.New(),
		Enabled:    true,
		MatchMode:  "all",
		Conditions: []RuleCondition{},
		Actions:    []RuleAction{{Type: RuleActionStar}},
	}

	resEmpty := engine.Evaluate([]Rule{emptyRule}, msg, "", false)
	if resEmpty.Star {
		t.Error("expected rule with no conditions to not match")
	}
}

func TestRuleEngine_PrioritySortingAndStopProcessing(t *testing.T) {
	engine := NewRuleEngine()

	// We want to verify:
	// 1. Rules are evaluated in Priority ascending order (1 before 2)
	// 2. ActionMoveToFolder sets target folder
	// 3. ActionMoveToTrash sets target folder to "Trash"
	// 4. ActionDelete sets Discard to true
	// 5. StopProcessing stops further rules from executing.

	msg := &Message{
		Subject: "Test Priority",
	}

	// Priority 1 sets TargetFolder = "Trash", StopProcessing = false
	// Priority 2 sets Star = true, StopProcessing = true
	// Priority 3 sets TargetFolder = "Important", StopProcessing = false
	// Since Priority 2 has StopProcessing = true, Priority 3 should NOT execute.
	// So TargetFolder should remain "Trash" (from Priority 1), Star should be true,
	// and TargetFolder should NOT be "Important".

	rules := []Rule{
		{
			ID:             uuid.New(),
			Enabled:        true,
			Priority:       3,
			MatchMode:      "any",
			Conditions:     []RuleCondition{{Field: RuleFieldSubject, Operator: RuleOperatorContains, Value: "Test"}},
			Actions:        []RuleAction{{Type: RuleActionMoveToFolder, Target: "Important"}},
			StopProcessing: false,
		},
		{
			ID:             uuid.New(),
			Enabled:        true,
			Priority:       2,
			MatchMode:      "any",
			Conditions:     []RuleCondition{{Field: RuleFieldSubject, Operator: RuleOperatorContains, Value: "Test"}},
			Actions:        []RuleAction{{Type: RuleActionStar}},
			StopProcessing: true,
		},
		{
			ID:             uuid.New(),
			Enabled:        true,
			Priority:       1,
			MatchMode:      "any",
			Conditions:     []RuleCondition{{Field: RuleFieldSubject, Operator: RuleOperatorContains, Value: "Test"}},
			Actions:        []RuleAction{{Type: RuleActionMoveToTrash}},
			StopProcessing: false,
		},
	}

	res := engine.Evaluate(rules, msg, "", false)
	if res.TargetFolder != "Trash" {
		t.Errorf("expected TargetFolder to be 'Trash', got '%s'", res.TargetFolder)
	}
	if !res.Star {
		t.Error("expected Star to be true")
	}

	// Test ActionDelete
	deleteRule := Rule{
		ID:         uuid.New(),
		Enabled:    true,
		Priority:   1,
		MatchMode:  "any",
		Conditions: []RuleCondition{{Field: RuleFieldSubject, Operator: RuleOperatorContains, Value: "Test"}},
		Actions:    []RuleAction{{Type: RuleActionDelete}},
	}

	resDelete := engine.Evaluate([]Rule{deleteRule}, msg, "", false)
	if !resDelete.Discard {
		t.Error("expected Discard to be true for ActionDelete")
	}
}
