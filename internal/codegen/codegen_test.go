package codegen

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

func TestExtractPolicies(t *testing.T) {
	schema := &model.Schema{
		Name: "game",
		Tables: []model.Table{
			{
				Name:   "chat_messages",
				Schema: "game",
				Policies: []model.Policy{
					{
						Name:      "chat_read_all",
						Operation: "SELECT",
						Using:     "true",
					},
					{
						Name:         "chat_requires_enabled",
						Operation:    "INSERT",
						WithCheck:    "sender_id::text = current_setting('app.player_id') AND EXISTS (SELECT 1 FROM game.player_privacy_settings WHERE player_id = sender_id AND chat_enabled = true)",
						ErrorCode:    "chat_disabled",
						ErrorMessage: "Chat is disabled in your privacy settings",
					},
				},
			},
			{
				Name:   "player_privacy_settings",
				Schema: "game",
				Policies: []model.Policy{
					{
						Name:      "privacy_read_all",
						Operation: "SELECT",
						Using:     "true",
					},
				},
			},
		},
	}

	contexts := ExtractPolicies(schema)
	if len(contexts) != 3 {
		t.Fatalf("expected 3 policies, got %d", len(contexts))
	}

	// Verify first policy from chat_messages.
	if contexts[0].PolicyName != "chat_read_all" {
		t.Errorf("expected chat_read_all, got %s", contexts[0].PolicyName)
	}
	if contexts[0].SchemaName != "game" {
		t.Errorf("expected schema game, got %s", contexts[0].SchemaName)
	}
	if contexts[0].TableName != "chat_messages" {
		t.Errorf("expected table chat_messages, got %s", contexts[0].TableName)
	}

	// Verify the error-code policy.
	if contexts[1].ErrorCode != "chat_disabled" {
		t.Errorf("expected chat_disabled, got %s", contexts[1].ErrorCode)
	}
}

func TestFilterGeneratable(t *testing.T) {
	policies := []PolicyContext{
		{
			PolicyName: "read_all",
			Using:      "true",
		},
		{
			PolicyName:   "chat_requires_enabled",
			WithCheck:    "EXISTS (SELECT 1 FROM game.player_privacy_settings WHERE player_id = sender_id AND chat_enabled = true)",
			ErrorCode:    "chat_disabled",
			ErrorMessage: "Chat is disabled",
		},
		{
			PolicyName: "own_only",
			Using:      "player_id::text = current_setting('app.player_id')",
			ErrorCode:  "not_owner",
		},
		{
			PolicyName:   "follow_requires_friends",
			WithCheck:    "EXISTS (SELECT 1 FROM game.player_privacy_settings WHERE player_id = followed_id AND friends_enabled = true)",
			ErrorCode:    "friends_disabled",
			ErrorMessage: "Friends disabled",
		},
	}

	result, diags := FilterGeneratable(policies)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d: %v", len(diags), diags)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 generatable policies, got %d", len(result))
	}
	if result[0].PolicyName != "chat_requires_enabled" {
		t.Errorf("expected chat_requires_enabled, got %s", result[0].PolicyName)
	}
	if result[1].PolicyName != "own_only" {
		t.Errorf("expected own_only, got %s", result[1].PolicyName)
	}
	if result[2].PolicyName != "follow_requires_friends" {
		t.Errorf("expected follow_requires_friends, got %s", result[2].PolicyName)
	}
}

func TestFilterGeneratable_Empty(t *testing.T) {
	policies := []PolicyContext{
		{PolicyName: "read_all", Using: "true"},
		{PolicyName: "own_only", Using: "player_id = current_setting('app.player_id')"},
	}
	result, _ := FilterGeneratable(policies)
	if len(result) != 0 {
		t.Errorf("expected 0 generatable policies, got %d", len(result))
	}
}
