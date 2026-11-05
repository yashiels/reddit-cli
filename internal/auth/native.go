package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

const (
	nativeTokenURL      = "https://www.reddit.com/auth/v2/oauth/access-token/session"
	nativeResponseLimit = 1 << 20
	nativeImportLimit   = 16 << 10
)

type NativeSession struct {
	Cookie    string `json:"cookie"`
	UserAgent string `json:"user_agent"`
	AccountID string `json:"account_id"`
}

type Identity struct {
	ID       string
	Username string
}

type Validator func(token, userAgent string) (Identity, error)

func printable(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}

func validNativeCookie(cookie string) bool {
	value, ok := strings.CutPrefix(cookie, "reddit_session=")
	return ok && len(value) <= 8192 && printable(value) && !strings.ContainsAny(value, " \";,\\")
}

func validNativeInput(cookie, userAgent string) bool {
	return validNativeCookie(cookie) && len(userAgent) <= 2048 && printable(userAgent) && strings.TrimSpace(userAgent) != ""
}

func validIdentity(identity Identity) bool {
	return len(identity.ID) <= 128 && printable(identity.ID) && !strings.ContainsAny(identity.ID, " ") && len(identity.Username) <= 128 && printable(identity.Username) && !strings.ContainsAny(identity.Username, " ")
}

func readNativeFile(path string) (*NativeSession, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot read native session file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("invalid native session file")
	}
	b, err := io.ReadAll(io.LimitReader(f, nativeImportLimit+1))
	if err != nil || len(b) > nativeImportLimit {
		return nil, fmt.Errorf("invalid native session file")
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("invalid native session file")
	}
	fields := make(map[string]string)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil || (key != "cookie" && key != "user_agent") {
			return nil, fmt.Errorf("invalid native session file")
		}
		name := key.(string)
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("invalid native session file")
		}
		var value string
		if decoder.Decode(&value) != nil {
			return nil, fmt.Errorf("invalid native session file")
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("invalid native session file")
	}
	if decoder.Decode(new(any)) != io.EOF || !validNativeInput(fields["cookie"], fields["user_agent"]) {
		return nil, fmt.Errorf("invalid native session file")
	}
	return &NativeSession{Cookie: fields["cookie"], UserAgent: fields["user_agent"]}, nil
}

func LoginWithNativeFile(path string, validate Validator) (*Session, error) {
	native, err := readNativeFile(path)
	if err != nil {
		return nil, err
	}
	s := &Session{Native: native}
	err = withSessionLock(func() error {
		if err := s.renewNative(validate); err != nil {
			return err
		}
		return replaceSession(s)
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) nativeToken(validate Validator) (string, error) {
	var token string
	err := withSessionLock(func() error {
		current, err := loadSession()
		if err != nil {
			return err
		}
		if current == nil || current.Native == nil || current.Revision != s.Revision || current.Native.AccountID != s.Native.AccountID {
			return fmt.Errorf("session changed; retry the command")
		}
		if !current.valid() {
			if err := current.renewNative(validate); err != nil {
				return err
			}
			if err := persistSession(current); err != nil {
				return err
			}
		}
		*s = *current
		token = current.AccessToken
		return nil
	})
	return token, err
}

func (s *Session) renewNative(validate Validator) error {
	if validate == nil || s.Native == nil || !validNativeInput(s.Native.Cookie, s.Native.UserAgent) {
		return fmt.Errorf("native session requires identity validation")
	}
	token, expiry, cookie, err := requestNative(s.Native)
	if err != nil {
		return err
	}
	identity, err := validate(token, s.Native.UserAgent)
	if err != nil || !validIdentity(identity) {
		return fmt.Errorf("native identity validation failed")
	}
	if s.Native.AccountID != "" && s.Native.AccountID != identity.ID {
		return fmt.Errorf("native account identity changed; import again")
	}
	s.AccessToken, s.ExpiresAt, s.Username = token, expiry, identity.Username
	s.Native.Cookie, s.Native.AccountID = cookie, identity.ID
	return nil
}

func requestNative(native *NativeSession) (string, time.Time, string, error) {
	fail := func() (string, time.Time, string, error) {
		return "", time.Time{}, "", errors.New("invalid native renewal response")
	}
	req, err := http.NewRequest(http.MethodPost, nativeTokenURL, strings.NewReader(`{"scopes":["*"]}`))
	if err != nil {
		return fail()
	}
	req.SetBasicAuth(AppClientID, "")
	req.Header.Set("Cookie", native.Cookie)
	req.Header.Set("User-Agent", native.UserAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := tokenHTTP.Do(req)
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("native renewal request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, "", fmt.Errorf("native renewal returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, nativeResponseLimit+1))
	if err != nil || len(b) > nativeResponseLimit {
		return fail()
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		ExpiryTS    int64  `json:"expiry_ts"`
	}
	if json.Unmarshal(b, &out) != nil || !printable(out.AccessToken) || strings.Contains(out.AccessToken, " ") || out.ExpiresIn <= 60 || out.ExpiresIn > 7*24*60*60 {
		return fail()
	}
	now := time.Now()
	expiry := time.Unix(out.ExpiryTS, 0)
	relative := now.Add(time.Duration(out.ExpiresIn) * time.Second)
	if !expiry.After(now.Add(time.Minute)) || expiry.After(now.Add(7*24*time.Hour)) || expiry.Sub(relative) > 5*time.Minute || relative.Sub(expiry) > 5*time.Minute {
		return fail()
	}
	if relative.Before(expiry) {
		expiry = relative
	}
	cookie := native.Cookie
	for _, raw := range resp.Header.Values("Set-Cookie") {
		name, _, _ := strings.Cut(raw, "=")
		if strings.TrimSpace(name) != "reddit_session" {
			continue
		}
		parsed, err := http.ParseSetCookie(raw)
		if err != nil || parsed.Valid() != nil || parsed.MaxAge < 0 || (!parsed.Expires.IsZero() && !parsed.Expires.After(now)) {
			return fail()
		}
		for _, attribute := range parsed.Unparsed {
			name, _, _ := strings.Cut(attribute, "=")
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "domain", "path", "max-age", "expires":
				return fail()
			}
		}
		domain := strings.TrimPrefix(strings.ToLower(parsed.Domain), ".")
		if domain != "" && domain != "reddit.com" && domain != "www.reddit.com" {
			return fail()
		}
		path := parsed.Path
		matchesPath := path == "" || path == "/" || path == req.URL.Path || (strings.HasPrefix(path, "/") && strings.HasPrefix(req.URL.Path, strings.TrimSuffix(path, "/")+"/"))
		if !matchesPath {
			return fail()
		}
		cookie = "reddit_session=" + parsed.Value
		if !validNativeCookie(cookie) {
			return fail()
		}
	}
	return out.AccessToken, expiry, cookie, nil
}
