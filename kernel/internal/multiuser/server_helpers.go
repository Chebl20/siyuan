package multiuser

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

func (a *App) newWorkspace(ctx context.Context, userID uuid.UUID, name, slug string, personal bool) (Workspace, error) {
	workspaceID := uuid.New()
	workspace, err := a.store.CreateWorkspace(ctx, userID, slug, name, containerName(workspaceID), personal)
	if err != nil {
		return Workspace{}, err
	}
	path := filepath.Join(a.config.WorkspaceRoot, workspace.ID.String())
	if err = os.MkdirAll(path, 0700); err != nil {
		_, _ = a.store.SoftDeleteWorkspace(ctx, workspace.ID, userID)
		return Workspace{}, err
	}
	if err = a.docker.CreateWorkspace(ctx, workspace); err != nil {
		_, _ = a.store.SoftDeleteWorkspace(ctx, workspace.ID, userID)
		return Workspace{}, err
	}
	return workspace, nil
}

func (a *App) openWorkspaceGet(writer http.ResponseWriter, request *http.Request) {
	auth, ok := a.auth(request)
	if !ok {
		http.Redirect(writer, request, "/login", http.StatusFound)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "workspaces" || parts[2] != "open" {
		http.NotFound(writer, request)
		return
	}
	workspaceID, err := uuid.Parse(parts[1])
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	a.redirectToWorkspace(writer, request, auth.user.ID, workspaceID)
}

func (a *App) redirectToWorkspace(writer http.ResponseWriter, request *http.Request, userID, workspaceID uuid.UUID) {
	workspace, err := a.store.WorkspaceForUser(request.Context(), userID, workspaceID)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	if err = a.store.SetActiveWorkspace(request.Context(), userID, workspace.ID); err != nil {
		http.Error(writer, "Unable to select workspace.", http.StatusInternalServerError)
		return
	}
	http.Redirect(writer, request, a.config.workspaceURL(workspace.Slug), http.StatusFound)
}

func (a *App) auth(request *http.Request) (requestAuth, bool) {
	cookie, err := request.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return requestAuth{}, false
	}
	user, err := a.store.UserBySession(request.Context(), cookie.Value)
	if err != nil {
		return requestAuth{}, false
	}
	return requestAuth{user: user, token: cookie.Value}, true
}

func (a *App) requireCSRF(writer http.ResponseWriter, request *http.Request) (requestAuth, bool) {
	auth, ok := a.auth(request)
	if !ok {
		http.Redirect(writer, request, "/login", http.StatusFound)
		return requestAuth{}, false
	}
	if !sameOrigin(request, a.config.PublicScheme, a.config.publicAuthority(a.config.AppHost)) || request.FormValue("csrf") != a.config.csrfToken(auth.token) {
		http.Error(writer, "Invalid request origin.", http.StatusForbidden)
		return requestAuth{}, false
	}
	return auth, true
}

func (a *App) startSession(writer http.ResponseWriter, request *http.Request, user User) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	if err = a.store.CreateSession(request.Context(), user.ID, token); err != nil {
		return err
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Domain:   a.config.sessionCookieDomain(),
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
		Secure:   a.config.CookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (a *App) clearSessionCookie(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", Domain: a.config.sessionCookieDomain(), MaxAge: -1, Secure: a.config.CookieSecure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (a *App) redirectDashboardError(writer http.ResponseWriter, request *http.Request, message string) {
	http.Redirect(writer, request, "/dashboard?error="+urlQueryEscape(message), http.StatusFound)
}

func (a *App) redirectDashboardMessage(writer http.ResponseWriter, request *http.Request, message string) {
	http.Redirect(writer, request, "/dashboard?message="+urlQueryEscape(message), http.StatusFound)
}

func (a *App) render(writer http.ResponseWriter, name string, data pageData) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(writer, name, data); err != nil {
		panic(err)
	}
}

func (a *App) sendResetEmail(to, token string) error {
	if a.config.SMTPHost == "" {
		return nil
	}
	address := net.JoinHostPort(a.config.SMTPHost, strconvItoa(a.config.SMTPPort))
	message := []byte("To: " + to + "\r\nFrom: " + a.config.SMTPFrom + "\r\nSubject: Reset your SiYuan password\r\n\r\nOpen this link to choose a new password: " + a.config.portalURL("/reset-password?token="+urlQueryEscape(token)) + "\r\nThis link expires in 30 minutes.\r\n")
	client, err := smtp.Dial(address)
	if err != nil {
		return err
	}
	defer client.Quit()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return errors.New("SMTP server does not offer STARTTLS")
	}
	if err = client.StartTLS(&tls.Config{ServerName: a.config.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
		return err
	}
	if a.config.SMTPUsername != "" {
		if err = client.Auth(smtp.PlainAuth("", a.config.SMTPUsername, a.config.SMTPPassword, a.config.SMTPHost)); err != nil {
			return err
		}
	}
	if err = client.Mail(a.config.SMTPFrom); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	mail, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = mail.Write(message); err != nil {
		return err
	}
	return mail.Close()
}

func requestHost(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.Host)
	if err == nil {
		return strings.ToLower(host)
	}
	return strings.ToLower(request.Host)
}

func requestAuthority(request *http.Request) string {
	return strings.ToLower(request.Host)
}

func (a *App) workspaceSlug(host string) (string, bool) {
	suffix := "." + a.config.BaseDomain
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(host, suffix)
	if strings.Contains(slug, ".") || !slugPattern.MatchString(slug) {
		return "", false
	}
	return slug, true
}

func sameOrigin(request *http.Request, scheme, host string) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return false
	}
	return origin == scheme+"://"+host
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func clientIP(request *http.Request) string {
	if forwarded := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(forwarded) != nil {
		return forwarded
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	return ""
}

type loginAttempt struct {
	failures int
	until    time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: map[string]loginAttempt{}}
}

func (l *loginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[key]
	return attempt.until.IsZero() || time.Now().After(attempt.until)
}

func (l *loginLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[key]
	attempt.failures++
	if attempt.failures >= 5 {
		attempt.until = time.Now().Add(15 * time.Minute)
		attempt.failures = 0
	}
	l.attempts[key] = attempt
}

func (l *loginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func urlQueryEscape(value string) string {
	return url.QueryEscape(value)
}

func strconvItoa(value int) string {
	return strconv.Itoa(value)
}
