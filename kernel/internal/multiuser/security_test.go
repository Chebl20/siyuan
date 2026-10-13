package multiuser

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestPasswordHashDoesNotAcceptDifferentPassword(t *testing.T) {
	hash, err := hashPassword("a password with enough length")
	if err != nil {
		t.Fatal(err)
	}
	if !comparePassword(hash, "a password with enough length") {
		t.Fatal("correct password was rejected")
	}
	if comparePassword(hash, "a different password with length") {
		t.Fatal("different password was accepted")
	}
}

func TestWorkspaceHostAndOriginValidation(t *testing.T) {
	app := App{config: Config{BaseDomain: "example.test", AppHost: "app.example.test", PublicScheme: "https"}}
	slug, ok := app.workspaceSlug("team-notes.example.test")
	if !ok || slug != "team-notes" {
		t.Fatalf("workspace slug = %q, %t", slug, ok)
	}
	if _, ok = app.workspaceSlug("other.team-notes.example.test"); ok {
		t.Fatal("nested host was accepted")
	}
	request := httptest.NewRequest("POST", "https://team-notes.example.test/api", nil)
	request.Header.Set("Origin", "https://team-notes.example.test")
	if !sameOrigin(request, "https", "team-notes.example.test") {
		t.Fatal("same origin request was rejected")
	}
	config := Config{PublicPort: "8080"}
	if authority := config.publicAuthority("team-notes.example.test"); authority != "team-notes.example.test:8080" {
		t.Fatalf("public authority = %q", authority)
	}
	request.Header.Set("Origin", "https://other.example.test")
	if sameOrigin(request, "https", "team-notes.example.test") {
		t.Fatal("cross-origin request was accepted")
	}
}

func TestLoginLimiterBlocksAfterFiveFailures(t *testing.T) {
	limiter := newLoginLimiter()
	for i := 0; i < 5; i++ {
		limiter.Fail("127.0.0.1:user@example.test")
	}
	if limiter.Allow("127.0.0.1:user@example.test") {
		t.Fatal("login limiter did not block repeated failures")
	}
	limiter.mu.Lock()
	attempt := limiter.attempts["127.0.0.1:user@example.test"]
	attempt.until = time.Now().Add(-time.Second)
	limiter.attempts["127.0.0.1:user@example.test"] = attempt
	limiter.mu.Unlock()
	if !limiter.Allow("127.0.0.1:user@example.test") {
		t.Fatal("login limiter did not release after the lock interval")
	}
}
