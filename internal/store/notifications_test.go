package store

import "testing"

func TestHasNotificationRuleCondition(t *testing.T) {
	if hasNotificationRuleCondition(nil, "", "", nil, "", "", "") {
		t.Fatal("empty rule must not match every message")
	}
	if !hasNotificationRuleCondition(nil, "group-1", "", nil, "", "", "") {
		t.Fatal("weibo group id should be a valid condition")
	}
	if !hasNotificationRuleCondition(nil, "", "", nil, "sender-1", "", "") {
		t.Fatal("weibo sender id should be a valid condition")
	}
	if !hasNotificationRuleCondition(nil, "", "", nil, "", "", "image") {
		t.Fatal("message type should be a valid condition")
	}
}
