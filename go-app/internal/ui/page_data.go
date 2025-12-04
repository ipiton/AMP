package ui

// PageData is the standard data structure passed to templates.
//
// All page templates should receive PageData with page-specific
// data in the Data field.
type PageData struct {
	// Title is the page title (<title> tag)
	Title string

	// Breadcrumbs for navigation
	Breadcrumbs []Breadcrumb

	// Flash message (success/error/warning/info)
	Flash *FlashMessage

	// User information (if authenticated)
	User *User

	// Data is page-specific data
	Data interface{}
}

// Breadcrumb represents a navigation breadcrumb.
type Breadcrumb struct {
	// Name is the display text
	Name string

	// URL is the link destination
	URL string
}

// FlashMessage represents a temporary message to display.
type FlashMessage struct {
	// Type is the message type: "success", "error", "warning", "info"
	Type string

	// Message is the display text
	Message string
}

// User represents the current user.
type User struct {
	// ID is the user identifier
	ID string

	// Name is the display name
	Name string

	// Email is the user email
	Email string

	// Roles are user roles (e.g., "admin", "viewer")
	Roles []string

	// Avatar is the avatar URL
	Avatar string
}

// NewPageData creates a new PageData with defaults.
func NewPageData(title string) *PageData {
	return &PageData{
		Title:       title,
		Breadcrumbs: []Breadcrumb{},
	}
}

// AddBreadcrumb adds a breadcrumb to the page.
func (p *PageData) AddBreadcrumb(name, url string) {
	p.Breadcrumbs = append(p.Breadcrumbs, Breadcrumb{
		Name: name,
		URL:  url,
	})
}

// SetFlash sets a flash message.
func (p *PageData) SetFlash(msgType, message string) {
	p.Flash = &FlashMessage{
		Type:    msgType,
		Message: message,
	}
}

// SetUser sets the current user.
func (p *PageData) SetUser(user *User) {
	p.User = user
}
