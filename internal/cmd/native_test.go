package cmd

import (
	"strings"
	"testing"
)

func TestNativeSessionFlagExcludesCredentialFlags(t *testing.T) {
	if loginCmd.Flags().Lookup("native-session-file") == nil {
		t.Fatal("native session import flag is missing")
	}
	for _, other := range []string{"cookie-file", "access-token", "client-id", "client-secret", "user", "password", "otp"} {
		t.Run(other, func(t *testing.T) {
			native := loginCmd.Flags().Lookup("native-session-file")
			flag := loginCmd.Flags().Lookup(other)
			native.Changed, flag.Changed = true, true
			t.Cleanup(func() { native.Changed, flag.Changed = false, false })
			if err := loginCmd.ValidateFlagGroups(); err == nil || !strings.Contains(err.Error(), "native-session-file") {
				t.Fatal("native import accepted conflicting credentials")
			}
		})
	}
}
