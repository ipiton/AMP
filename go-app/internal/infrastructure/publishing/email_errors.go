package publishing

import (
	"errors"
	"net"
	"net/textproto"
	"strings"
)

// ProviderEmail — идентификатор провайдера для метрик и ошибок.
const ProviderEmail = "email"

// classifyEmailError классифицирует ошибку SMTP для метрик и retry-логики.
// Возвращает строку-категорию:
//   - "auth_error"         — SMTP 535 (authentication failed)
//   - "rate_limit"         — SMTP 421/451/452 (try again later)
//   - "invalid_recipient"  — SMTP 550/551/552/553 (bad recipient)
//   - "server_error"       — прочие 5xx
//   - "tls_error"          — TLS handshake / certificate
//   - "network_error"      — сетевые ошибки, timeout, dial fail
//   - "unknown"            — всё остальное
func classifyEmailError(err error) string {
	if err == nil {
		return "unknown"
	}

	msg := err.Error()

	// TLS errors — проверяем до network, т.к. могут оборачиваться
	if strings.Contains(msg, "tls:") ||
		strings.Contains(msg, "x509:") ||
		strings.Contains(msg, "certificate") ||
		strings.Contains(msg, "handshake") {
		return "tls_error"
	}

	// Network / timeout errors (обе ветки netErr.Timeout() возвращали одно — упрощено)
	var netErr net.Error
	if errors.As(err, &netErr) {
		return "network_error"
	}
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "EOF") {
		return "network_error"
	}

	// SMTP-коды — через textproto.Error (корректно unwrap-ает обёрнутые ошибки net/smtp,
	// например fmt.Errorf("email: AUTH: %w", err) где err — *textproto.Error).
	// Используем .Code напрямую — не дублируем логику парсинга из textproto пакета.
	var textErr *textproto.Error
	if errors.As(err, &textErr) {
		return classifyByCode(textErr.Code)
	}

	return "unknown"
}

// classifyByCode преобразует числовой SMTP-код в категорию ошибки.
func classifyByCode(code int) string {
	switch code {
	case 535:
		return "auth_error"
	case 421, 451, 452:
		return "rate_limit"
	case 550, 551, 552, 553:
		return "invalid_recipient"
	}
	if code >= 500 && code < 600 {
		return "server_error"
	}
	return "unknown"
}
