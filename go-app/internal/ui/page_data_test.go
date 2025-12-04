package ui

import (
	"testing"
)

// TestNewPageData tests PageData constructor.
func TestNewPageData(t *testing.T) {
	title := "Test Page"
	pageData := NewPageData(title)

	if pageData == nil {
		t.Fatal("Expected non-nil PageData")
	}
	if pageData.Title != title {
		t.Errorf("Expected Title=%q, got %q", title, pageData.Title)
	}
	if pageData.Breadcrumbs == nil {
		t.Error("Expected Breadcrumbs to be initialized")
	}
	if len(pageData.Breadcrumbs) != 0 {
		t.Errorf("Expected empty Breadcrumbs, got %d items", len(pageData.Breadcrumbs))
	}
	if pageData.Flash != nil {
		t.Error("Expected Flash to be nil initially")
	}
	if pageData.User != nil {
		t.Error("Expected User to be nil initially")
	}
	if pageData.Data != nil {
		t.Error("Expected Data to be nil initially")
	}
}

// TestAddBreadcrumb tests adding breadcrumbs.
func TestAddBreadcrumb(t *testing.T) {
	pageData := NewPageData("Test")

	// Add first breadcrumb
	pageData.AddBreadcrumb("Home", "/")
	if len(pageData.Breadcrumbs) != 1 {
		t.Fatalf("Expected 1 breadcrumb, got %d", len(pageData.Breadcrumbs))
	}
	if pageData.Breadcrumbs[0].Name != "Home" {
		t.Errorf("Expected Name='Home', got %q", pageData.Breadcrumbs[0].Name)
	}
	if pageData.Breadcrumbs[0].URL != "/" {
		t.Errorf("Expected URL='/', got %q", pageData.Breadcrumbs[0].URL)
	}

	// Add second breadcrumb
	pageData.AddBreadcrumb("Dashboard", "/dashboard")
	if len(pageData.Breadcrumbs) != 2 {
		t.Fatalf("Expected 2 breadcrumbs, got %d", len(pageData.Breadcrumbs))
	}
	if pageData.Breadcrumbs[1].Name != "Dashboard" {
		t.Errorf("Expected Name='Dashboard', got %q", pageData.Breadcrumbs[1].Name)
	}
	if pageData.Breadcrumbs[1].URL != "/dashboard" {
		t.Errorf("Expected URL='/dashboard', got %q", pageData.Breadcrumbs[1].URL)
	}

	// Add third breadcrumb (current page, no URL)
	pageData.AddBreadcrumb("Details", "")
	if len(pageData.Breadcrumbs) != 3 {
		t.Fatalf("Expected 3 breadcrumbs, got %d", len(pageData.Breadcrumbs))
	}
	if pageData.Breadcrumbs[2].Name != "Details" {
		t.Errorf("Expected Name='Details', got %q", pageData.Breadcrumbs[2].Name)
	}
	if pageData.Breadcrumbs[2].URL != "" {
		t.Errorf("Expected empty URL, got %q", pageData.Breadcrumbs[2].URL)
	}
}

// TestSetFlash tests setting flash messages.
func TestSetFlash(t *testing.T) {
	pageData := NewPageData("Test")

	tests := []struct {
		msgType string
		message string
	}{
		{"success", "Operation completed successfully"},
		{"error", "An error occurred"},
		{"warning", "Please be careful"},
		{"info", "For your information"},
	}

	for _, tt := range tests {
		t.Run(tt.msgType, func(t *testing.T) {
			pageData.SetFlash(tt.msgType, tt.message)

			if pageData.Flash == nil {
				t.Fatal("Expected Flash to be set")
			}
			if pageData.Flash.Type != tt.msgType {
				t.Errorf("Expected Type=%q, got %q", tt.msgType, pageData.Flash.Type)
			}
			if pageData.Flash.Message != tt.message {
				t.Errorf("Expected Message=%q, got %q", tt.message, pageData.Flash.Message)
			}
		})
	}
}

// TestSetUser tests setting user information.
func TestSetUser(t *testing.T) {
	pageData := NewPageData("Test")

	user := &User{
		ID:     "user123",
		Name:   "John Doe",
		Email:  "john@example.com",
		Roles:  []string{"admin", "editor"},
		Avatar: "https://example.com/avatar.jpg",
	}

	pageData.SetUser(user)

	if pageData.User == nil {
		t.Fatal("Expected User to be set")
	}
	if pageData.User.ID != user.ID {
		t.Errorf("Expected ID=%q, got %q", user.ID, pageData.User.ID)
	}
	if pageData.User.Name != user.Name {
		t.Errorf("Expected Name=%q, got %q", user.Name, pageData.User.Name)
	}
	if pageData.User.Email != user.Email {
		t.Errorf("Expected Email=%q, got %q", user.Email, pageData.User.Email)
	}
	if len(pageData.User.Roles) != len(user.Roles) {
		t.Errorf("Expected %d roles, got %d", len(user.Roles), len(pageData.User.Roles))
	}
	if pageData.User.Avatar != user.Avatar {
		t.Errorf("Expected Avatar=%q, got %q", user.Avatar, pageData.User.Avatar)
	}
}

