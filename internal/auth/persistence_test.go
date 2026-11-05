package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeProcessHelper(t *testing.T) {
	mode := os.Getenv("REDDIT_TEST_PROCESS")
	if mode == "" {
		return
	}
	endpoint := os.Getenv("REDDIT_TEST_ENDPOINT")
	tokenHTTP.Transport = nativeTestTransport{endpoint}
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("REDDIT_TEST_READY"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, os.Getenv("REDDIT_TEST_START"))
	switch mode {
	case "refresh":
		token, err := s.TokenWithValidator(fakeIdentity)
		if token != "synthetic-bearer" || err != nil {
			t.Fatalf("child refresh failed: %v", err)
		}
	case "stale":
		token, err := s.TokenWithValidator(fakeIdentity)
		if token != "" || err == nil || !strings.Contains(err.Error(), "session changed") {
			t.Fatal("stale child overwrote newer session")
		}
	case "cookie":
		if _, err := LoginWithCookie("browser=SYNTHETIC", "BrowserTest/1"); err != nil {
			t.Fatal(err)
		}
	case "lock-timeout":
		sessionLockTimeout = 40 * time.Millisecond
		if err := withSessionLock(func() error { return nil }); err == nil {
			t.Fatal("child lock did not time out")
		}
	case "hold-lock":
		if err := withSessionLock(func() error {
			if err := os.WriteFile(os.Getenv("REDDIT_TEST_LOCKED"), nil, 0o600); err != nil {
				return err
			}
			select {}
		}); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unknown child operation")
	}
}

func fakeIdentity(token, ua string) (Identity, error) {
	return Identity{ID: "fake-account", Username: "fake-user"}, nil
}

func waitForTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("child synchronization timed out")
}

func startAuthProcess(t *testing.T, mode, endpoint, start string) *exec.Cmd {
	t.Helper()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestNativeProcessHelper$", "-test.timeout=10s")
	cmd.Env = []string{"HOME=" + os.Getenv("HOME"), "XDG_CONFIG_HOME=" + os.Getenv("XDG_CONFIG_HOME"), "TMPDIR=" + os.TempDir(), "TZ=UTC", "REDDIT_TEST_PROCESS=" + mode, "REDDIT_TEST_ENDPOINT=" + endpoint, "REDDIT_TEST_READY=" + ready, "REDDIT_TEST_START=" + start, "REDDIT_TEST_LOCKED=" + filepath.Join(dir, "locked")}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForTestFile(t, ready)
	return cmd
}

func seedNative(t *testing.T) (*Session, string) {
	t.Helper()
	path := isolatedStore(t)
	s := &Session{Native: nativeFixture(), Username: "fake-user", AccessToken: "old-token", ExpiresAt: time.Now().Add(-time.Hour)}
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func releaseAuthProcesses(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeProcessesShareOneRefresh(t *testing.T) {
	_, path := seedNative(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Set-Cookie", "reddit_session=ROTATED; Path=/")
		fmt.Fprint(w, nativeSuccess())
	}))
	defer server.Close()
	start := filepath.Join(t.TempDir(), "start")
	one := startAuthProcess(t, "refresh", server.URL, start)
	two := startAuthProcess(t, "refresh", server.URL, start)
	releaseAuthProcesses(t, start)
	for _, cmd := range []*exec.Cmd{one, two} {
		if err := cmd.Wait(); err != nil {
			t.Fatal("concurrent refresh process failed")
		}
	}
	stored, err := Load()
	if err != nil || stored.Native.Cookie != "reddit_session=ROTATED" || calls.Load() != 1 {
		t.Fatalf("concurrent renewal state failed; calls=%d err=%v", calls.Load(), err)
	}
	for _, file := range []string{path, filepath.Join(filepath.Dir(path), "credentials.lock")} {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("credentials or lock permissions are not private")
		}
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("session directory is not private")
	}
}

