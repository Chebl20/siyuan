package multiuser

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

const sessionCookie = "siyuan_portal_session"

type App struct {
	config    Config
	store     *Store
	docker    *DockerClient
	templates *template.Template
	limiter   *loginLimiter
}

type pageData struct {
	Title      string
	User       *User
	Workspaces []Workspace
	CSRF       string
	Error      string
	Message    string
	ResetToken string
	Signup     bool
}

type requestAuth struct {
	user  User
	token string
}

func NewApp(ctx context.Context, config Config) (*App, error) {
	if err := os.MkdirAll(config.WorkspaceRoot, 0700); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}
	store, err := NewStore(ctx, config.DatabaseURL)
	if err != nil {
		return nil, err
	}
	templates, err := template.New("layout").Parse(pageTemplates)
	if err != nil {
		store.Close()
		return nil, err
	}
	return &App{config: config, store: store, docker: NewDockerClient(config), templates: templates, limiter: newLoginLimiter()}, nil
}

func (a *App) Close() {
	a.store.Close()
}

func (a *App) Server() *http.Server {
	return &http.Server{
		Addr:              ":8080",
		Handler:           a,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       90 * time.Second,
	}
}

func (a *App) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	host := requestHost(request)
	if host == a.config.AppHost {
		a.servePortal(writer, request)
		return
	}
	if slug, ok := a.workspaceSlug(host); ok {
		a.serveWorkspace(writer, request, slug)
		return
	}
	http.NotFound(writer, request)
}

