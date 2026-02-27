package main

import "time"

const apiTimestampLayout = "2006-01-02T15:04:05.000Z07:00"

func formatAPITimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(apiTimestampLayout)
}