func TestStaleProcessCannotOverwriteImportOrMode(t *testing.T) {
	for _, mode := range []string{"native-import", "browser", "bearer"} {
		t.Run(mode, func(t *testing.T) {
			seedNative(t)
			start := filepath.Join(t.TempDir(), "start")
			child := startAuthProcess(t, "stale", "", start)
			switch mode {
			case "native-import":
				nativeServer(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, nativeSuccess()) })
				fixture := filepath.Join(t.TempDir(), "fixture.json")
				if err := os.WriteFile(fixture, []byte(`{"cookie":"reddit_session=IMPORTED","user_agent":"NativeTest/1"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := LoginWithNativeFile(fixture, fakeIdentity); err != nil {
					t.Fatal(err)
				}
			case "browser":
				if _, err := LoginWithCookie("browser=SYNTHETIC", "BrowserTest/1"); err != nil {
					t.Fatal(err)
				}
			case "bearer":
				if _, err := LoginWithToken("fake-user", "synthetic-legacy-bearer"); err != nil {
					t.Fatal(err)
				}
			}
			releaseAuthProcesses(t, start)
			if err := child.Wait(); err != nil {
				t.Fatal("stale process did not refuse changed session")
			}
			current, err := Load()
			if err != nil || (mode == "native-import" && current.Native.Cookie != "reddit_session=IMPORTED") || (mode != "native-import" && current.Native != nil) {
				t.Fatal("newer import or mode lost")
			}
			if mode == "browser" && current.AccessToken != "" {
				t.Fatal("browser import retained native bearer")
			}
		})
	}
}

func TestModeChangeWaitsForRenewalProcess(t *testing.T) {
	seedNative(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		fmt.Fprint(w, nativeSuccess())
	}))
	defer server.Close()
	start := filepath.Join(t.TempDir(), "start")
	refresh := startAuthProcess(t, "refresh", server.URL, start)
	releaseAuthProcesses(t, start)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not reach fake endpoint")
	}
	change := startAuthProcess(t, "cookie", server.URL, start)
	close(release)
	for _, child := range []*exec.Cmd{refresh, change} {
		if err := child.Wait(); err != nil {
			t.Fatal("overlapping mode change failed")
		}
	}
	s, err := Load()
	if err != nil || s.Native != nil || s.Cookie != "browser=SYNTHETIC" || s.AccessToken != "" {
		t.Fatal("refresh overwrote later browser import")
	}
}

func TestProcessLockTimeoutAndCrashRelease(t *testing.T) {
	seedNative(t)
	start := filepath.Join(t.TempDir(), "start")
	holder := startAuthProcess(t, "hold-lock", "", start)
	releaseAuthProcesses(t, start)
	var locked string
	for _, entry := range holder.Env {
		if value, ok := strings.CutPrefix(entry, "REDDIT_TEST_LOCKED="); ok {
			locked = value
		}
	}
	waitForTestFile(t, locked)
	waiter := startAuthProcess(t, "lock-timeout", "", start)
	if err := waiter.Wait(); err != nil {
		t.Fatal("competing process did not time out")
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()
	if err := withSessionLock(func() error { return nil }); err != nil {
		t.Fatal("process exit did not release lock")
	}
}

func TestSessionSymlinksRejectedWithoutChangingTarget(t *testing.T) {
	for _, target := range []string{"credentials.json", "credentials.lock", "directory"} {
		t.Run(target, func(t *testing.T) {
			_, path := seedNative(t)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(filepath.Dir(path), target)
			backup := filepath.Join(t.TempDir(), "original")
			if target == "directory" {
				link = filepath.Dir(path)
			}
			if err := os.Rename(link, backup); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(backup, link); err != nil {
				t.Fatal(err)
			}
			if _, err := LoginWithToken("fake", "synthetic"); err == nil {
				t.Fatal("symlink target accepted")
			}
			if target == "credentials.json" {
				after, err := os.ReadFile(backup)
				if err != nil || string(after) != string(original) {
					t.Fatal("rejected save changed prior complete record")
				}
			}
		})
	}
}

func TestNativePersistenceFailureReturnsNoToken(t *testing.T) {
	s, path := seedNative(t)
	backup := filepath.Join(filepath.Dir(path), "prior-record")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	nativeServer(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, nativeSuccess()) })
	validate := func(token, ua string) (Identity, error) {
		if err := os.Rename(path, backup); err != nil {
			return Identity{}, err
		}
		if err := os.Symlink(backup, path); err != nil {
			return Identity{}, err
		}
		return fakeIdentity(token, ua)
	}
	if token, err := s.TokenWithValidator(validate); err == nil || token != "" || s.AccessToken != "old-token" {
		t.Fatal("persistence failure exposed candidate token")
	}
	after, err := os.ReadFile(backup)
	if err != nil || string(before) != string(after) {
		t.Fatal("persistence failure damaged previous complete JSON")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".credentials-*"))
	if err != nil || len(matches) != 0 {
		t.Fatal("failed persistence left temporary credentials")
	}
}
