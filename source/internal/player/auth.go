package player

import (
	"strconv"
	"strings"
)

type AuthStatus struct {
	Attempt string `json:"-"`
	Status  string `json:"status"`
	Browser string `json:"browser"`
	Message string `json:"message"`
}
type browserCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"httpOnly"`
	Expires  float64 `json:"expires"`
}

func youtubeDomain(d string) bool {
	d = strings.TrimPrefix(strings.ToLower(d), ".")
	return d == "youtube.com" || strings.HasSuffix(d, ".youtube.com")
}
func cookieJar(cookies []browserCookie) ([]byte, bool) {
	lines := []string{"# Netscape HTTP Cookie File"}
	authenticated := false
	for _, c := range cookies {
		if !youtubeDomain(c.Domain) || strings.ContainsAny(c.Name+c.Value+c.Path, "\r\n\t") {
			continue
		}
		if c.Name == "SAPISID" || c.Name == "__Secure-3PAPISID" {
			authenticated = true
		}
		domain := c.Domain
		if c.HTTPOnly {
			domain = "#HttpOnly_" + domain
		}
		subs := "FALSE"
		if strings.HasPrefix(c.Domain, ".") {
			subs = "TRUE"
		}
		secure := "FALSE"
		if c.Secure {
			secure = "TRUE"
		}
		expiry := int64(c.Expires)
		if expiry < 0 {
			expiry = 0
		}
		path := c.Path
		if path == "" {
			path = "/"
		}
		lines = append(lines, strings.Join([]string{domain, subs, path, secure, strconv.FormatInt(expiry, 10), c.Name, c.Value}, "\t"))
	}
	return []byte(strings.Join(lines, "\n") + "\n"), authenticated
}

func (a *App) cancelAuth() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.authCancel != nil {
		a.authCancel()
		a.authCancel = nil
	}
	a.authStatus = AuthStatus{Status: "idle"}
}
