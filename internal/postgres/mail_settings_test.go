package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

func TestPostgresSignaturesAndRules(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	sigRepo := NewSignatureRepository(db)
	ruleRepo := NewRuleRepository(db)

	// Create test users
	var userA, userB uuid.UUID
	err = db.QueryRowContext(ctx, `
		INSERT INTO users (email, username, password_hash, display_name)
		VALUES ('sig-test-a@example.com', 'sigtesta', 'x', 'User A')
		RETURNING id
	`).Scan(&userA)
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userA)

	err = db.QueryRowContext(ctx, `
		INSERT INTO users (email, username, password_hash, display_name)
		VALUES ('sig-test-b@example.com', 'sigtestb', 'x', 'User B')
		RETURNING id
	`).Scan(&userB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userB)

	// ==========================================
	// 1. Signatures TDD Tests
	// ==========================================
	t.Run("Signatures", func(t *testing.T) {
		// Create a Signature for User A
		sig1 := &mail.Signature{
			UserID:    userA,
			Name:      "Work Signature",
			Content:   "Regards, User A",
			IsDefault: false,
		}
		err = sigRepo.Create(ctx, sig1)
		if err != nil {
			t.Fatalf("Create signature 1 failed: %v", err)
		}
		if sig1.ID == uuid.Nil {
			t.Fatal("Expected created signature to have a generated UUID")
		}

		// Create another Signature for User A
		sig2 := &mail.Signature{
			UserID:    userA,
			Name:      "Personal Signature",
			Content:   "Cheers, A",
			IsDefault: true,
		}
		err = sigRepo.Create(ctx, sig2)
		if err != nil {
			t.Fatalf("Create signature 2 failed: %v", err)
		}

		// Verify GetByID
		fetchedSig1, err := sigRepo.GetByID(ctx, userA, sig1.ID)
		if err != nil {
			t.Fatalf("GetByID failed: %v", err)
		}
		if fetchedSig1.Name != sig1.Name || fetchedSig1.Content != sig1.Content {
			t.Errorf("GetByID returned mismatching signature. Got Name: %q, Content: %q", fetchedSig1.Name, fetchedSig1.Content)
		}

		// User Isolation on GetByID: User B cannot retrieve User A's signature
		_, err = sigRepo.GetByID(ctx, userB, sig1.ID)
		if err == nil {
			t.Error("Expected GetByID to fail when User B tries to retrieve User A's signature")
		}

		// Verify ListByUser (User Isolation check)
		sigsA, err := sigRepo.ListByUser(ctx, userA)
		if err != nil {
			t.Fatalf("ListByUser for A failed: %v", err)
		}
		if len(sigsA) != 2 {
			t.Errorf("Expected User A to have 2 signatures, got %d", len(sigsA))
		}

		sigsB, err := sigRepo.ListByUser(ctx, userB)
		if err != nil {
			t.Fatalf("ListByUser for B failed: %v", err)
		}
		if len(sigsB) != 0 {
			t.Errorf("Expected User B to have 0 signatures (User Isolation failed), got %d", len(sigsB))
		}

		// Set Default and verify only one is default
		err = sigRepo.SetDefault(ctx, userA, sig1.ID)
		if err != nil {
			t.Fatalf("SetDefault failed: %v", err)
		}

		// Fetch updated signatures to check default flag
		updatedSig1, err := sigRepo.GetByID(ctx, userA, sig1.ID)
		if err != nil {
			t.Fatal(err)
		}
		updatedSig2, err := sigRepo.GetByID(ctx, userA, sig2.ID)
		if err != nil {
			t.Fatal(err)
		}

		if !updatedSig1.IsDefault {
			t.Error("Expected signature 1 to be default")
		}
		if updatedSig2.IsDefault {
			t.Error("Expected signature 2 to no longer be default")
		}

		// GetDefault test
		defaultSig, err := sigRepo.GetDefault(ctx, userA)
		if err != nil {
			t.Fatalf("GetDefault failed: %v", err)
		}
		if defaultSig.ID != sig1.ID {
			t.Errorf("Expected default signature to be %v, got %v", sig1.ID, defaultSig.ID)
		}

		// GetDefault for User B should return nil/not found
		_, err = sigRepo.GetDefault(ctx, userB)
		if err == nil {
			t.Error("Expected GetDefault for user B to return error (no default found)")
		}

		// Update Signature
		sig1.Name = "Updated Name"
		sig1.Content = "Updated Content"
		err = sigRepo.Update(ctx, sig1)
		if err != nil {
			t.Fatalf("Update signature failed: %v", err)
		}
		fetchedSig1Updated, err := sigRepo.GetByID(ctx, userA, sig1.ID)
		if err != nil {
			t.Fatal(err)
		}
		if fetchedSig1Updated.Name != "Updated Name" || fetchedSig1Updated.Content != "Updated Content" {
			t.Errorf("Expected updated fields, got Name: %q, Content: %q", fetchedSig1Updated.Name, fetchedSig1Updated.Content)
		}

		// User Isolation: User B cannot delete or update User A's signatures
		err = sigRepo.Delete(ctx, userB, sig1.ID)
		if err == nil {
			t.Error("Expected error when User B tries to delete User A's signature")
		}
		// Confirm signature is not deleted
		confirmSig1, err := sigRepo.GetByID(ctx, userA, sig1.ID)
		if err != nil || confirmSig1 == nil {
			t.Fatal("Signature was deleted by wrong user!")
		}

		// Delete signature 1 as User A (should succeed)
		err = sigRepo.Delete(ctx, userA, sig1.ID)
		if err != nil {
			t.Fatalf("Delete failed: %v", err)
		}
		_, err = sigRepo.GetByID(ctx, userA, sig1.ID)
		if err == nil {
			t.Error("Expected GetByID to fail for deleted signature")
		}
	})

	// ==========================================
	// 2. Rules TDD Tests
	// ==========================================
	t.Run("Rules", func(t *testing.T) {
		rule1 := &mail.Rule{
			UserID:   userA,
			Name:     "Spam Filter",
			Priority: 0,
			Enabled:  true,
			MatchMode: "all",
			Conditions: []mail.RuleCondition{
				{Field: mail.RuleFieldSubject, Operator: mail.RuleOperatorContains, Value: "buy now"},
			},
			Actions: []mail.RuleAction{
				{Type: mail.RuleActionMoveToTrash},
			},
			StopProcessing: true,
		}

		err = ruleRepo.Create(ctx, rule1)
		if err != nil {
			t.Fatalf("Create rule 1 failed: %v", err)
		}
		if rule1.ID == uuid.Nil {
			t.Fatal("Expected created rule to have generated UUID")
		}

		rule2 := &mail.Rule{
			UserID:   userA,
			Name:     "Work Mails",
			Priority: 1,
			Enabled:  true,
			MatchMode: "any",
			Conditions: []mail.RuleCondition{
				{Field: mail.RuleFieldFrom, Operator: mail.RuleOperatorEndsWith, Value: "@work.com"},
			},
			Actions: []mail.RuleAction{
				{Type: mail.RuleActionMoveToFolder, Target: "Work"},
				{Type: mail.RuleActionStar},
			},
			StopProcessing: false,
		}
		err = ruleRepo.Create(ctx, rule2)
		if err != nil {
			t.Fatalf("Create rule 2 failed: %v", err)
		}

		// GetByID
		fetchedRule1, err := ruleRepo.GetByID(ctx, userA, rule1.ID)
		if err != nil {
			t.Fatalf("GetByID rule failed: %v", err)
		}
		if fetchedRule1.Name != rule1.Name || len(fetchedRule1.Conditions) != 1 || fetchedRule1.Conditions[0].Value != "buy now" {
			t.Errorf("GetByID rule returned incorrect conditions/data: %+v", fetchedRule1)
		}

		// User Isolation on GetByID: User B cannot retrieve User A's rule
		_, err = ruleRepo.GetByID(ctx, userB, rule1.ID)
		if err == nil {
			t.Error("Expected GetByID to fail when User B tries to retrieve User A's rule")
		}

		// ListByUser
		rulesA, err := ruleRepo.ListByUser(ctx, userA)
		if err != nil {
			t.Fatalf("ListByUser rules failed: %v", err)
		}
		if len(rulesA) != 2 {
			t.Errorf("Expected 2 rules, got %d", len(rulesA))
		}
		if rulesA[0].ID != rule1.ID || rulesA[1].ID != rule2.ID {
			t.Errorf("Expected priority ordering [rule1, rule2], got sequence: %v, %v", rulesA[0].Name, rulesA[1].Name)
		}

		// User Isolation: ListByUser for B
		rulesB, err := ruleRepo.ListByUser(ctx, userB)
		if err != nil {
			t.Fatal(err)
		}
		if len(rulesB) != 0 {
			t.Errorf("Expected User B to have 0 rules, got %d", len(rulesB))
		}

		// Reorder priorities (make rule2 priority 0 and rule1 priority 1)
		err = ruleRepo.Reorder(ctx, userA, []uuid.UUID{rule2.ID, rule1.ID})
		if err != nil {
			t.Fatalf("Reorder failed: %v", err)
		}

		// Verify priority reordered in ListByUser
		rulesAOrdered, err := ruleRepo.ListByUser(ctx, userA)
		if err != nil {
			t.Fatal(err)
		}
		if rulesAOrdered[0].ID != rule2.ID || rulesAOrdered[1].ID != rule1.ID {
			t.Errorf("Expected priority reordered, got sequence first: %v, second: %v", rulesAOrdered[0].Name, rulesAOrdered[1].Name)
		}

		// SetEnabled (Disable rule1)
		err = ruleRepo.SetEnabled(ctx, userA, rule1.ID, false)
		if err != nil {
			t.Fatalf("SetEnabled failed: %v", err)
		}

		// Verify ListEnabled only returns Rule 2
		enabledRules, err := ruleRepo.ListEnabled(ctx, userA)
		if err != nil {
			t.Fatalf("ListEnabled failed: %v", err)
		}
		if len(enabledRules) != 1 {
			t.Fatalf("Expected 1 enabled rule, got %d", len(enabledRules))
		}
		if enabledRules[0].ID != rule2.ID {
			t.Errorf("Expected Rule 2 to be the only enabled rule, got %v", enabledRules[0].Name)
		}

		// Update Rule
		rule2.Name = "Super Work Mails"
		rule2.MatchMode = "all"
		err = ruleRepo.Update(ctx, rule2)
		if err != nil {
			t.Fatalf("Update rule failed: %v", err)
		}
		fetchedRule2Updated, err := ruleRepo.GetByID(ctx, userA, rule2.ID)
		if err != nil {
			t.Fatal(err)
		}
		if fetchedRule2Updated.Name != "Super Work Mails" || fetchedRule2Updated.MatchMode != "all" {
			t.Errorf("Expected updated rule fields, got Name: %q, MatchMode: %q", fetchedRule2Updated.Name, fetchedRule2Updated.MatchMode)
		}

		// User Isolation: User B cannot delete User A's rule
		err = ruleRepo.Delete(ctx, userB, rule2.ID)
		if err == nil {
			t.Error("Expected error when User B tries to delete User A's rule")
		}
		confirmRule2, err := ruleRepo.GetByID(ctx, userA, rule2.ID)
		if err != nil || confirmRule2 == nil {
			t.Fatal("Rule 2 was deleted by wrong user!")
		}

		// Delete rule 2 as User A (should succeed)
		err = ruleRepo.Delete(ctx, userA, rule2.ID)
		if err != nil {
			t.Fatalf("Delete rule failed: %v", err)
		}
		_, err = ruleRepo.GetByID(ctx, userA, rule2.ID)
		if err == nil {
			t.Error("Expected GetByID to fail for deleted rule")
		}
	})
}
