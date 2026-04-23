package investigation

// TrimHistoryForTest exposes trimHistory for white-box unit testing.
func TrimHistoryForTest(history []AgentMessage, maxMsgs int) []AgentMessage {
	return trimHistory(history, maxMsgs)
}
