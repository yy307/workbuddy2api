package pool

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoredPoolKeepsPersistentIntent(t *testing.T) {
	path := os.Getenv("WB2A_RECOVERY_STATE_FILE")
	if path == "" {
		t.Skip("private isolated state restore not supplied")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4*1024*1024 {
		t.Fatal("bounded private restored state required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal("invalid restore path")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || resolved != abs {
		t.Fatal("restore symlink refused")
	}
	raw, err := os.ReadFile(path)
	var expected stateFile
	if err != nil || json.Unmarshal(raw, &expected) != nil || len(expected.Accounts) == 0 {
		t.Fatal("restored pool state invalid")
	}
	// A no-network Pool constructor only reads this immutable isolated restore.
	for range 2 {
		p := New(path)
		if len(p.byUID) != len(expected.Accounts) {
			p.Close()
			t.Fatal("restored pool account bindings changed")
		}
		for uid, s := range expected.Accounts {
			e := p.byUID[uid]
			errTotal := max(s.ErrTotal, int64(s.ErrCount))
			expiring := max(int64(0), min(s.CreditsExpiring, s.Credits))
			if e == nil || e.a.UID != uid || e.credits != s.Credits || e.disabled != s.Disabled || e.reason != s.Reason || e.manualDisabled != s.ManualDisabled || e.manualReason != s.ManualReason || !e.until.Equal(s.Until) || e.coolKind != s.CoolKind || e.successCount != s.SuccessCount || e.errTotal != errTotal || !e.lastErr.Equal(s.LastErr) || !e.lastSuccess.Equal(s.LastSuccess) || e.softStreak != s.SoftStreak || e.sessionDeadFails != s.SessionDeadFails || e.consecutiveFails != s.ConsecutiveFails || e.creditsExpiring != expiring {
				p.Close()
				t.Fatal("restored credits, disable intent or continuity changed")
			}
		}
		p.Close() // No dirty flag: must not write or mirror state.
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("restored state changed")
	}
}