func (a *App) servePortal(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("X-Frame-Options", "DENY")
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/":
		if _, ok := a.auth(request); ok {
			http.Redirect(writer, request, "/dashboard", http.StatusFound)
		} else {
			http.Redirect(writer, request, "/login", http.StatusFound)
		}
	case request.Method == http.MethodGet && request.URL.Path == "/login":
		a.render(writer, "login", pageData{Title: "Sign in", Signup: a.config.SignupEnabled})
	case request.Method == http.MethodPost && request.URL.Path == "/login":
		a.login(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/register":
		a.render(writer, "register", pageData{Title: "Create account", Signup: a.config.SignupEnabled})
	case request.Method == http.MethodPost && request.URL.Path == "/register":
		a.register(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/forgot-password":
		a.render(writer, "forgot", pageData{Title: "Reset password"})
	case request.Method == http.MethodPost && request.URL.Path == "/forgot-password":
		a.forgotPassword(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/reset-password":
		a.render(writer, "reset", pageData{Title: "Choose a new password", ResetToken: request.URL.Query().Get("token")})
	case request.Method == http.MethodPost && request.URL.Path == "/reset-password":
		a.resetPassword(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/dashboard":
		a.dashboard(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/logout":
		a.logout(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/workspaces":
		a.createWorkspace(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/workspaces/"):
		a.openWorkspaceGet(writer, request)
	case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/workspaces/"):
		a.workspaceAction(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (a *App) login(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		a.render(writer, "login", pageData{Title: "Sign in", Error: "Invalid form."})
		return
	}
	email := request.FormValue("email")
	key := clientIP(request) + ":" + normalizeEmail(email)
	if !a.limiter.Allow(key) {
		a.render(writer, "login", pageData{Title: "Sign in", Error: "Too many attempts. Try again later.", Signup: a.config.SignupEnabled})
		return
	}
	user, err := a.store.UserByEmail(request.Context(), email)
	if err != nil || !comparePassword(user.PasswordHash, request.FormValue("password")) {
		a.limiter.Fail(key)
		a.store.Audit(request.Context(), nil, "login_failed", clientIP(request))
		a.render(writer, "login", pageData{Title: "Sign in", Error: "Email or password is incorrect.", Signup: a.config.SignupEnabled})
		return
	}
	a.limiter.Reset(key)
	if err = a.startSession(writer, request, user); err != nil {
		a.render(writer, "login", pageData{Title: "Sign in", Error: "Unable to start your session."})
		return
	}
	a.store.Audit(request.Context(), &user.ID, "login_succeeded", clientIP(request))
	http.Redirect(writer, request, "/dashboard", http.StatusFound)
}

func (a *App) register(writer http.ResponseWriter, request *http.Request) {
	if !a.config.SignupEnabled {
		http.NotFound(writer, request)
		return
	}
	if err := request.ParseForm(); err != nil {
		a.render(writer, "register", pageData{Title: "Create account", Error: "Invalid form.", Signup: true})
		return
	}
	email, password := request.FormValue("email"), request.FormValue("password")
	if err := validateEmail(email); err != nil {
		a.render(writer, "register", pageData{Title: "Create account", Error: err.Error(), Signup: true})
		return
	}
	if err := validatePassword(password); err != nil {
		a.render(writer, "register", pageData{Title: "Create account", Error: err.Error(), Signup: true})
		return
	}
	hash, err := hashPassword(password)
	if err != nil {
		a.render(writer, "register", pageData{Title: "Create account", Error: "Unable to create your account.", Signup: true})
		return
	}
	user, err := a.store.CreateUser(request.Context(), strings.TrimSpace(email), hash)
	if err != nil {
		a.render(writer, "register", pageData{Title: "Create account", Error: "An account already uses this email.", Signup: true})
		return
	}
	workspace, err := a.newWorkspace(request.Context(), user.ID, "Personal workspace", "personal-"+strings.ReplaceAll(uuid.New().String()[:8], "-", ""), true)
	if err != nil {
		a.store.Audit(request.Context(), &user.ID, "workspace_provision_failed", clientIP(request))
		a.render(writer, "register", pageData{Title: "Create account", Error: "Your account was created, but its workspace could not be provisioned. Contact an administrator.", Signup: true})
		return
	}
	a.store.Audit(request.Context(), &user.ID, "account_registered", clientIP(request))
	if err = a.startSession(writer, request, user); err != nil {
		a.render(writer, "login", pageData{Title: "Sign in", Error: "Account created. Please sign in."})
		return
	}
	http.Redirect(writer, request, "/workspaces/"+workspace.ID.String()+"/open", http.StatusFound)
}

func (a *App) dashboard(writer http.ResponseWriter, request *http.Request) {
	auth, ok := a.auth(request)
	if !ok {
		http.Redirect(writer, request, "/login", http.StatusFound)
		return
	}
	workspaces, err := a.store.WorkspacesForUser(request.Context(), auth.user.ID)
	if err != nil {
		http.Error(writer, "Unable to load workspaces.", http.StatusInternalServerError)
		return
	}
	for index := range workspaces {
		workspaces[index].Active = auth.user.ActiveWorkspaceID != nil && workspaces[index].ID == *auth.user.ActiveWorkspaceID
	}
	a.render(writer, "dashboard", pageData{Title: "Workspaces", User: &auth.user, Workspaces: workspaces, CSRF: a.config.csrfToken(auth.token), Error: request.URL.Query().Get("error"), Message: request.URL.Query().Get("message")})
}

func (a *App) createWorkspace(writer http.ResponseWriter, request *http.Request) {
	auth, ok := a.requireCSRF(writer, request)
	if !ok {
		return
	}
	name, slug := strings.TrimSpace(request.FormValue("name")), normalizeSlug(request.FormValue("slug"))
	if err := validateWorkspaceName(name); err != nil {
		a.redirectDashboardError(writer, request, err.Error())
		return
	}
	if err := validateSlug(slug); err != nil {
		a.redirectDashboardError(writer, request, err.Error())
		return
	}
	workspace, err := a.newWorkspace(request.Context(), auth.user.ID, name, slug, false)
	if err != nil {
		a.redirectDashboardError(writer, request, "Unable to create this workspace. Its address may already be in use.")
		return
	}
	a.store.Audit(request.Context(), &auth.user.ID, "workspace_created", clientIP(request))
	http.Redirect(writer, request, "/workspaces/"+workspace.ID.String()+"/open", http.StatusFound)
}

func (a *App) workspaceAction(writer http.ResponseWriter, request *http.Request) {
	auth, ok := a.requireCSRF(writer, request)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 3 {
		http.NotFound(writer, request)
		return
	}
	workspaceID, err := uuid.Parse(parts[1])
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	switch parts[2] {
	case "open":
		a.redirectToWorkspace(writer, request, auth.user.ID, workspaceID)
	case "rename":
		name := strings.TrimSpace(request.FormValue("name"))
		if err = validateWorkspaceName(name); err != nil {
			a.redirectDashboardError(writer, request, err.Error())
			return
		}
		if err = a.store.RenameWorkspace(request.Context(), workspaceID, auth.user.ID, name); err != nil {
			http.NotFound(writer, request)
			return
		}
		a.redirectDashboardMessage(writer, request, "Workspace renamed.")
	case "delete":
		workspace, err := a.store.WorkspaceForUser(request.Context(), auth.user.ID, workspaceID)
		if err != nil || workspace.OwnerID != auth.user.ID || workspace.Personal || request.FormValue("confirm") != workspace.Name {
			a.redirectDashboardError(writer, request, "Type the exact workspace name to delete it.")
			return
		}
		workspace, err = a.store.SoftDeleteWorkspace(request.Context(), workspaceID, auth.user.ID)
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		if err = a.docker.RemoveWorkspace(request.Context(), workspace.ContainerName); err != nil {
			slog.Error("remove workspace container", "workspace", workspace.ID, "error", err)
		}
		a.store.Audit(request.Context(), &auth.user.ID, "workspace_soft_deleted", clientIP(request))
		a.redirectDashboardMessage(writer, request, "Workspace deleted. Its data is retained for recovery.")
	default:
		http.NotFound(writer, request)
	}
}

func (a *App) forgotPassword(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		a.render(writer, "forgot", pageData{Title: "Reset password", Error: "Invalid form."})
		return
	}
	if user, err := a.store.UserByEmail(request.Context(), request.FormValue("email")); err == nil {
		if token, tokenErr := randomToken(); tokenErr == nil && a.store.CreateResetToken(request.Context(), user.ID, token) == nil {
			a.store.Audit(request.Context(), &user.ID, "password_reset_requested", clientIP(request))
			if err = a.sendResetEmail(user.Email, token); err != nil {
				slog.Error("send reset email", "error", err)
			}
		}
	}
	a.render(writer, "forgot", pageData{Title: "Reset password", Message: "If an account exists for that email, a reset link has been sent."})
}

func (a *App) resetPassword(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		a.render(writer, "reset", pageData{Title: "Choose a new password", Error: "Invalid form."})
		return
	}
	password, token := request.FormValue("password"), request.FormValue("token")
	if err := validatePassword(password); err != nil {
		a.render(writer, "reset", pageData{Title: "Choose a new password", ResetToken: token, Error: err.Error()})
		return
	}
	hash, err := hashPassword(password)
	if err == nil {
		_, err = a.store.ResetPassword(request.Context(), token, hash)
	}
	if err != nil {
		a.render(writer, "reset", pageData{Title: "Choose a new password", Error: "This reset link is invalid or has expired."})
		return
	}
	a.render(writer, "login", pageData{Title: "Sign in", Message: "Password updated. Sign in with your new password.", Signup: a.config.SignupEnabled})
}

func (a *App) logout(writer http.ResponseWriter, request *http.Request) {
	auth, ok := a.requireCSRF(writer, request)
	if !ok {
		return
	}
	_ = a.store.DeleteSession(request.Context(), auth.token)
	a.store.Audit(request.Context(), &auth.user.ID, "logout", clientIP(request))
	a.clearSessionCookie(writer)
	http.Redirect(writer, request, "/login", http.StatusFound)
}

func (a *App) serveWorkspace(writer http.ResponseWriter, request *http.Request, slug string) {
	auth, ok := a.auth(request)
	if !ok {
		http.Redirect(writer, request, a.config.portalURL("/login"), http.StatusFound)
		return
	}
	if isUnsafeMethod(request.Method) && !sameOrigin(request, a.config.PublicScheme, requestAuthority(request)) {
		http.Error(writer, "Invalid request origin.", http.StatusForbidden)
		return
	}
	workspace, err := a.store.WorkspaceForUserBySlug(request.Context(), auth.user.ID, slug)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	if err = a.store.SetActiveWorkspace(request.Context(), auth.user.ID, workspace.ID); err != nil {
		http.Error(writer, "Unable to select workspace.", http.StatusInternalServerError)
		return
	}
	target, err := a.docker.Target(request.Context(), workspace.ContainerName)
	if err != nil {
		http.Error(writer, "Workspace is starting. Refresh shortly.", http.StatusServiceUnavailable)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(proxyRequest *http.Request) {
		original(proxyRequest)
		proxyRequest.Host = requestHost(request)
		proxyRequest.Header.Set("X-Forwarded-Host", requestHost(request))
		proxyRequest.Header.Set("X-Forwarded-Proto", a.config.PublicScheme)
	}
	proxy.ErrorHandler = func(response http.ResponseWriter, proxyRequest *http.Request, proxyError error) {
		slog.Error("workspace proxy failed", "workspace", workspace.ID, "error", proxyError)
		http.Error(response, "Workspace is temporarily unavailable.", http.StatusBadGateway)
	}
	proxy.ServeHTTP(writer, request)
}
