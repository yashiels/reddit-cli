package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var sessionLockTimeout = 35 * time.Second

var errSessionStore = errors.New("cannot access private session storage")

func sessionDirectory() (string, error) {
	path, err := sessionPath()
	if err != nil {
		return "", errSessionStore
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", errSessionStore
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errSessionStore
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", errSessionStore
	}
	return dir, nil
}

func withSessionLock(fn func() error) error {
	dir, err := sessionDirectory()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "credentials.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return errSessionStore
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || f.Chmod(0o600) != nil {
		return errSessionStore
	}
	deadline := time.Now().Add(sessionLockTimeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return errSessionStore
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("session storage busy; retry the command")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func loadSession() (*Session, error) {
	path, err := sessionPath()
	if err != nil {
		return nil, errSessionStore
	}
	if info, err := os.Lstat(filepath.Dir(path)); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errSessionStore
		}
	} else if !os.IsNotExist(err) {
		return nil, errSessionStore
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errSessionStore
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errSessionStore
	}
	b, err := io.ReadAll(io.LimitReader(f, nativeResponseLimit+1))
	if err != nil || len(b) > nativeResponseLimit {
		return nil, errSessionStore
	}
	var s Session
	if json.Unmarshal(b, &s) != nil {
		return nil, errSessionStore
	}
	if s.Native != nil && (!validNativeInput(s.Native.Cookie, s.Native.UserAgent) || !validIdentity(Identity{ID: s.Native.AccountID, Username: s.Username})) {
		return nil, fmt.Errorf("incomplete native session; import again")
	}
	return &s, nil
}

func replaceSession(s *Session) error {
	var revision [16]byte
	if _, err := rand.Read(revision[:]); err != nil {
		return errSessionStore
	}
	s.Revision = hex.EncodeToString(revision[:])
	return persistSession(s)
}

func persistSession(s *Session) error {
	dir, err := sessionDirectory()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "credentials.json")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errSessionStore
		}
	} else if !os.IsNotExist(err) {
		return errSessionStore
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return errSessionStore
	}
	f, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return errSessionStore
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if f.Chmod(0o600) != nil {
		return errSessionStore
	}
	if _, err := f.Write(b); err != nil {
		return errSessionStore
	}
	if f.Sync() != nil || f.Close() != nil {
		return errSessionStore
	}
	if os.Rename(f.Name(), path) != nil {
		return errSessionStore
	}
	d, err := os.Open(dir)
	if err != nil {
		return errSessionStore
	}
	defer d.Close()
	if d.Sync() != nil {
		return errSessionStore
	}
	return nil
}
