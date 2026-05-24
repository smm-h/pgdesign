package risk

import "testing"

func TestCreateTable(t *testing.T) {
	c := Classify(OpCreateTable, OpContext{})
	if c.RiskLevel != Safe {
		t.Errorf("expected Safe, got %s", c.RiskLevel)
	}
	if c.LockType != LockNone {
		t.Errorf("expected LockNone, got %s", c.LockType)
	}
	if !c.Reversible {
		t.Error("expected Reversible")
	}
}

func TestDropTable(t *testing.T) {
	c := Classify(OpDropTable, OpContext{})
	if c.RiskLevel != Dangerous {
		t.Errorf("expected Dangerous, got %s", c.RiskLevel)
	}
	if !c.DataLoss {
		t.Error("expected DataLoss")
	}
	if c.LockType != LockAccessExclusive {
		t.Errorf("expected AccessExclusive, got %s", c.LockType)
	}
}

func TestAddColumnNullable(t *testing.T) {
	c := Classify(OpAddColumn, OpContext{IsNullable: true})
	if c.RiskLevel != Safe {
		t.Errorf("expected Safe, got %s", c.RiskLevel)
	}
	if !c.Reversible {
		t.Error("expected Reversible")
	}
}

func TestAddColumnNotNullNoDefault(t *testing.T) {
	c := Classify(OpAddColumn, OpContext{IsNullable: false, HasDefault: false})
	if c.RiskLevel != Dangerous {
		t.Errorf("expected Dangerous, got %s", c.RiskLevel)
	}
	if c.Suggestion == "" {
		t.Error("expected a suggestion")
	}
}

func TestAddColumnNotNullWithDefaultPG11(t *testing.T) {
	c := Classify(OpAddColumn, OpContext{
		IsNullable: false,
		HasDefault: true,
		PGVersion:  11,
	})
	if c.RiskLevel != Safe {
		t.Errorf("expected Safe, got %s", c.RiskLevel)
	}
}

func TestAddColumnNotNullWithDefaultPrePG11(t *testing.T) {
	c := Classify(OpAddColumn, OpContext{
		IsNullable: false,
		HasDefault: true,
		PGVersion:  10,
	})
	if c.RiskLevel != Dangerous {
		t.Errorf("expected Dangerous, got %s", c.RiskLevel)
	}
}

func TestCreateIndex(t *testing.T) {
	c := Classify(OpCreateIndex, OpContext{})
	if c.RiskLevel != Caution {
		t.Errorf("expected Caution, got %s", c.RiskLevel)
	}
	if c.LockType != LockShareLock {
		t.Errorf("expected ShareLock, got %s", c.LockType)
	}
	if c.Suggestion == "" {
		t.Error("expected a suggestion about CONCURRENTLY")
	}
}

func TestCreateIndexConcurrently(t *testing.T) {
	c := Classify(OpCreateIndexConcurrently, OpContext{})
	if c.RiskLevel != Safe {
		t.Errorf("expected Safe, got %s", c.RiskLevel)
	}
	if c.LockType != LockShareUpdateExclusive {
		t.Errorf("expected ShareUpdateExclusive, got %s", c.LockType)
	}
}

func TestTableSizeEscalation(t *testing.T) {
	// set_not_null is Caution with AccessExclusive; with >1M rows it should escalate.
	c := Classify(OpSetNotNull, OpContext{EstimatedRows: 2_000_000})
	if c.RiskLevel != Dangerous {
		t.Errorf("expected Dangerous after escalation, got %s", c.RiskLevel)
	}
}

func TestTableSizeEscalationDoesNotAffectSafe(t *testing.T) {
	// Safe operations should not escalate even with large tables.
	c := Classify(OpDropNotNull, OpContext{EstimatedRows: 2_000_000})
	if c.RiskLevel != Safe {
		t.Errorf("expected Safe (no escalation), got %s", c.RiskLevel)
	}
}

func TestTableSizeLockTimeoutSuggestion(t *testing.T) {
	c := Classify(OpSetNotNull, OpContext{EstimatedRows: 15_000_000})
	if c.RiskLevel != Dangerous {
		t.Errorf("expected Dangerous, got %s", c.RiskLevel)
	}
	if c.Suggestion == "" {
		t.Error("expected suggestion about lock_timeout")
	}
}

func TestRenameTable(t *testing.T) {
	c := Classify(OpRenameTable, OpContext{})
	if c.RiskLevel != Caution {
		t.Errorf("expected Caution, got %s", c.RiskLevel)
	}
	if c.LockType != LockAccessExclusive {
		t.Errorf("expected AccessExclusive, got %s", c.LockType)
	}
	if c.Suggestion == "" {
		t.Error("expected a suggestion about breaking clients")
	}
	if !c.Reversible {
		t.Error("expected Reversible")
	}
}

func TestRiskLevelString(t *testing.T) {
	tests := []struct {
		level RiskLevel
		want  string
	}{
		{Safe, "safe"},
		{Caution, "caution"},
		{Dangerous, "dangerous"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("RiskLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestLockTypeString(t *testing.T) {
	tests := []struct {
		lock LockType
		want string
	}{
		{LockNone, "none"},
		{LockShareLock, "ShareLock"},
		{LockShareRowExclusive, "ShareRowExclusive"},
		{LockShareUpdateExclusive, "ShareUpdateExclusive"},
		{LockAccessExclusive, "AccessExclusive"},
	}
	for _, tt := range tests {
		if got := tt.lock.String(); got != tt.want {
			t.Errorf("LockType(%d).String() = %q, want %q", tt.lock, got, tt.want)
		}
	}
}
