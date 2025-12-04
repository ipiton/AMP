package ui

import "errors"

// Template engine errors.
var (
	// ErrTemplateNotFound is returned when template is not found.
	ErrTemplateNotFound = errors.New("template not found")

	// ErrTemplateRender is returned when template rendering fails.
	ErrTemplateRender = errors.New("template render failed")

	// ErrTemplateLoad is returned when template loading fails.
	ErrTemplateLoad = errors.New("template load failed")
)