// TestPageData_FullExample tests complete PageData usage.
func TestPageData_FullExample(t *testing.T) {
	// Create page data
	pageData := NewPageData("Alert Details")

	// Add breadcrumbs
	pageData.AddBreadcrumb("Home", "/")
	pageData.AddBreadcrumb("Alerts", "/alerts")
	pageData.AddBreadcrumb("Details", "")

	// Set flash message
	pageData.SetFlash("success", "Alert resolved successfully")

	// Set user
	user := &User{
		ID:    "admin1",
		Name:  "Admin User",
		Email: "admin@example.com",
		Roles: []string{"admin"},
	}
	pageData.SetUser(user)

	// Set page-specific data
	pageData.Data = map[string]interface{}{
		"AlertID":   "alert-123",
		"Severity":  "critical",
		"Status":    "resolved",
		"CreatedAt": "2025-11-19",
	}

	// Verify all fields
	if pageData.Title != "Alert Details" {
		t.Errorf("Expected Title='Alert Details', got %q", pageData.Title)
	}
	if len(pageData.Breadcrumbs) != 3 {
		t.Errorf("Expected 3 breadcrumbs, got %d", len(pageData.Breadcrumbs))
	}
	if pageData.Flash == nil || pageData.Flash.Type != "success" {
		t.Error("Expected success flash message")
	}
	if pageData.User == nil || pageData.User.Name != "Admin User" {
		t.Error("Expected user to be set")
	}
	if pageData.Data == nil {
		t.Error("Expected Data to be set")
	}
}

// TestBreadcrumb_Structure tests Breadcrumb struct.
func TestBreadcrumb_Structure(t *testing.T) {
	breadcrumb := Breadcrumb{
		Name: "Test Page",
		URL:  "/test",
	}

	if breadcrumb.Name != "Test Page" {
		t.Errorf("Expected Name='Test Page', got %q", breadcrumb.Name)
	}
	if breadcrumb.URL != "/test" {
		t.Errorf("Expected URL='/test', got %q", breadcrumb.URL)
	}
}

// TestFlashMessage_Structure tests FlashMessage struct.
func TestFlashMessage_Structure(t *testing.T) {
	flash := FlashMessage{
		Type:    "error",
		Message: "Test error message",
	}

	if flash.Type != "error" {
		t.Errorf("Expected Type='error', got %q", flash.Type)
	}
	if flash.Message != "Test error message" {
		t.Errorf("Expected Message='Test error message', got %q", flash.Message)
	}
}

// TestUser_Structure tests User struct.
func TestUser_Structure(t *testing.T) {
	user := User{
		ID:     "user1",
		Name:   "Test User",
		Email:  "test@example.com",
		Roles:  []string{"viewer"},
		Avatar: "https://example.com/avatar.png",
	}

	if user.ID != "user1" {
		t.Errorf("Expected ID='user1', got %q", user.ID)
	}
	if user.Name != "Test User" {
		t.Errorf("Expected Name='Test User', got %q", user.Name)
	}
	if user.Email != "test@example.com" {
		t.Errorf("Expected Email='test@example.com', got %q", user.Email)
	}
	if len(user.Roles) != 1 || user.Roles[0] != "viewer" {
		t.Errorf("Expected Roles=['viewer'], got %v", user.Roles)
	}
	if user.Avatar != "https://example.com/avatar.png" {
		t.Errorf("Expected Avatar='https://example.com/avatar.png', got %q", user.Avatar)
	}
}

// TestPageData_MultipleFlashUpdates tests that flash can be updated.
func TestPageData_MultipleFlashUpdates(t *testing.T) {
	pageData := NewPageData("Test")

	// Set first flash
	pageData.SetFlash("info", "First message")
	if pageData.Flash.Type != "info" || pageData.Flash.Message != "First message" {
		t.Error("First flash not set correctly")
	}

	// Update flash
	pageData.SetFlash("error", "Second message")
	if pageData.Flash.Type != "error" || pageData.Flash.Message != "Second message" {
		t.Error("Flash not updated correctly")
	}
}

// TestPageData_NilUser tests setting nil user.
func TestPageData_NilUser(t *testing.T) {
	pageData := NewPageData("Test")

	// Set user first
	pageData.SetUser(&User{ID: "test"})
	if pageData.User == nil {
		t.Error("User should be set")
	}

	// Set to nil
	pageData.SetUser(nil)
	if pageData.User != nil {
		t.Error("User should be nil")
	}
}
